package database

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var Ctx = context.Background()

const (
	roomSummaryDefaultTTL = 6 * time.Hour
	roomSummaryRoomPrefix = "room:"
)

type RedisStore struct {
	Client *redis.Client
}

func InitRedis(addr, password string) *redis.Client {
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           0,
		PoolSize:     400, // Absolutely force a massive pool of 400 connections
		MinIdleConns: 50,  // Keep 50 connections warm and ready at all times
	})

	if _, err := rdb.Ping(Ctx).Result(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	if err := enableKeyspaceExpiryEvents(Ctx, rdb); err != nil {
		log.Printf("⚠️  Redis keyspace notification setup failed: %v", err)
	}

	log.Println("✅ Connected to Redis")
	return rdb
}

func NewRedisStore(addr, password string) (*RedisStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           0,
		PoolSize:     400, // Absolutely force a massive pool of 400 connections
		MinIdleConns: 50,  // Keep 50 connections warm and ready at all times
	})

	if _, err := client.Ping(Ctx).Result(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}
	if err := enableKeyspaceExpiryEvents(Ctx, client); err != nil {
		log.Printf("⚠️  Redis keyspace notification setup failed: %v", err)
	}

	return &RedisStore{Client: client}, nil
}

func enableKeyspaceExpiryEvents(ctx context.Context, client *redis.Client) error {
	if client == nil {
		return fmt.Errorf("redis client is not configured")
	}
	return client.ConfigSet(ctx, "notify-keyspace-events", "Ex").Err()
}

func (r *RedisStore) Publish(ctx context.Context, channel string, payload []byte) error {
	return r.Client.Publish(ctx, channel, payload).Err()
}

func (r *RedisStore) Ping(ctx context.Context) error {
	if r == nil || r.Client == nil {
		return fmt.Errorf("redis client is not configured")
	}
	return r.Client.Ping(ctx).Err()
}

func (r *RedisStore) Close() error {
	return r.Client.Close()
}

// IncrTaskNumber atomically increments the task counter for a room and returns
// the new value. This is used to assign short, human-readable task numbers.
func (r *RedisStore) IncrTaskNumber(ctx context.Context, roomID string) (int, error) {
	if r == nil || r.Client == nil {
		return 0, fmt.Errorf("redis client is not configured")
	}
	n, err := r.Client.Incr(ctx, fmt.Sprintf("task_counter:%s", roomID)).Result()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

func (r *RedisStore) SetRoomSummary(ctx context.Context, roomID string, summary string) error {
	if r == nil || r.Client == nil {
		return fmt.Errorf("redis client is not configured")
	}
	normalizedRoomID := strings.TrimSpace(roomID)
	if normalizedRoomID == "" {
		return fmt.Errorf("room id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	ttl := roomSummaryDefaultTTL
	if roomTTL, err := r.Client.TTL(ctx, roomSummaryRoomPrefix+normalizedRoomID).Result(); err == nil && roomTTL > 0 {
		ttl = roomTTL
	}

	key := fmt.Sprintf("room:{%s}:summary", normalizedRoomID)
	return r.Client.Set(ctx, key, strings.TrimSpace(summary), ttl).Err()
}

func (r *RedisStore) GetRoomSummary(ctx context.Context, roomID string) (string, error) {
	if r == nil || r.Client == nil {
		return "", fmt.Errorf("redis client is not configured")
	}
	normalizedRoomID := strings.TrimSpace(roomID)
	if normalizedRoomID == "" {
		return "", nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	key := fmt.Sprintf("room:{%s}:summary", normalizedRoomID)
	summary, err := r.Client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(summary), nil
}
