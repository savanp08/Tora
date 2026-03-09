package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

var windowCounterScript = redis.NewScript(`
local maxAllowed = tonumber(ARGV[1]) or 0
if maxAllowed <= 0 then
  return {1, 0}
end

local current = tonumber(redis.call("GET", KEYS[1]) or "0")
if current >= maxAllowed then
  return {0, current}
end

local updated = redis.call("INCR", KEYS[1])
local ttlSeconds = tonumber(ARGV[2]) or 1
if updated == 1 then
  redis.call("EXPIRE", KEYS[1], ttlSeconds)
end

return {1, updated}
`)

type windowCounterEntry struct {
	Count     int64
	ExpiresAt time.Time
}

var windowCounterMemoryState struct {
	mu      sync.Mutex
	entries map[string]windowCounterEntry
}

type WindowLimitResult struct {
	Allowed bool
	Current int64
}

func AllowFixedWindow(
	ctx context.Context,
	namespace string,
	scope string,
	windowName string,
	identifier string,
	maxAllowed int64,
	window time.Duration,
) (WindowLimitResult, error) {
	if maxAllowed <= 0 {
		return WindowLimitResult{Allowed: true}, nil
	}
	if window <= 0 {
		window = time.Hour
	}
	if ctx == nil {
		ctx = context.Background()
	}

	key := windowCounterKey(namespace, scope, windowName, identifier)
	ttlSeconds := int64(window / time.Second)
	if ttlSeconds <= 0 {
		ttlSeconds = 1
	}

	redisClientMu.RLock()
	client := redisClient
	redisClientMu.RUnlock()
	if client != nil {
		result, err := windowCounterScript.Run(ctx, client, []string{key}, maxAllowed, ttlSeconds).Result()
		if err == nil {
			allowed, current := parseWindowCounterScriptResult(result)
			return WindowLimitResult{
				Allowed: allowed,
				Current: current,
			}, nil
		}
	}

	return allowFixedWindowInMemory(key, maxAllowed, window), nil
}

func allowFixedWindowInMemory(key string, maxAllowed int64, window time.Duration) WindowLimitResult {
	now := time.Now().UTC()
	windowCounterMemoryState.mu.Lock()
	defer windowCounterMemoryState.mu.Unlock()

	if windowCounterMemoryState.entries == nil {
		windowCounterMemoryState.entries = make(map[string]windowCounterEntry)
	}
	for existingKey, entry := range windowCounterMemoryState.entries {
		if now.After(entry.ExpiresAt) {
			delete(windowCounterMemoryState.entries, existingKey)
		}
	}

	entry, exists := windowCounterMemoryState.entries[key]
	if exists && now.Before(entry.ExpiresAt) {
		if entry.Count >= maxAllowed {
			return WindowLimitResult{
				Allowed: false,
				Current: entry.Count,
			}
		}
		entry.Count++
		windowCounterMemoryState.entries[key] = entry
		return WindowLimitResult{
			Allowed: true,
			Current: entry.Count,
		}
	}

	windowCounterMemoryState.entries[key] = windowCounterEntry{
		Count:     1,
		ExpiresAt: now.Add(window),
	}
	return WindowLimitResult{
		Allowed: true,
		Current: 1,
	}
}

func windowCounterKey(namespace string, scope string, windowName string, identifier string) string {
	normalizedNamespace := normalizeWindowComponent(namespace, "global")
	normalizedScope := normalizeWindowComponent(scope, "scope")
	normalizedWindow := normalizeWindowComponent(windowName, "window")
	normalizedIdentifier := strings.TrimSpace(identifier)
	if normalizedIdentifier == "" {
		normalizedIdentifier = "unknown"
	}
	return fmt.Sprintf(
		"limits:%s:%s:%s:%s",
		normalizedNamespace,
		normalizedScope,
		normalizedWindow,
		hashWindowCounterIdentifier(normalizedIdentifier),
	)
}

func normalizeWindowComponent(value string, fallback string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return fallback
	}
	var builder strings.Builder
	for _, ch := range trimmed {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-' {
			builder.WriteRune(ch)
		}
	}
	sanitized := strings.TrimSpace(builder.String())
	if sanitized == "" {
		return fallback
	}
	return sanitized
}

func hashWindowCounterIdentifier(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	encoded := hex.EncodeToString(sum[:])
	if len(encoded) > 24 {
		return encoded[:24]
	}
	return encoded
}

func parseWindowCounterScriptResult(result any) (bool, int64) {
	values, ok := result.([]any)
	if !ok || len(values) < 2 {
		return true, 0
	}
	decision := toInt64Window(values[0])
	current := toInt64Window(values[1])
	return decision == 1, current
}

func toInt64Window(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int8:
		return int64(typed)
	case int16:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case uint:
		return int64(typed)
	case uint8:
		return int64(typed)
	case uint16:
		return int64(typed)
	case uint32:
		return int64(typed)
	case uint64:
		if typed > uint64(^uint64(0)>>1) {
			return int64(^uint64(0) >> 1)
		}
		return int64(typed)
	case float32:
		return int64(typed)
	case float64:
		return int64(typed)
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err == nil {
			return parsed
		}
	}
	return 0
}
