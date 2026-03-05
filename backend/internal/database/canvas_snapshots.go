package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const canvasSnapshotHotTTL = 7 * 24 * time.Hour

func canvasHotSnapshotKey(roomID string) (string, error) {
	normalizedRoomID := strings.TrimSpace(roomID)
	if normalizedRoomID == "" {
		return "", fmt.Errorf("room id is required")
	}
	return "canvas_hot:{" + normalizedRoomID + "}", nil
}

func SaveCanvasSnapshotToRedis(
	ctx context.Context,
	redisClient *redis.Client,
	roomID string,
	snapshot []byte,
) error {
	if redisClient == nil {
		return fmt.Errorf("redis client is not configured")
	}
	key, err := canvasHotSnapshotKey(roomID)
	if err != nil {
		return err
	}
	return redisClient.Set(ctx, key, snapshot, canvasSnapshotHotTTL).Err()
}

func GetCanvasSnapshotFromRedis(
	ctx context.Context,
	redisClient *redis.Client,
	roomID string,
) ([]byte, error) {
	if redisClient == nil {
		return nil, fmt.Errorf("redis client is not configured")
	}
	key, err := canvasHotSnapshotKey(roomID)
	if err != nil {
		return nil, err
	}
	snapshot, err := redisClient.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	return snapshot, nil
}

func DeleteCanvasSnapshotFromRedis(
	ctx context.Context,
	redisClient *redis.Client,
	roomID string,
) error {
	if redisClient == nil {
		return fmt.Errorf("redis client is not configured")
	}
	key, err := canvasHotSnapshotKey(roomID)
	if err != nil {
		return err
	}
	return redisClient.Del(ctx, key).Err()
}
