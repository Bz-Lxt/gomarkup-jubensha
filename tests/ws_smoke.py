#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
野生拼车墙 —— WebSocket Hub 行为测试。

覆盖 Requirements TR-3 与 C 系列验收基线：
  - 房间隔离：A 房的消息绝不出现在 B 房
  - 鉴权：非成员不得进入房间频道
  - 离线补齐：断线期间的消息在重连后被完整取回
  - 全量降级：落后过多时置 truncated，而不是一次性回传上万条
  - 席位广播：上车/退车实时推给房内所有人
  - 在线状态：presence 跨连接可见

同样不依赖 pytest，且预期花费 ¥0。
"""

import json
import os
import sys
import time
import traceback
from typing import Any, Dict, List, Optional

import requests
import websocket  # websocket-client

BASE = os.environ.get("API_BASE", "http://localhost:31810").rstrip("/")
WS = os.environ.get("WS_BASE", BASE.replace("http://", "ws://").replace("https://", "wss://")).rstrip("/")
TIMEOUT = 15

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from api_smoke import Api, Failure, expect, expect_eq, make_room, new_user, rand_name  # noqa: E402

_TESTS = []


def test(name):
    def deco(fn):
        _TESTS.append((name, fn))
        return fn

    return deco


class Conn(object):
    """一条测试用 WebSocket 连接，带按类型收帧的辅助方法。"""

    def __init__(self, path, token=None, timeout=8, origin=None):
        # type: (str, Optional[str], int, Optional[str]) -> None
        url = WS + path
        if token:
            url += ("&" if "?" in url else "?") + "access_token=" + token
        # suppress_origin：websocket-client 默认会自动补一个 Origin 头，而服务端
        # 按 CORS 白名单校验 Origin 以阻断跨站 WebSocket 劫持（CSWSH），于是
        # 测试会被合法地 403 掉。测试脚本本就是非浏览器客户端，不该伪造 Origin；
        # 把它去掉才是如实模拟。想验证校验本身是否生效时显式传 origin。
        kw = {"timeout": timeout}
        if origin is None:
            kw["suppress_origin"] = True
        else:
            kw["origin"] = origin
        self.ws = websocket.create_connection(url, **kw)
        self.buf = []  # type: List[Dict[str, Any]]

    def send(self, typ, data=None):
        self.ws.send(json.dumps({"type": typ, "data": data or {}}))

    def _pump(self):
        """读一帧到缓冲。超时返回 False。"""
        try:
            raw = self.ws.recv()
        except Exception:
            return False
        if not raw:
            return False
        try:
            self.buf.append(json.loads(raw))
        except ValueError:
            return False
        return True

    def wait(self, typ, timeout=6.0):
        # type: (str, float) -> Optional[Dict[str, Any]]
        """等待指定类型的帧。已缓冲的优先。"""
        for i, f in enumerate(self.buf):
            if f.get("type") == typ:
                return self.buf.pop(i)
        deadline = time.time() + timeout
        while time.time() < deadline:
            self.ws.settimeout(max(0.2, deadline - time.time()))
            if not self._pump():
                continue
            f = self.buf[-1]
            if f.get("type") == typ:
                return self.buf.pop()
        return None

    def collect(self, seconds=1.2):
        # type: (float) -> List[Dict[str, Any]]
        """在给定时间窗内尽量收帧，用于断言「某类帧没有出现」。"""
        deadline = time.time() + seconds
        while time.time() < deadline:
            self.ws.settimeout(max(0.1, deadline - time.time()))
            if not self._pump():
                break
        out = self.buf[:]
        self.buf = []
        return out

    def close(self):
        try:
            self.ws.close()
        except Exception:
            pass


def setup_room(capacity=6):
    """建一辆车并返回 (车主会话, 房间 ID)。"""
    owner = new_user()
    card = make_room(owner, any_seats=capacity, min_viable=2)
    return owner, card["room"]["id"]


# ==================================================================== 用例


@test("握手：hello 首帧携带房间与游标信息")
def test_hello():
    owner, room_id = setup_room()
    c = Conn("/ws/rooms/%d" % room_id, owner.token)
    try:
        hello = c.wait("hello")
        expect(hello is not None, "连接建立后应收到 hello 首帧")
        d = hello["data"]
        expect_eq(d["room_id"], room_id, "hello 中的房间号")
        expect_eq(d["user_id"], owner.user["id"], "hello 中的用户号")
        expect(d["latest_seq"] >= 1, "hello 应给出当前最新序号")
        expect(d["server_time"].endswith("+08:00") or "+08" in d["server_time"],
               "服务端时间应为东八区，实际 %s" % d["server_time"])
    finally:
        c.close()


@test("鉴权：非成员与无令牌都不能进房间频道")
def test_ws_authz():
    owner, room_id = setup_room()

    # 无令牌：握手阶段就该被 401 掉，create_connection 会抛异常。
    no_token_rejected = False
    try:
        c = Conn("/ws/rooms/%d" % room_id, None)
        c.close()
    except Exception:
        no_token_rejected = True
    expect(no_token_rejected, "无令牌应无法建立房间连接")

    # 有令牌但不是成员
    outsider = new_user()
    rejected = False
    try:
        c = Conn("/ws/rooms/%d" % room_id, outsider.token)
        # 有些实现会先建连再发 error 帧，两种都算正确拒绝。
        err = c.wait("error", timeout=3)
        if err is not None and err["data"]["code"] == "NOT_ROOM_MEMBER":
            rejected = True
        c.close()
    except Exception:
        rejected = True
    expect(rejected, "非成员应被拒绝进入房间频道")


@test("防跨站劫持：白名单外的 Origin 被拒于握手之外")
def test_ws_origin_guard():
    owner, room_id = setup_room()

    # 冒充第三方站点发起的浏览器连接。令牌合法也必须被拒——否则任何网站
    # 都能借用户浏览器里的凭据静默读取房内聊天。
    evil_rejected = False
    try:
        c = Conn("/ws/rooms/%d" % room_id, owner.token,
                 origin="http://evil.example.com")
        c.close()
    except Exception:
        evil_rejected = True
    expect(evil_rejected, "白名单外的 Origin 应在握手阶段被拒")

    # 对照组：不带 Origin 的非浏览器客户端应当放行，否则移动端与压测无法接入。
    c = Conn("/ws/rooms/%d" % room_id, owner.token)
    try:
        expect(c.wait("hello") is not None, "无 Origin 的客户端应可正常连接")
    finally:
        c.close()


@test("★ 房间隔离：A 房消息绝不泄漏到 B 房")
def test_room_isolation():
    owner_a, room_a = setup_room()
    owner_b, room_b = setup_room()

    ca = Conn("/ws/rooms/%d" % room_a, owner_a.token)
    cb = Conn("/ws/rooms/%d" % room_b, owner_b.token)
    try:
        ca.wait("hello")
        cb.wait("hello")
        ca.collect(0.4)
        cb.collect(0.4)

        marker = "隔离测试-" + rand_name("")
        ca.send("chat.send", {"content": marker, "msg_type": "TEXT", "client_msg_id": rand_name("c")})

        got = ca.wait("chat.message", timeout=6)
        expect(got is not None, "A 房应收到自己发的消息")
        expect_eq(got["data"]["content"], marker, "A 房收到的消息内容")

        # B 房在时间窗内不应看到任何带该标记的消息。
        leaked = [
            f for f in cb.collect(1.5)
            if f.get("type") == "chat.message" and marker in json.dumps(f.get("data", {}), ensure_ascii=False)
        ]
        expect_eq(len(leaked), 0, "泄漏到 B 房的消息条数")
    finally:
        ca.close()
        cb.close()


@test("同房广播：两条连接都能实时收到对方消息")
def test_fanout():
    owner, room_id = setup_room()
    member = new_user()
    member.ok("POST", "/api/rooms/%d/join" % room_id, {"seat_gender": "ANY"})

    c1 = Conn("/ws/rooms/%d" % room_id, owner.token)
    c2 = Conn("/ws/rooms/%d" % room_id, member.token)
    try:
        c1.wait("hello")
        c2.wait("hello")
        c1.collect(0.4)
        c2.collect(0.4)

        marker = "扇出测试-" + rand_name("")
        c2.send("chat.send", {"content": marker, "msg_type": "TEXT", "client_msg_id": rand_name("c")})

        for label, c in (("发送方", c2), ("另一方", c1)):
            got = c.wait("chat.message", timeout=6)
            expect(got is not None, "%s 应收到广播" % label)
            expect_eq(got["data"]["content"], marker, "%s 收到的内容" % label)
    finally:
        c1.close()
        c2.close()


@test("★ 离线补齐：断线期间的消息在重连后完整取回")
def test_offline_backfill():
    owner, room_id = setup_room()
    member = new_user()
    member.ok("POST", "/api/rooms/%d/join" % room_id, {"seat_gender": "ANY"})

    # 第一次连接：记下当前水位后主动断开，模拟用户关掉页面。
    c = Conn("/ws/rooms/%d" % room_id, member.token)
    hello = c.wait("hello")
    expect(hello is not None, "首次连接应收到 hello")
    watermark = hello["data"]["latest_seq"]
    c.close()

    # 离线期间车主发了 7 条消息。
    sent = []
    for i in range(7):
        m = owner.ok(
            "POST",
            "/api/rooms/%d/messages" % room_id,
            {"content": "离线期消息 %d" % i, "msg_type": "TEXT", "client_msg_id": rand_name("c")},
        )
        sent.append(m["seq"])

    # 重连并携带旧游标补齐。
    c2 = Conn("/ws/rooms/%d" % room_id, member.token)
    try:
        c2.wait("hello")
        c2.send("chat.pull", {"last_seen_seq": watermark})
        bf = c2.wait("chat.backfill", timeout=8)
        expect(bf is not None, "重连补齐应返回 chat.backfill 帧")
        d = bf["data"]

        got_seqs = [m["seq"] for m in d["messages"]]
        expect(isinstance(d["messages"], list), "messages 必须是数组")
        for s in sent:
            expect(s in got_seqs, "离线期间的消息 seq=%d 应被补齐取回" % s)
        expect(all(s > watermark for s in got_seqs), "补齐结果不应包含断线前已读的消息")
        expect_eq(got_seqs, sorted(got_seqs), "补齐结果必须按序号升序")
        expect_eq(d["latest_seq"], sent[-1], "补齐结果中的最新序号")
        expect(not d["truncated"], "7 条消息不应触发全量降级")
    finally:
        c2.close()


@test("全量降级：落后过多时置 truncated 而非回传全部")
def test_backfill_truncation():
    owner, room_id = setup_room()

    # BACKFILL_MAX 默认 200。灌 240 条以跨过阈值。
    # 走 HTTP 批量灌，比 WS 逐条发快得多。
    total = 240
    for i in range(total):
        owner.ok(
            "POST",
            "/api/rooms/%d/messages" % room_id,
            {"content": "洪水消息 %d" % i, "msg_type": "TEXT", "client_msg_id": rand_name("c")},
        )

    # 从 0 开始拉，等于「落后了全部历史」。
    bf = owner.ok("GET", "/api/rooms/%d/messages?since=0" % room_id)
    expect(bf["truncated"], "落后 240 条应触发全量降级（truncated=true）")
    expect(
        len(bf["messages"]) <= 200,
        "降级后返回条数应受 BACKFILL_MAX 限制，实际 %d 条" % len(bf["messages"]),
    )
    expect(bf["total_gap"] >= total, "total_gap 应反映真实断层大小，实际 %d" % bf["total_gap"])
    # 降级后必须给的是**最近**的一段，而不是最老的一段 ——
    # 否则用户重连后看到的是几天前的消息，完全没用。
    expect_eq(bf["messages"][-1]["seq"], bf["latest_seq"], "降级后应返回最近一段而非最早一段")


@test("席位广播：上车与退车实时推给房内成员")
def test_slot_broadcast():
    owner, room_id = setup_room(capacity=5)

    c = Conn("/ws/rooms/%d" % room_id, owner.token)
    try:
        c.wait("hello")
        c.collect(0.4)

        joiner = new_user()
        joiner.ok("POST", "/api/rooms/%d/join" % room_id, {"seat_gender": "ANY"})

        slot = c.wait("room.slot", timeout=8)
        expect(slot is not None, "有人上车时房内应收到 room.slot 帧")
        d = slot["data"]
        expect_eq(d["room_id"], room_id, "席位帧中的房间号")
        expect_eq(d["occupied"], 2, "上车后的占用席位数")
        expect(d["headline"], "席位帧应带 headline 文案")
        expect("seats" in d and len(d["seats"]) == 3, "席位帧应带完整席位桶")

        c.collect(0.3)
        joiner.ok("POST", "/api/rooms/%d/leave" % room_id)
        slot = c.wait("room.slot", timeout=8)
        expect(slot is not None, "有人退车时房内应收到 room.slot 帧")
        expect_eq(slot["data"]["occupied"], 1, "退车后的占用席位数")
    finally:
        c.close()


@test("墙频道：席位变动推给所有看墙的人（含未登录）")
def test_wall_channel():
    owner, room_id = setup_room(capacity=5)

    # 墙允许匿名订阅，新访客才能看到实时数字。
    c = Conn("/ws/wall", None)
    try:
        c.collect(0.5)
        joiner = new_user()
        joiner.ok("POST", "/api/rooms/%d/join" % room_id, {"seat_gender": "ANY"})

        deadline = time.time() + 8
        found = None
        while time.time() < deadline and found is None:
            f = c.wait("room.slot", timeout=2)
            if f and f["data"].get("room_id") == room_id:
                found = f
        expect(found is not None, "墙订阅者应收到该房间的席位变动")
        expect_eq(found["data"]["occupied"], 2, "墙上收到的占用席位数")
    finally:
        c.close()


@test("在线状态：presence 跨连接可见")
def test_presence():
    owner, room_id = setup_room()
    member = new_user()
    member.ok("POST", "/api/rooms/%d/join" % room_id, {"seat_gender": "ANY"})

    c1 = Conn("/ws/rooms/%d" % room_id, owner.token)
    c2 = Conn("/ws/rooms/%d" % room_id, member.token)
    try:
        c1.wait("hello")
        c2.wait("hello")
        # 清干净缓冲：c1 建连时服务端会广播一次只含车主的 presence，
        # 若不丢弃，下面的 wait 会拿到这帧过期快照而不是查询结果。
        c1.collect(0.5)
        c1.send("presence.query", {})

        pres = c1.wait("presence", timeout=6)
        expect(pres is not None, "presence.query 应得到 presence 帧")
        users = pres["data"]["users"]
        expect(isinstance(users, list), "presence.users 必须是数组")
        # 两条连接都在，双方都应出现在在线集合里。
        expect(owner.user["id"] in users, "车主应在在线集合中")
        expect(member.user["id"] in users, "成员应在在线集合中")
        expect_eq(pres["data"]["count"], len(users), "count 应与 users 长度一致")
    finally:
        c1.close()
        c2.close()


@test("协议健壮性：坏帧与未知类型不得断开连接")
def test_bad_frames():
    owner, room_id = setup_room()
    c = Conn("/ws/rooms/%d" % room_id, owner.token)
    try:
        c.wait("hello")

        # 非法 JSON
        c.ws.send("这不是 JSON")
        # 未知类型
        c.send("chat.explode", {"x": 1})
        # 载荷类型错误
        c.send("chat.send", {"content": 12345})
        # 缺字段
        c.send("chat.ack", {})

        c.collect(1.0)

        # 连接必须还活着：发一条正常消息应当照常送达。
        marker = "健壮性-" + rand_name("")
        c.send("chat.send", {"content": marker, "msg_type": "TEXT", "client_msg_id": rand_name("c")})
        got = c.wait("chat.message", timeout=6)
        expect(got is not None, "坏帧之后连接应仍可用")
        expect_eq(got["data"]["content"], marker, "坏帧之后的正常消息内容")
    finally:
        c.close()


@test("心跳：ping 得到 pong")
def test_ping():
    owner, room_id = setup_room()
    c = Conn("/ws/rooms/%d" % room_id, owner.token)
    try:
        c.wait("hello")
        c.send("ping", {})
        pong = c.wait("pong", timeout=6)
        expect(pong is not None, "应用层 ping 应得到 pong")
    finally:
        c.close()


@test("聊天限流：超出配额被拒但连接不断")
def test_ws_rate_limit():
    owner, room_id = setup_room()
    c = Conn("/ws/rooms/%d" % room_id, owner.token)
    try:
        c.wait("hello")
        # 快速连发。测试环境把 HTTP 限流放宽了，但 WS 帧级限流是独立的
        # （ws/ratelimit.go），因此这里仍可能触发；无论触发与否，
        # 连接都必须存活 —— 这才是本用例真正要守的性质。
        for i in range(60):
            c.send("chat.send", {
                "content": "限流测试 %d" % i,
                "msg_type": "TEXT",
                "client_msg_id": rand_name("c"),
            })
        frames = c.collect(2.5)
        errs = [f for f in frames if f.get("type") == "error"]
        for e in errs:
            expect(
                e["data"]["code"] in ("RATE_LIMITED", "MESSAGE_EMPTY", "MESSAGE_TOO_LONG"),
                "限流场景下的错误码应受控，实际 %s" % e["data"]["code"],
            )

        # 连接仍应可用。
        c.send("ping", {})
        expect(c.wait("pong", timeout=6) is not None, "限流后连接应仍存活")
    finally:
        c.close()


# ==================================================================== 主流程


def main():
    print("=" * 68)
    print("WebSocket Hub 行为测试")
    print("目标: %s" % WS)
    print("预期花费: ¥0")
    print("=" * 68)

    for _ in range(30):
        try:
            if requests.get(BASE + "/readyz", timeout=3).status_code == 200:
                break
        except Exception:
            pass
        time.sleep(2)
    else:
        print("[FAIL] 服务未就绪")
        return 1

    passed, failed = 0, []
    only = os.environ.get("ONLY", "")
    for name, fn in _TESTS:
        if only and only not in name:
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
