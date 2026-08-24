-- 0001_init.sql — 剧本杀拼车墙基础表结构
--
-- 本文件承载 Requirements TR-1 的三道「物理屏障」。它们不是性能优化，
-- 而是正确性的最后防线：即使 Redis 锁失效、应用层出 bug，数据库也必须
-- 拒绝写入非法状态。
--
--   屏障 1（超载）  rooms.ck_rooms_no_overload
--   屏障 2（幂等）  room_members.uq_members_active
--   屏障 3（有序）  room_messages.uq_msg_room_seq

-- ---------------------------------------------------------------- users
CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL PRIMARY KEY,
    username      TEXT        NOT NULL,
    phone         TEXT        NOT NULL DEFAULT '',
    password_hash TEXT        NOT NULL,
    nickname      TEXT        NOT NULL DEFAULT '',
    avatar        TEXT        NOT NULL DEFAULT '',
    city          TEXT        NOT NULL DEFAULT '',
    bio           TEXT        NOT NULL DEFAULT '',
    reputation    INT         NOT NULL DEFAULT 100,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_users_reputation CHECK (reputation BETWEEN 0 AND 200)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_users_username ON users (lower(username));
-- 手机号选填，只对非空值去重
CREATE UNIQUE INDEX IF NOT EXISTS uq_users_phone ON users (phone) WHERE phone <> '';

-- ------------------------------------------------------------ user_tags
CREATE TABLE IF NOT EXISTS user_tags (
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    tag        TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, tag),
    CONSTRAINT ck_user_tags_value CHECK (
        tag IN ('HARDCORE', 'EMOTION', 'HORROR', 'FUN', 'ROOKIE', 'VETERAN')
    )
);

-- ---------------------------------------------------------------- rooms
CREATE TABLE IF NOT EXISTS rooms (
    id            BIGSERIAL PRIMARY KEY,
    owner_id      BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title         TEXT        NOT NULL,
    script_name   TEXT        NOT NULL,
    venue_name    TEXT        NOT NULL,
    city          TEXT        NOT NULL,
    address       TEXT        NOT NULL DEFAULT '',
    room_type     TEXT        NOT NULL,
    difficulty    INT         NOT NULL DEFAULT 3,
    theme         TEXT        NOT NULL DEFAULT '',
    notes         TEXT        NOT NULL DEFAULT '',

    start_at      TIMESTAMPTZ NOT NULL,
    capacity      INT         NOT NULL,
    min_viable    INT         NOT NULL,

    joined_count  INT         NOT NULL DEFAULT 0,
    pending_count INT         NOT NULL DEFAULT 0,

    male_seats    INT         NOT NULL DEFAULT 0,
    female_seats  INT         NOT NULL DEFAULT 0,
    any_seats     INT         NOT NULL DEFAULT 0,
    male_taken    INT         NOT NULL DEFAULT 0,
    female_taken  INT         NOT NULL DEFAULT 0,
    any_taken     INT         NOT NULL DEFAULT 0,

    status        TEXT        NOT NULL DEFAULT 'RECRUITING',
    msg_seq       BIGINT      NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT ck_rooms_type CHECK (room_type IN ('SCRIPT', 'ESCAPE')),
    CONSTRAINT ck_rooms_status CHECK (
        status IN ('DRAFT', 'RECRUITING', 'LOCKED', 'CONFIRMED',
                   'IN_PROGRESS', 'COMPLETED', 'EXPIRED', 'CANCELLED')
    ),
    CONSTRAINT ck_rooms_difficulty CHECK (difficulty BETWEEN 1 AND 5),
    CONSTRAINT ck_rooms_capacity CHECK (capacity BETWEEN 2 AND 12),
    CONSTRAINT ck_rooms_min_viable CHECK (min_viable BETWEEN 1 AND capacity),
    CONSTRAINT ck_rooms_counts_nonneg CHECK (joined_count >= 0 AND pending_count >= 0),

    -- ★ 屏障 1：超载物理屏障。任何绕过应用层锁的写入都会在这里被拒绝。
    CONSTRAINT ck_rooms_no_overload CHECK (joined_count + pending_count <= capacity),

    -- 席位配额必须精确瓜分总人数，不允许「多出来的位子」
    CONSTRAINT ck_rooms_seat_plan CHECK (male_seats + female_seats + any_seats = capacity),
    CONSTRAINT ck_rooms_seat_bounds CHECK (
        male_taken   BETWEEN 0 AND male_seats   AND
        female_taken BETWEEN 0 AND female_seats AND
        any_taken    BETWEEN 0 AND any_seats
    ),
    -- 账目一致性：分桶占用之和必须精确等于聚合占用（防 NFR-1 A-5 的账目漂移）
    CONSTRAINT ck_rooms_seat_sum CHECK (
        male_taken + female_taken + any_taken = joined_count + pending_count
    )
);

