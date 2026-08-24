package ws

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/alkaid/jubensha-carpool/backend/internal/logger"
	"github.com/alkaid/jubensha-carpool/backend/internal/timeutil"
)

// presenceTTL 是在线状态的过期时长。心跳周期的三倍左右，
// 容忍一次丢包不至于让用户「闪断」。
const presenceTTL = 95 * time.Second

// Presence 用 Redis ZSET 维护每个房间的在线用户。
//
// 为什么不用 Hub 的内存 map：内存只知道**本节点**的连接。多副本部署时，
// 用户 A 连在副本 1、用户 B 连在副本 2，两人应该互相看到彼此在线。
// ZSET 的 score 存最后心跳时间，读取时按时间窗过滤，因此进程被 kill -9
// 来不及清理也不会留下永久的「幽灵在线」。
type Presence struct {
	rdb *redis.Client
}

func NewPresence(rdb *redis.Client) *Presence { return &Presence{rdb: rdb} }

func presenceKey(roomID int64) string { return fmt.Sprintf("presence:room:%d", roomID) }

// Touch 记录/刷新用户在该房间的在线心跳。
func (p *Presence) Touch(ctx context.Context, roomID, userID int64) {
	if p.rdb == nil {
		return
	}
	key := presenceKey(roomID)
	pipe := p.rdb.Pipeline()
	pipe.ZAdd(ctx, key, redis.Z{
		Score:  float64(timeutil.Now().Unix()),
		Member: strconv.FormatInt(userID, 10),
	})
	// 整个 key 也设 TTL：房间彻底没人后自动消失，不留垃圾键。
	pipe.Expire(ctx, key, presenceTTL*3)
	if _, err := pipe.Exec(ctx); err != nil {
		logger.C(ctx).Warn("刷新在线状态失败", "room_id", roomID, "user_id", userID, "error", err)
	}
}

// Leave 移除在线标记。
func (p *Presence) Leave(ctx context.Context, roomID, userID int64) {
	if p.rdb == nil {
		return
	}
	if err := p.rdb.ZRem(ctx, presenceKey(roomID), strconv.FormatInt(userID, 10)).Err(); err != nil {
		logger.C(ctx).Warn("移除在线状态失败", "room_id", roomID, "user_id", userID, "error", err)
	}
}

// List 返回该房间当前在线的用户 ID。返回值永不为 nil。
func (p *Presence) List(ctx context.Context, roomID int64) []int64 {
	out := []int64{}
	if p.rdb == nil {
		return out
	}
	key := presenceKey(roomID)
	cutoff := timeutil.Now().Add(-presenceTTL).Unix()

	// 顺手清理过期成员：把「读」和「垃圾回收」合并，省掉一个后台任务。
	if err := p.rdb.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(cutoff, 10)).Err(); err != nil {
		logger.C(ctx).Debug("清理过期在线状态失败", "room_id", roomID, "error", err)
	}

	members, err := p.rdb.ZRangeByScore(ctx, key, &redis.ZRangeBy{
		Min: strconv.FormatInt(cutoff, 10),
		Max: "+inf",
	}).Result()
	if err != nil {
		logger.C(ctx).Warn("读取在线状态失败", "room_id", roomID, "error", err)
		return out
	}
	for _, m := range members {
		if id, err := strconv.ParseInt(m, 10, 64); err == nil {
			out = append(out, id)
		}
	}
	return out
}
