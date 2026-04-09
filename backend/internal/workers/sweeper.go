package workers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gocql/gocql"
	"github.com/minio/minio-go/v7"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/storage"
)

const (
	r2OrphanSweepInterval    = 48 * time.Hour
	r2OrphanSweepTimeout     = 30 * time.Minute
	r2OrphanDeleteBatchSize  = 1000
	r2OrphanScyllaQueryLimit = 1
)

func StartR2OrphanSweeper(
	ctx context.Context,
	redisStore *database.RedisStore,
	scyllaStore *database.ScyllaStore,
	r2Client *storage.R2Client,
) {
	if redisStore == nil || redisStore.Client == nil || scyllaStore == nil || scyllaStore.Session == nil {
		return
	}
	if r2Client == nil || r2Client.Client == nil || strings.TrimSpace(r2Client.Bucket) == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	runSweep := func() {
		sweepCtx, cancel := context.WithTimeout(ctx, r2OrphanSweepTimeout)
		defer cancel()
		if err := sweepR2OrphansOnce(sweepCtx, redisStore, scyllaStore, r2Client); err != nil {
			log.Printf("[r2-sweeper] run failed err=%v", err)
		}
	}

	runSweep()

	ticker := time.NewTicker(r2OrphanSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runSweep()
		}
	}
}

func sweepR2OrphansOnce(
	ctx context.Context,
	redisStore *database.RedisStore,
	scyllaStore *database.ScyllaStore,
	r2Client *storage.R2Client,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if redisStore == nil || redisStore.Client == nil {
		return fmt.Errorf("redis store is not configured")
	}
	if scyllaStore == nil || scyllaStore.Session == nil {
		return fmt.Errorf("scylla store is not configured")
	}
	if r2Client == nil || r2Client.Client == nil || strings.TrimSpace(r2Client.Bucket) == "" {
		return fmt.Errorf("r2 client is not configured")
	}

	startedAt := time.Now()
	roomsTable := scyllaStore.Table("rooms")
	roomActiveCache := make(map[string]bool)
	batchKeys := make([]string, 0, r2OrphanDeleteBatchSize)
	var batchBytes int64
	var scannedCount int64
	var skippedCount int64
	var orphanCount int64
	var deletedCount int64

	flushBatch := func() error {
		if len(batchKeys) == 0 {
			return nil
		}
		keys := append([]string(nil), batchKeys...)
		bytesToDecrement := batchBytes

		batchKeys = batchKeys[:0]
		batchBytes = 0

		if err := r2Client.DeleteObjects(ctx, keys); err != nil {
			return err
		}
		deletedCount += int64(len(keys))
		if bytesToDecrement > 0 {
			if _, usageErr := storage.DecrementR2UsageBytes(ctx, redisStore, bytesToDecrement); usageErr != nil {
				log.Printf(
					"[r2-sweeper] failed to decrement r2 usage bytes deleted_keys=%d deleted_bytes=%d err=%v",
					len(keys),
					bytesToDecrement,
					usageErr,
				)
			}
		}
		return nil
	}

	objects := r2Client.Client.ListObjects(ctx, r2Client.Bucket, minio.ListObjectsOptions{Recursive: true})
	for objectInfo := range objects {
		if objectInfo.Err != nil {
			return fmt.Errorf("list r2 objects: %w", objectInfo.Err)
		}
		scannedCount++

		roomID := storage.ExtractRoomIDFromObjectKey(objectInfo.Key)
		if roomID == "" {
			skippedCount++
			continue
		}

		isActive, cached := roomActiveCache[roomID]
		if !cached {
			roomFound, lookupErr := sweeperRoomIsActive(ctx, scyllaStore, roomsTable, roomID, startedAt)
			if lookupErr != nil {
				return fmt.Errorf("lookup room %s: %w", roomID, lookupErr)
			}
			isActive = roomFound
			roomActiveCache[roomID] = roomFound
		}
		if isActive {
			continue
		}

		orphanCount++
		batchKeys = append(batchKeys, objectInfo.Key)
		if objectInfo.Size > 0 {
			batchBytes += objectInfo.Size
		}
		if len(batchKeys) >= r2OrphanDeleteBatchSize {
			if err := flushBatch(); err != nil {
				return fmt.Errorf("delete orphan batch: %w", err)
			}
		}
	}

	if err := flushBatch(); err != nil {
		return fmt.Errorf("delete final orphan batch: %w", err)
	}

	log.Printf(
		"[r2-sweeper] completed scanned=%d skipped=%d orphaned=%d deleted=%d checked_rooms=%d elapsed_ms=%d",
		scannedCount,
		skippedCount,
		orphanCount,
		deletedCount,
		len(roomActiveCache),
		time.Since(startedAt).Milliseconds(),
	)
	return nil
}

func sweeperRoomIsActive(
	ctx context.Context,
	scyllaStore *database.ScyllaStore,
	roomsTable string,
	roomID string,
	now time.Time,
) (bool, error) {
	if scyllaStore == nil || scyllaStore.Session == nil {
		return false, fmt.Errorf("scylla store is not configured")
	}
	normalizedRoomID := strings.TrimSpace(roomID)
	if normalizedRoomID == "" {
		return false, nil
	}

	candidates := make([]string, 0, 2)
	seen := map[string]struct{}{}
	addCandidate := func(candidate string) {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" {
			return
		}
		if _, exists := seen[trimmed]; exists {
			return
		}
		seen[trimmed] = struct{}{}
		candidates = append(candidates, trimmed)
	}

	addCandidate(normalizedRoomID)
	addCandidate(normalizeSweeperRoomID(normalizedRoomID))

	for _, candidate := range candidates {
		var foundRoomID string
		var expiresAt *time.Time
		err := scyllaStore.Session.Query(
			fmt.Sprintf(`SELECT room_id, expires_at FROM %s WHERE room_id = ? LIMIT %d`, roomsTable, r2OrphanScyllaQueryLimit),
			candidate,
		).WithContext(ctx).Scan(&foundRoomID, &expiresAt)
		if errors.Is(err, gocql.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		if strings.TrimSpace(foundRoomID) != "" {
			if expiresAt != nil && !expiresAt.IsZero() && !expiresAt.UTC().After(now.UTC()) {
				return false, nil
			}
			return true, nil
		}
	}
	return false, nil
}

func normalizeSweeperRoomID(raw string) string {
	candidate := strings.ToLower(strings.TrimSpace(raw))
	if candidate == "" {
		return ""
	}
	var builder strings.Builder
	for _, ch := range candidate {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			builder.WriteRune(ch)
		}
	}
	return builder.String()
}