CREATE INDEX IF NOT EXISTS idx_rooms_wall  ON rooms (city, status, start_at);
CREATE INDEX IF NOT EXISTS idx_rooms_owner ON rooms (owner_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rooms_sched ON rooms (status, start_at);

-- --------------------------------------------------------- room_members
CREATE TABLE IF NOT EXISTS room_members (
    id              BIGSERIAL PRIMARY KEY,
    room_id         BIGINT      NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    user_id         BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    seat_gender     TEXT        NOT NULL,
    status          TEXT        NOT NULL,
    is_owner        BOOLEAN     NOT NULL DEFAULT false,
    hold_expires_at TIMESTAMPTZ,
    joined_at       TIMESTAMPTZ,
    left_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT ck_members_seat CHECK (seat_gender IN ('MALE', 'FEMALE', 'ANY')),
    CONSTRAINT ck_members_status CHECK (
        status IN ('PENDING', 'JOINED', 'RELEASED', 'LEFT', 'KICKED', 'CHECKED_IN')
    ),
    -- PENDING 必须有 TTL，否则占位永不回收
    CONSTRAINT ck_members_hold CHECK (status <> 'PENDING' OR hold_expires_at IS NOT NULL)
);

-- ★ 屏障 2：幂等物理屏障。同一用户在同一房间最多一条「占席位」记录。
--   退车后可再次上车（历史记录以 LEFT/RELEASED/KICKED 形态保留）。
CREATE UNIQUE INDEX IF NOT EXISTS uq_members_active
    ON room_members (room_id, user_id)
    WHERE status IN ('PENDING', 'JOINED', 'CHECKED_IN');

CREATE INDEX IF NOT EXISTS idx_members_room ON room_members (room_id, status);
CREATE INDEX IF NOT EXISTS idx_members_user ON room_members (user_id, status);
-- 占位回收扫描专用：只索引 PENDING，索引体积小
CREATE INDEX IF NOT EXISTS idx_members_reap
    ON room_members (hold_expires_at)
    WHERE status = 'PENDING';

-- -------------------------------------------------------- room_messages
CREATE TABLE IF NOT EXISTS room_messages (
    id            BIGSERIAL PRIMARY KEY,
    room_id       BIGINT      NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    seq           BIGINT      NOT NULL,
    sender_id     BIGINT               REFERENCES users (id) ON DELETE SET NULL,
    msg_type      TEXT        NOT NULL,
    content       TEXT        NOT NULL,
    tag_code      TEXT        NOT NULL DEFAULT '',
    client_msg_id TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT ck_msg_type CHECK (msg_type IN ('TEXT', 'TAG', 'SYSTEM')),
    CONSTRAINT ck_msg_len CHECK (char_length(content) BETWEEN 1 AND 500),
    CONSTRAINT ck_msg_sender CHECK (msg_type = 'SYSTEM' OR sender_id IS NOT NULL),
    CONSTRAINT ck_msg_seq CHECK (seq > 0)
);

-- ★ 屏障 3：房内有序物理屏障。seq 不重不跳的前提。
CREATE UNIQUE INDEX IF NOT EXISTS uq_msg_room_seq ON room_messages (room_id, seq);
CREATE INDEX IF NOT EXISTS idx_msg_room_seq_desc ON room_messages (room_id, seq DESC);
-- 客户端重发幂等：同一房同一发送者的同一 client_msg_id 只落一条
CREATE UNIQUE INDEX IF NOT EXISTS uq_msg_client
    ON room_messages (room_id, sender_id, client_msg_id)
    WHERE client_msg_id <> '';

-- ------------------------------------------------------ room_state_logs
CREATE TABLE IF NOT EXISTS room_state_logs (
    id          BIGSERIAL PRIMARY KEY,
    room_id     BIGINT      NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    member_id   BIGINT,
    scope       TEXT        NOT NULL,
    from_status TEXT        NOT NULL,
    to_status   TEXT        NOT NULL,
    event       TEXT        NOT NULL,
    actor_id    BIGINT,
    reason      TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ck_statelog_scope CHECK (scope IN ('room', 'member'))
);

CREATE INDEX IF NOT EXISTS idx_statelog_room ON room_state_logs (room_id, created_at DESC);

-- ------------------------------------------------------ message_cursors
CREATE TABLE IF NOT EXISTS message_cursors (
    room_id       BIGINT      NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    user_id       BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    last_seen_seq BIGINT      NOT NULL DEFAULT 0,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, user_id),
    CONSTRAINT ck_cursor_seq CHECK (last_seen_seq >= 0)
);
