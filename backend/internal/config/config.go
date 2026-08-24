// Package config 从环境变量加载配置，并在启动时做一次强校验。
//
// 对齐 NFR-4：JWT 密钥只从环境注入，代码内零硬编码；生产环境弱密钥直接拒绝启动，
// 而不是打个 warning 就放过去。
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 是应用的全部可调参数。
type Config struct {
	AppEnv   string
	HTTPAddr string
	LogLevel string

	DatabaseURL  string
	DBMaxOpen    int
	DBMaxIdle    int
	DBConnMaxIdl time.Duration

	RedisAddr     string
	RedisPassword string
	RedisDB       int

	JWTSecret     string
	JWTAccessTTL  time.Duration
	JWTRefreshTTL time.Duration

	CORSOrigins []string

	// 抢位
	SlotHoldTTL      time.Duration // 占位（PENDING）存活时长
	SlotLockTTL      time.Duration // Redis 分布式锁 TTL
	SlotLockRetries  int           // L2 获取失败的重试次数
	SlotRedisLockOn  bool          // 关闭后仅靠 DB 悲观锁（NFR-1 A-4 验收开关）
	SlotAutoConfirm  bool          // 上车后是否自动 PENDING -> JOINED
	SlotTxTimeout    time.Duration // 单次抢位事务预算，超时打 WARN
	SchedulerEnabled bool
	SchedulerEvery   time.Duration

	// 聊天 / WS
	BackfillMax    int // 增量转全量降级的阈值，同时也是全量返回条数上限
	MessageMaxLen  int
	WSSendBuffer   int
	WSPingInterval time.Duration
	WSPongWait     time.Duration
	WSWriteWait    time.Duration

	// 限流（每分钟）
	RateLoginPerMin int
	RateJoinPerMin  int
	RateChatPerMin  int

	SeedOnBoot bool
}

const weakSecret = "dev-only-change-me-please-32bytes-min"

// Load 读取环境变量并校验。任何致命配置问题都返回 error，让进程快速失败。
func Load() (*Config, error) {
	c := &Config{
		AppEnv:   env("APP_ENV", "development"),
		HTTPAddr: env("HTTP_ADDR", ":8080"),
		LogLevel: env("LOG_LEVEL", "info"),

		DatabaseURL:  env("DATABASE_URL", ""),
		DBMaxOpen:    envInt("DB_MAX_OPEN", 40),
		DBMaxIdle:    envInt("DB_MAX_IDLE", 10),
		DBConnMaxIdl: envDur("DB_CONN_MAX_IDLE", 5*time.Minute),

		RedisAddr:     env("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPassword: env("REDIS_PASSWORD", ""),
		RedisDB:       envInt("REDIS_DB", 0),

		JWTSecret:     env("JWT_SECRET", ""),
		JWTAccessTTL:  envDur("JWT_ACCESS_TTL", 2*time.Hour),
		JWTRefreshTTL: envDur("JWT_REFRESH_TTL", 720*time.Hour),

		CORSOrigins: envList("CORS_ORIGINS", []string{"http://localhost:5173"}),

		SlotHoldTTL:      envDur("SLOT_HOLD_TTL", 15*time.Minute),
		SlotLockTTL:      envDur("SLOT_LOCK_TTL", 3*time.Second),
		SlotLockRetries:  envInt("SLOT_LOCK_RETRIES", 3),
		SlotRedisLockOn:  envBool("SLOT_REDIS_LOCK_ENABLED", true),
		SlotAutoConfirm:  envBool("SLOT_AUTO_CONFIRM", true),
		SlotTxTimeout:    envDur("SLOT_TX_TIMEOUT", 500*time.Millisecond),
		SchedulerEnabled: envBool("SCHEDULER_ENABLED", true),
		SchedulerEvery:   envDur("SCHEDULER_INTERVAL", 15*time.Second),

		BackfillMax:    envInt("BACKFILL_MAX", 200),
		MessageMaxLen:  envInt("MESSAGE_MAX_LEN", 500),
		WSSendBuffer:   envInt("WS_SEND_BUFFER", 256),
		WSPingInterval: envDur("WS_PING_INTERVAL", 30*time.Second),
		WSPongWait:     envDur("WS_PONG_WAIT", 60*time.Second),
		WSWriteWait:    envDur("WS_WRITE_WAIT", 10*time.Second),

		RateLoginPerMin: envInt("RATE_LOGIN_PER_MIN", 5),
		RateJoinPerMin:  envInt("RATE_JOIN_PER_MIN", 10),
		RateChatPerMin:  envInt("RATE_CHAT_PER_MIN", 20),

		SeedOnBoot: envBool("SEED_ON_BOOT", true),
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) IsProd() bool { return strings.EqualFold(c.AppEnv, "production") }

func (c *Config) validate() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("config: DATABASE_URL 必须设置")
	}
	if c.RedisAddr == "" {
		return fmt.Errorf("config: REDIS_ADDR 必须设置")
	}
	if len(c.JWTSecret) < 32 {
		return fmt.Errorf("config: JWT_SECRET 长度必须 >= 32 字节，当前 %d", len(c.JWTSecret))
	}
	if c.IsProd() && c.JWTSecret == weakSecret {
		return fmt.Errorf("config: 生产环境禁止使用开发默认 JWT_SECRET")
	}
	if c.SlotHoldTTL <= 0 {
		return fmt.Errorf("config: SLOT_HOLD_TTL 必须为正")
	}
	if c.SlotLockTTL <= 0 {
		return fmt.Errorf("config: SLOT_LOCK_TTL 必须为正")
	}
	if c.BackfillMax <= 0 || c.BackfillMax > 2000 {
		return fmt.Errorf("config: BACKFILL_MAX 必须在 1..2000 之间，当前 %d", c.BackfillMax)
	}
	if c.WSSendBuffer <= 0 {
		return fmt.Errorf("config: WS_SEND_BUFFER 必须为正")
	}
	if c.WSPongWait <= c.WSPingInterval {
		return fmt.Errorf("config: WS_PONG_WAIT(%s) 必须大于 WS_PING_INTERVAL(%s)", c.WSPongWait, c.WSPingInterval)
	}
	if c.DBMaxIdle > c.DBMaxOpen {
		return fmt.Errorf("config: DB_MAX_IDLE 不能大于 DB_MAX_OPEN")
	}
	return nil
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(k string, def bool) bool {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func envDur(k string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func envList(k string, def []string) []string {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}
