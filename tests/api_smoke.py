#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
野生拼车墙 —— API 冒烟与并发正确性测试。

设计原则
--------
1. **零外部计费依赖**。本项目不调用任何外部付费 API，因此整轮测试的预期花费为 ¥0。
   Postgres 与 Redis 是自托管基础设施，不算外部依赖。
2. **不用 pytest**。测试逻辑本身很直白，但要跑在容器里；少一个依赖就少一处
   「装不上/版本冲突」的失败面。自带一个 30 行的极简 runner 足够。
3. **兼容 Python 3.9**。容器里是 3.12，但开发机上常见 3.9，
   因此不用 `int | None` 这类 3.10+ 语法。

最重要的用例是 test_concurrent_last_seat：它是 Requirements TR-1
（强一致性拼车位并发抢占）唯一有意义的验收方式。
"""

import json
import os
import random
import string
import sys
import threading
import time
import traceback
from concurrent.futures import ThreadPoolExecutor
from typing import Any, Dict, List, Optional, Tuple

import requests

BASE = os.environ.get("API_BASE", "http://localhost:31810").rstrip("/")
WS_BASE = os.environ.get("WS_BASE", BASE.replace("http://", "ws://").replace("https://", "wss://"))
# 并发抢位的并发度。32 足以在毫秒级窗口内制造真实竞争。
RACERS = int(os.environ.get("RACERS", "32"))
TIMEOUT = 20

# ---------------------------------------------------------------- 极简 runner

_TESTS = []
_ONLY = os.environ.get("ONLY", "")


def test(name):
    def deco(fn):
        _TESTS.append((name, fn))
        return fn

    return deco


class Failure(AssertionError):
    pass


def expect(cond, msg):
    if not cond:
        raise Failure(msg)


def expect_eq(got, want, what):
    if got != want:
        raise Failure("%s: 实际 %r，期望 %r" % (what, got, want))


# ------------------------------------------------------------------ HTTP 封装


class Api(object):
    """一个持有 token 的会话。"""

    def __init__(self, token=None):
        # type: (Optional[str]) -> None
        self.s = requests.Session()
        self.token = token
        self.user = None  # type: Optional[Dict[str, Any]]

    def _headers(self):
        h = {"Content-Type": "application/json"}
        if self.token:
            h["Authorization"] = "Bearer " + self.token
        return h

    def raw(self, method, path, body=None):
        # type: (str, str, Optional[dict]) -> requests.Response
        return self.s.request(
            method,
            BASE + path,
            headers=self._headers(),
            data=None if body is None else json.dumps(body),
            timeout=TIMEOUT,
        )

    def call(self, method, path, body=None):
        # type: (str, str, Optional[dict]) -> Tuple[int, str, Any]
        """返回 (status, error_code, data)。error_code 为空串表示成功。"""
        r = self.raw(method, path, body)
        try:
            env = r.json()
        except ValueError:
            raise Failure("%s %s 返回了非 JSON 内容: %s" % (method, path, r.text[:200]))
        if env.get("ok"):
            return r.status_code, "", env.get("data")
        err = env.get("error") or {}
        return r.status_code, err.get("code", "UNKNOWN"), err

    def ok(self, method, path, body=None):
        # type: (str, str, Optional[dict]) -> Any
        status, code, data = self.call(method, path, body)
        if code:
            raise Failure(
                "%s %s 预期成功，实际 %d %s: %s"
                % (method, path, status, code, data.get("message", ""))
            )
        return data


def rand_name(prefix="t"):
    return prefix + "".join(random.choice(string.ascii_lowercase + string.digits) for _ in range(9))


def new_user(city="测试城"):
    # type: (str) -> Api
    """注册一个新账号并返回已登录的会话。"""
    api = Api()
    username = rand_name("u")
    status, code, data = api.call(
        "POST",
        "/api/auth/register",
        {"username": username, "password": "test1234", "nickname": username, "city": city},
    )
    if code == "RATE_LIMITED":
        raise Failure(
            "注册被限流（RATE_LIMITED）。本套测试需要批量建号，"
            "请用 docker-compose.test.yml 覆盖启动（其中放宽了 RATE_LOGIN_PER_MIN）。"
        )
    if code:
        raise Failure("注册失败 %s: %s" % (code, data.get("message", "")))
    api.token = data["tokens"]["access_token"]
    api.user = data["user"]
    return api


def make_room(owner, male=0, female=0, any_seats=6, min_viable=2, hours=6, title=None):
    # type: (Api, int, int, int, int, int, Optional[str]) -> Dict[str, Any]
    """开一辆车，返回 RoomCard。"""
    start = time.time() + hours * 3600
    lt = time.localtime(start)
    # 带上本地时区偏移，避免服务端按 UTC 解释导致开局时间错 8 小时。
    off = -(time.altzone if lt.tm_isdst else time.timezone)
    sign = "+" if off >= 0 else "-"
    off = abs(off)
    start_at = "%04d-%02d-%02dT%02d:%02d:00%s%02d:%02d" % (
        lt.tm_year, lt.tm_mon, lt.tm_mday, lt.tm_hour, lt.tm_min,
        sign, off // 3600, (off % 3600) // 60,
    )
    body = {
        "title": title or "自动化测试拼车局",
        "script_name": "测试剧本-" + rand_name(""),
        "venue_name": "测试密室店",
        "city": "测试城",
        "address": "测试路 1 号",
        "room_type": "SCRIPT",
        "difficulty": 3,
        "theme": "硬核推理",
        "notes": "自动化测试创建",
        "start_at": start_at,
        "male_seats": male,
        "female_seats": female,
        "any_seats": any_seats,
        "min_viable": min_viable,
        "owner_seat": "ANY" if any_seats > 0 else ("MALE" if male > 0 else "FEMALE"),
    }
    return owner.ok("POST", "/api/rooms", body)


def audit(api, room_id):
    # type: (Api, int) -> Dict[str, Any]
    return api.ok("GET", "/api/rooms/%d/audit" % room_id)


# ==================================================================== 用例


@test("健康探针与就绪探针")
def test_health():
    r = requests.get(BASE + "/healthz", timeout=TIMEOUT)
    expect_eq(r.status_code, 200, "/healthz 状态码")

    r = requests.get(BASE + "/readyz", timeout=TIMEOUT)
    expect_eq(r.status_code, 200, "/readyz 状态码")
    # 探针刻意不套业务信封：负载均衡与 Docker healthcheck 不应依赖应用层
    # 的响应约定，否则信封一改探针就集体失灵。
    d = r.json()
    expect(d["ok"], "/readyz 应报告就绪")
    expect(d["checks"]["postgres"]["ok"], "Postgres 应连通")
    expect(d["checks"]["redis"]["ok"], "Redis 应连通")
    # 三层锁必须都在位，否则后面的并发用例失去意义。
    expect(d["slot_lock"]["l1_local_shards"] > 0, "L1 分片锁应启用")
    expect(d["slot_lock"]["l3_db_pessimistic"], "L3 数据库悲观锁应启用")


@test("枚举与标签目录由后端供给")
def test_meta():
    api = Api()
    tags = api.ok("GET", "/api/meta/tags")
    expect(len(tags) >= 3, "标签目录不应为空")
    for t in tags:
        expect(t["label"] and t["phrase"], "标签 %s 缺少文案" % t["code"])

    enums = api.ok("GET", "/api/meta/enums")
    for key in ("seat_genders", "room_types", "statuses", "themes"):
        expect(len(enums[key]) > 0, "枚举 %s 不应为空" % key)
    expect_eq(enums["max_tags"], 3, "最多标签数")


@test("注册 / 登录 / 刷新令牌")
def test_auth():
    api = new_user()
    me = api.ok("GET", "/api/users/me")
    expect_eq(me["username"], api.user["username"], "当前用户名")
    # 空标签集必须是 []，不能是 null —— 否则前端 .map() 直接崩。
    expect(isinstance(me["tags"], list), "tags 必须是数组，不能是 null")

    # 未带令牌访问受保护端点必须 401。
    anon = Api()
    status, code, _ = anon.call("GET", "/api/users/me")
    expect_eq(status, 401, "未认证访问 /api/users/me 的状态码")

    # 用 refresh token 冒充 access token 必须被拒（kind 混用防护）。
    login = api.ok("POST", "/api/auth/login", {"username": api.user["username"], "password": "test1234"})
    refresh_tok = login["tokens"]["refresh_token"]
    imposter = Api(refresh_tok)
    status, code, _ = imposter.call("GET", "/api/users/me")
    expect_eq(status, 401, "refresh token 当 access token 用的状态码")

    # 刷新本身应当成功并给出新的一对令牌。
    pair = api.ok("POST", "/api/auth/refresh", {"refresh_token": refresh_tok})
    expect(pair["tokens"]["access_token"], "刷新后应返回新的 access token")


@test("交付契约：README 与登录页宣称的演示账号真能登进去")
def test_demo_accounts_usable():
    # 这条用例存在的理由：登录页有个「快速体验」按钮，README 也列了测试账号。
    # 这些凭据是硬编码的字符串，和后端种子数据没有编译期关联——种子改名或改
    # 口令时不会有任何报错，只会让交付出去的第一个入口直接失败。
    demo_password = "jbs12345"
    for username in ("alice", "bob", "grace"):
        api = Api()
        status, code, body = api.call(
            "POST", "/api/auth/login", {"username": username, "password": demo_password}
        )
        expect_eq(status, 200,
                  "演示账号 %s 应能用 README 公示的口令登录（实际 %s）" % (username, code))
        api.token = body["tokens"]["access_token"]
        me = api.ok("GET", "/api/users/me")
        expect_eq(me["username"], username, "演示账号身份")
        expect(me["nickname"], "演示账号应有昵称，否则墙上头像没有文案可显示")


@test("开车：席位配置校验与账目初始化")
def test_create_room():
    owner = new_user()
    card = make_room(owner, male=3, female=2, any_seats=1, min_viable=4)
    snap = card["snapshot"]

    expect_eq(snap["capacity"], 6, "总席位")
    # 车主自动占一个席位，走的是和普通用户完全相同的账目通路。
    expect_eq(snap["occupied"], 1, "开车后已占席位（车主自己）")
    expect_eq(snap["remaining"], 5, "开车后剩余席位")
    expect_eq(snap["status"], "RECRUITING", "初始状态")
    expect_eq(snap["headline"], "6缺5", "席位主标文案")
    expect_eq(len(snap["seats"]), 3, "席位桶应固定 3 类")
    expect_eq([s["gender"] for s in snap["seats"]], ["MALE", "FEMALE", "ANY"], "席位桶顺序")
    expect(card["am_owner"], "创建者应被标记为车主")
    expect(card["am_on_car"], "创建者应已在车上")

    # 席位之和必须等于总人数。
    status, code, err = owner.call(
        "POST",
        "/api/rooms",
        dict(
            title="席位不合法", script_name="x", venue_name="y", city="测试城", address="",
            room_type="SCRIPT", difficulty=3, theme="", notes="",
            start_at="2030-01-01T20:00:00+08:00",
            male_seats=0, female_seats=0, any_seats=0, min_viable=1, owner_seat="ANY",
        ),
    )
    expect(code != "", "总人数为 0 的车应被拒绝")

    # 开局时间必须留出招募窗口。
    status, code, err = owner.call(
        "POST",
        "/api/rooms",
        dict(
            title="时间已过", script_name="x", venue_name="y", city="测试城", address="",
            room_type="SCRIPT", difficulty=3, theme="", notes="",
            start_at="2020-01-01T20:00:00+08:00",
            male_seats=0, female_seats=0, any_seats=4, min_viable=2, owner_seat="ANY",
        ),
    )
    expect_eq(code, "START_AT_IN_PAST", "过期开局时间的错误码")

    audit_res = audit(owner, card["room"]["id"])
    expect_eq(audit_res["drift"], 0, "新车席位账目漂移")


@test("★ 并发抢最后一个席位：绝对不允许超载")
def test_concurrent_last_seat():
    """
    Requirements TR-1 的核心验收。

    构造一辆 capacity=8 的车（车主占 1），先用 6 个用户把它填到只剩 1 个位，
    然后让 RACERS 个用户在同一瞬间抢这最后一个位。

    断言：
      - 恰好 1 人成功
      - 其余全部收到席位类冲突错误（而不是 500）
      - occupied == capacity，一个不多一个不少
      - /audit 的 drift == 0（聚合计数与实际成员行数一致）
    """
    owner = new_user()
    capacity = 8
    card = make_room(owner, any_seats=capacity, min_viable=2)
    room_id = card["room"]["id"]

    # 填到只剩 1 个位。
    fillers = [new_user() for _ in range(capacity - 2)]
    for f in fillers:
        f.ok("POST", "/api/rooms/%d/join" % room_id, {"seat_gender": "ANY"})

    snap = owner.ok("GET", "/api/rooms/%d" % room_id)["snapshot"]
    expect_eq(snap["remaining"], 1, "预热后应恰好剩 1 个席位")

    # 预先建好账号并登录，让竞争窗口只包含 join 这一次调用本身。
    racers = [new_user() for _ in range(RACERS)]

    barrier = threading.Barrier(RACERS)
    results = [None] * RACERS  # type: List[Optional[Tuple[int, str]]]

    def race(i):
        api = racers[i]
        # 用屏障把所有线程卡在同一起跑线，最大化真实竞争。
        barrier.wait()
        status, code, _ = api.call("POST", "/api/rooms/%d/join" % room_id, {"seat_gender": "ANY"})
        results[i] = (status, code)

    with ThreadPoolExecutor(max_workers=RACERS) as pool:
        list(pool.map(race, range(RACERS)))

    winners = [r for r in results if r and r[1] == ""]
    losers = [r for r in results if r and r[1] != ""]

    expect_eq(len(winners), 1, "抢到最后一个席位的人数")

    # 失败者必须是明确的业务拒绝，不能是 500，也不能是限流掩盖了真实结果。
    for status, code in losers:
        expect(
            code in ("SLOT_FULL", "SEAT_GENDER_FULL", "ROOM_NOT_RECRUITING", "SLOT_LOCK_BUSY"),
            "落败者应收到席位冲突类错误，实际 %d %s" % (status, code),
        )
        expect(status < 500, "落败者不应收到 5xx，实际 %d %s" % (status, code))

    final = owner.ok("GET", "/api/rooms/%d" % room_id)["snapshot"]
    expect_eq(final["occupied"], capacity, "最终占用席位数（超载即为致命缺陷）")
    expect_eq(final["remaining"], 0, "最终剩余席位")

    a = audit(owner, room_id)
    expect_eq(a["drift"], 0, "并发抢位后的账目漂移")
    expect(a["consistent"], "并发抢位后账目应一致")
    expect_eq(a["aggregate_counts"], a["actual_members"], "聚合计数与实际成员数")


@test("同一用户并发重复上车：幂等，不重复占位")
def test_join_idempotent():
    owner = new_user()
    card = make_room(owner, any_seats=6, min_viable=2)
    room_id = card["room"]["id"]

    joiner = new_user()
    n = 8
    barrier = threading.Barrier(n)
    codes = [None] * n

    def go(i):
        barrier.wait()
        _, code, _ = joiner.call("POST", "/api/rooms/%d/join" % room_id, {"seat_gender": "ANY"})
        codes[i] = code

    with ThreadPoolExecutor(max_workers=n) as pool:
        list(pool.map(go, range(n)))

    # 允许多次返回成功（幂等语义：重复上车视为成功），但席位只能扣 1 个。
    snap = owner.ok("GET", "/api/rooms/%d" % room_id)["snapshot"]
    expect_eq(snap["occupied"], 2, "车主 + 该用户共 2 个席位，重复上车不得重复扣减")

    a = audit(owner, room_id)
    expect_eq(a["drift"], 0, "重复上车后的账目漂移")

    members = owner.ok("GET", "/api/rooms/%d" % room_id)["members"]
    ids = [m["user"]["id"] for m in members]
    expect_eq(len(ids), len(set(ids)), "同一用户不应出现多条在座成员记录")


@test("满员自动锁车，有人退车后自动回到招募中")
def test_lock_and_reopen():
    owner = new_user()
    capacity = 3
    card = make_room(owner, any_seats=capacity, min_viable=2)
    room_id = card["room"]["id"]

    a1 = new_user()
    a2 = new_user()
    a1.ok("POST", "/api/rooms/%d/join" % room_id, {"seat_gender": "ANY"})
    a2.ok("POST", "/api/rooms/%d/join" % room_id, {"seat_gender": "ANY"})

    snap = owner.ok("GET", "/api/rooms/%d" % room_id)["snapshot"]
    expect_eq(snap["occupied"], capacity, "应已满员")
    expect_eq(snap["status"], "LOCKED", "满员应自动锁车")
    expect(not snap["accepts_join"], "锁车后不应接受上车")

    # 满员后再有人上车必须被拒。
    late = new_user()
    _, code, _ = late.call("POST", "/api/rooms/%d/join" % room_id, {"seat_gender": "ANY"})
    expect(code in ("SLOT_FULL", "ROOM_NOT_RECRUITING"), "满员后上车应被拒，实际 %s" % code)

    # 有人退车 → 席位释放 → 回到招募中。
    a2.ok("POST", "/api/rooms/%d/leave" % room_id)
    snap = owner.ok("GET", "/api/rooms/%d" % room_id)["snapshot"]
    expect_eq(snap["status"], "RECRUITING", "退车后应回到招募中")
    expect_eq(snap["remaining"], 1, "退车后应释放 1 个席位")
    expect(snap["accepts_join"], "回到招募中后应重新接受上车")

    expect_eq(audit(owner, room_id)["drift"], 0, "锁车/回流后的账目漂移")


@test("席位性别配额独立生效")
def test_seat_gender_quota():
    owner = new_user()
    # 车主坐男席，剩 0 男 / 2 女。
    card = make_room(owner, male=1, female=2, any_seats=0, min_viable=2)
    room_id = card["room"]["id"]

    snap = card["snapshot"]
    male_bucket = [s for s in snap["seats"] if s["gender"] == "MALE"][0]
    expect_eq(male_bucket["remaining"], 0, "男席应已被车主占满")

    # 男席已满，但整车还有余量 —— 必须按席位类别拒绝，而不是笼统放行。
    u = new_user()
    _, code, _ = u.call("POST", "/api/rooms/%d/join" % room_id, {"seat_gender": "MALE"})
    expect_eq(code, "SEAT_GENDER_FULL", "男席已满时的错误码")

    # 女席还有位，同一个人应当能上车。
    u.ok("POST", "/api/rooms/%d/join" % room_id, {"seat_gender": "FEMALE"})
    expect_eq(audit(owner, room_id)["drift"], 0, "按性别席位上车后的账目漂移")


@test("权限：车主不能退车、非车主不能踢人、非成员不能看消息")
def test_authz():
    owner = new_user()
    card = make_room(owner, any_seats=4, min_viable=2)
    room_id = card["room"]["id"]

    member = new_user()
    member.ok("POST", "/api/rooms/%d/join" % room_id, {"seat_gender": "ANY"})
    outsider = new_user()

    _, code, _ = owner.call("POST", "/api/rooms/%d/leave" % room_id)
    expect_eq(code, "OWNER_CANNOT_LEAVE", "车主退车的错误码")

    _, code, _ = member.call(
        "POST", "/api/rooms/%d/kick" % room_id, {"user_id": owner.user["id"], "reason": "试试"}
    )
    expect_eq(code, "NOT_ROOM_OWNER", "非车主踢人的错误码")

    _, code, _ = outsider.call("GET", "/api/rooms/%d/messages" % room_id)
    expect_eq(code, "NOT_ROOM_MEMBER", "非成员读取消息的错误码")

    _, code, _ = outsider.call("POST", "/api/rooms/%d/cancel" % room_id, {"reason": "x"})
    expect_eq(code, "NOT_ROOM_OWNER", "非车主解散的错误码")

    # 车主踢人应当成功并释放席位。
    before = owner.ok("GET", "/api/rooms/%d" % room_id)["snapshot"]["occupied"]
    owner.ok("POST", "/api/rooms/%d/kick" % room_id, {"user_id": member.user["id"], "reason": "测试"})
    after = owner.ok("GET", "/api/rooms/%d" % room_id)["snapshot"]["occupied"]
    expect_eq(after, before - 1, "踢人后应释放 1 个席位")
    expect_eq(audit(owner, room_id)["drift"], 0, "踢人后的账目漂移")


@test("聊天：房内序号单调、client_msg_id 幂等")
def test_chat_basic():
    owner = new_user()
    card = make_room(owner, any_seats=4, min_viable=2)
    room_id = card["room"]["id"]

    # 开车会产生一条系统消息，因此起点 seq 不为 0。
    bf = owner.ok("GET", "/api/rooms/%d/messages" % room_id)
    base_seq = bf["latest_seq"]
    expect(base_seq >= 1, "开车应产生系统消息，latest_seq 应 >= 1")
    expect(isinstance(bf["messages"], list), "messages 必须是数组")

    seqs = []
    for i in range(5):
        m = owner.ok(
            "POST",
            "/api/rooms/%d/messages" % room_id,
            {"content": "第 %d 条测试消息" % i, "msg_type": "TEXT", "client_msg_id": rand_name("c")},
        )
        seqs.append(m["seq"])

    expect_eq(seqs, sorted(seqs), "房内序号必须单调递增")
    expect_eq(len(set(seqs)), len(seqs), "房内序号不得重复")
    expect_eq(seqs[0], base_seq + 1, "序号必须连续，不得跳号")

    # 同一个 client_msg_id 重发必须返回同一条消息，而不是插入第二条。
    cid = rand_name("idem")
    m1 = owner.ok(
        "POST",
        "/api/rooms/%d/messages" % room_id,
        {"content": "幂等测试", "msg_type": "TEXT", "client_msg_id": cid},
    )
    m2 = owner.ok(
        "POST",
        "/api/rooms/%d/messages" % room_id,
        {"content": "幂等测试", "msg_type": "TEXT", "client_msg_id": cid},
    )
    expect_eq(m2["seq"], m1["seq"], "相同 client_msg_id 应返回同一条消息")
    expect_eq(m2["id"], m1["id"], "相同 client_msg_id 不应插入新行")

    # 空消息与超长消息必须被拒。
    _, code, _ = owner.call(
        "POST",
        "/api/rooms/%d/messages" % room_id,
        {"content": "   ", "msg_type": "TEXT", "client_msg_id": rand_name("c")},
    )
    expect(code in ("MESSAGE_EMPTY", "VALIDATION_FAILED"), "空消息应被拒，实际 %s" % code)

    _, code, _ = owner.call(
        "POST",
        "/api/rooms/%d/messages" % room_id,
        {"content": "长" * 2000, "msg_type": "TEXT", "client_msg_id": rand_name("c")},
    )
    expect(code in ("MESSAGE_TOO_LONG", "VALIDATION_FAILED"), "超长消息应被拒，实际 %s" % code)


@test("离线消息：增量拉取与已读水位")
def test_offline_backfill():
    owner = new_user()
    card = make_room(owner, any_seats=4, min_viable=2)
    room_id = card["room"]["id"]

    start = owner.ok("GET", "/api/rooms/%d/messages" % room_id)["latest_seq"]

    for i in range(10):
        owner.ok(
            "POST",
            "/api/rooms/%d/messages" % room_id,
            {"content": "离线消息 %d" % i, "msg_type": "TEXT", "client_msg_id": rand_name("c")},
        )

    # 从中途游标增量拉取：只应拿到 (since, latest] 区间。
    since = start + 4
    bf = owner.ok("GET", "/api/rooms/%d/messages?since=%d" % (room_id, since))
    expect_eq(bf["latest_seq"], start + 10, "最新序号")
    expect_eq(len(bf["messages"]), 6, "增量区间应为 (since, latest]")
    expect(all(m["seq"] > since for m in bf["messages"]), "增量结果不应包含已读消息")
    expect(not bf["truncated"], "小步增量不应触发全量降级")

    # 游标已是最新时应返回空数组（不是 null）。
    bf = owner.ok("GET", "/api/rooms/%d/messages?since=%d" % (room_id, start + 10))
    expect_eq(bf["messages"], [], "无新消息时应返回空数组")

    # 上报已读水位后，未读计数应清零。
    owner.ok("POST", "/api/rooms/%d/read" % room_id, {"seq": start + 10})
    unread = owner.ok("GET", "/api/rooms/unread")
    mine = [u for u in unread if u["room_id"] == room_id]
    if mine:
        expect_eq(mine[0]["unread"], 0, "已读上报后的未读数")

    # 另一个成员应当看到未读。
    other = new_user()
    other.ok("POST", "/api/rooms/%d/join" % room_id, {"seat_gender": "ANY"})
    owner.ok(
        "POST",
        "/api/rooms/%d/messages" % room_id,
        {"content": "给新成员的消息", "msg_type": "TEXT", "client_msg_id": rand_name("c")},
    )
    unread = other.ok("GET", "/api/rooms/unread")
    mine = [u for u in unread if u["room_id"] == room_id]
    expect(len(mine) == 1 and mine[0]["unread"] >= 1, "新成员应看到未读消息")


@test("状态机审计日志可追溯")
def test_state_log():
    owner = new_user()
    card = make_room(owner, any_seats=3, min_viable=2)
    room_id = card["room"]["id"]

    u = new_user()
    u.ok("POST", "/api/rooms/%d/join" % room_id, {"seat_gender": "ANY"})
    u.ok("POST", "/api/rooms/%d/leave" % room_id)

    logs = owner.ok("GET", "/api/rooms/%d/history" % room_id)
    expect(len(logs) >= 2, "至少应记录开车与成员流转")
    events = [l["event"] for l in logs]
    expect("PUBLISH" in events, "应记录开车（PUBLISH）事件")
    for l in logs:
        expect(l["scope"] in ("room", "member"), "审计范围取值应受控，实际 %r" % l["scope"])
        expect(l["to_status"], "审计记录必须有目标状态")


@test("拼车墙筛选与分页")
def test_wall_filters():
    owner = new_user()
    card = make_room(owner, any_seats=5, min_viable=2, title="筛选测试专用车")
    room_id = card["room"]["id"]
    script = card["room"]["script_name"]

    anon = Api()
    # 用剧本名（随机且唯一）精确命中，而不是去翻墙的第一页。
    # 墙按 start_at 升序分页，测试库里房间越积越多时「新车一定在第一页」
    # 根本不成立——那是分页的正常行为，不是缺陷。
    wall = anon.ok("GET", "/api/rooms?q=%s&limit=100" % requests.utils.quote(script))
    expect(any(i["room"]["id"] == room_id for i in wall["items"]),
           "新车应能被关键词检索到")
    expect(isinstance(wall["items"], list), "items 必须是数组")

    # 顺带验证关键词筛选真的收窄了结果，而不是被忽略后返回全表。
    # 匹配字段与 repository 的实现保持一致：剧本名 / 店名 / 标题。
    kw = script.lower()
    expect(all(
        kw in (i["room"]["script_name"] + i["room"]["venue_name"] + i["room"]["title"]).lower()
        for i in wall["items"]
    ), "关键词筛选应生效")

    wall = anon.ok("GET", "/api/rooms?city=%s&limit=100" % requests.utils.quote("测试城"))
    expect(all(i["room"]["city"] == "测试城" for i in wall["items"]), "城市筛选应生效")

    wall = anon.ok("GET", "/api/rooms?room_type=ESCAPE&limit=100")
    expect(all(i["room"]["room_type"] == "ESCAPE" for i in wall["items"]), "类型筛选应生效")

    _, code, _ = anon.call("GET", "/api/rooms?room_type=KTV")
    expect_eq(code, "VALIDATION_FAILED", "非法类型筛选的错误码")

    # 未登录看墙时不应带出任何「我在车上」的标记。
    for i in wall["items"]:
        expect(not i["am_on_car"], "匿名访客不应被标记为在车上")

    # 分页边界
    page = anon.ok("GET", "/api/rooms?limit=1&offset=0")
    expect(len(page["items"]) <= 1, "limit=1 应最多返回 1 条")
    expect(page["total"] >= 1, "total 应反映总数而非当页数")


@test("错误响应信封与错误码契约")
def test_error_envelope():
    anon = Api()
    r = anon.raw("GET", "/api/rooms/99999999")
    env = r.json()
    expect(env["ok"] is False, "失败响应的 ok 必须为 false")
    expect_eq(env["error"]["code"], "ROOM_NOT_FOUND", "不存在房间的错误码")
    expect(env["error"]["message"], "错误必须带面向用户的中文文案")
    expect(isinstance(env["error"]["details"], dict), "details 必须是对象，不能是 null")

    r = anon.raw("GET", "/api/rooms/abc")
    expect_eq(r.json()["error"]["code"], "BAD_REQUEST", "非法路径参数的错误码")

    r = anon.raw("GET", "/api/不存在的路径")
    expect_eq(r.status_code, 404, "未知路由的状态码")
    expect_eq(r.json()["error"]["code"], "NOT_FOUND", "未知路由的错误码")


@test("时区：所有对外时间均为东八区")
def test_timezone():
    owner = new_user()
    card = make_room(owner, any_seats=4, min_viable=2, hours=5)
    room = card["room"]
    for field in ("start_at", "created_at", "updated_at"):
        val = room[field]
        expect(
            val.endswith("+08:00") or val.endswith("+0800"),
            "%s 应带东八区偏移，实际 %s" % (field, val),
        )
    expect(
        card["snapshot"]["start_at"].endswith("+08:00"),
        "快照 start_at 应带东八区偏移，实际 %s" % card["snapshot"]["start_at"],
    )
    # 5 小时后开局，倒计时应在合理区间内。
    left = card["snapshot"]["seconds_left"]
    expect(4 * 3600 < left <= 5 * 3600 + 120, "倒计时秒数应约为 5 小时，实际 %d" % left)


@test("全量对账：所有房间席位账目零漂移")
def test_global_audit():
    api = new_user()
    wall = api.ok("GET", "/api/rooms?limit=100&status=RECRUITING,LOCKED,CONFIRMED,IN_PROGRESS")
    checked = 0
    for item in wall["items"]:
        rid = item["room"]["id"]
        a = api.ok("GET", "/api/rooms/%d/audit" % rid)
        expect_eq(a["drift"], 0, "房间 %d 的席位账目漂移" % rid)
        checked += 1
    expect(checked > 0, "应至少对账一个房间")
    print("      （已对账 %d 个房间）" % checked)


# ==================================================================== 主流程


def main():
    print("=" * 68)
    print("野生拼车墙 API 冒烟测试")
    print("目标: %s" % BASE)
    print("并发抢位并发度: %d" % RACERS)
    print("预期花费: ¥0（本项目零外部计费依赖）")
    print("=" * 68)

    # 等服务就绪，避免 compose 刚起来就跑测试造成假失败。
    for _ in range(30):
        try:
            if requests.get(BASE + "/readyz", timeout=3).status_code == 200:
                break
        except Exception:
            pass
        time.sleep(2)
    else:
        print("[FAIL] 服务在 60 秒内没有就绪，放弃")
        return 1

    passed, failed = 0, []
    for name, fn in _TESTS:
        if _ONLY and _ONLY not in name:
            continue
        t0 = time.time()
        try:
            fn()
            print("[PASS] %-44s %5.0fms" % (name, (time.time() - t0) * 1000))
            passed += 1
        except Failure as e:
            print("[FAIL] %-44s %5.0fms" % (name, (time.time() - t0) * 1000))
            print("       断言失败: %s" % e)
            failed.append((name, str(e)))
        except Exception as e:  # noqa: BLE001
            print("[FAIL] %-44s %5.0fms" % (name, (time.time() - t0) * 1000))
            print("       异常: %s" % e)
            traceback.print_exc(limit=4)
            failed.append((name, repr(e)))

    print("=" * 68)
    print("通过 %d，失败 %d" % (passed, len(failed)))
    if failed:
        print("\n失败清单:")
        for name, msg in failed:
            print("  - %s\n      %s" % (name, msg))
        return 1
    print("全部通过。本轮花费 ¥0。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
