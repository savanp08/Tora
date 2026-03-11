package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/savanp08/converse/internal/database"
)

const (
	GlobalR2UsageBytesKey = "global_r2_usage_bytes"
	R2HardCapBytes        = int64(8_500_000_000)
)

var ErrR2StorageFull = errors.New("r2 storage hard cap reached")

func EnsureR2WriteAllowed(ctx context.Context, redisStore *database.RedisStore, hardCapBytes int64) error {
	overCap, _, err := IsR2WriteBlocked(ctx, redisStore, hardCapBytes)
	if err != nil {
		return err
	}
	if overCap {
		return ErrR2StorageFull
	}
	return nil
}

func IsR2WriteBlocked(
	ctx context.Context,
	redisStore *database.RedisStore,
	hardCapBytes int64,
) (blocked bool, usageBytes int64, err error) {
	if redisStore == nil || redisStore.Client == nil {
		return false, 0, fmt.Errorf("redis client is not configured")
	}
	if hardCapBytes <= 0 {
		hardCapBytes = R2HardCapBytes
	}
	if ctx == nil {
		ctx = context.Background()
	}

	usageBytes, err = redisStore.Client.Get(ctx, GlobalR2UsageBytesKey).Int64()
	if errors.Is(err, redis.Nil) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("load r2 usage bytes: %w", err)
	}
	return usageBytes >= hardCapBytes, usageBytes, nil
}

func IncrementR2UsageBytes(ctx context.Context, redisStore *database.RedisStore, delta int64) (int64, error) {
	if delta <= 0 {
		return 0, nil
	}
	if redisStore == nil || redisStore.Client == nil {
		return 0, fmt.Errorf("redis client is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	nextValue, err := redisStore.Client.IncrBy(ctx, GlobalR2UsageBytesKey, delta).Result()
	if err != nil {
		return 0, fmt.Errorf("increment r2 usage bytes: %w", err)
	}
	return nextValue, nil
}

func DecrementR2UsageBytes(ctx context.Context, redisStore *database.RedisStore, delta int64) (int64, error) {
	if delta <= 0 {
		return 0, nil
	}
	if redisStore == nil || redisStore.Client == nil {
		return 0, fmt.Errorf("redis client is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	const script = `
local key = KEYS[1]
local delta = tonumber(ARGV[1]) or 0
local current = tonumber(redis.call("GET", key) or "0") or 0
local next = current - delta
if next < 0 then
	next = 0
end
redis.call("SET", key, tostring(next))
return next
`

	result, err := redisStore.Client.Eval(ctx, script, []string{GlobalR2UsageBytesKey}, delta).Result()
	if err != nil {
		return 0, fmt.Errorf("decrement r2 usage bytes: %w", err)
	}
	switch typed := result.(type) {
	case int64:
		return typed, nil
	case float64:
		return int64(typed), nil
	case string:
		var parsed int64
		_, parseErr := fmt.Sscan(typed, &parsed)
		if parseErr != nil {
			return 0, fmt.Errorf("parse r2 usage bytes result: %w", parseErr)
		}
		return parsed, nil
	default:
		return 0, nil
	}
}
