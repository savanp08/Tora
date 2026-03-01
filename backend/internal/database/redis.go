package database

import (
	"context"
	"fmt"
	"log"

	"github.com/redis/go-redis/v9"
)

var Ctx = context.Background()

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
