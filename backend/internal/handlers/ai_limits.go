package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/savanp08/converse/internal/monitor"
)

const (
	aiLimitScopeUser      = "user"
	aiLimitScopeRoom      = "room"
	aiLimitScopeIP        = "ip"
	aiLimitScopeDeviceID  = "device_id"
	aiLimitDefaultWindow  = 24 * time.Hour
	aiLimitReloadInterval = 15 * time.Second
)

var (
	defaultPrivateAILimits = privateAILimitValues{
		WindowSeconds: 86400,
		PerUser:       2,
		PerRoom:       10,
		PerIP:         5,
		PerDeviceID:   5,
	}

	privateAILimitsState struct {
		mu             sync.Mutex
		lastLoadedAt   time.Time
		lastLoadedPath string
		values         privateAILimitValues
	}

	privateAILimitsParsePatterns = map[string]*regexp.Regexp{
		"windowSeconds": regexp.MustCompile(`(?m)\bwindowSeconds\b\s*:\s*(\d+)`),
		"perUser":       regexp.MustCompile(`(?m)\bperUser\b\s*:\s*(\d+)`),
		"perRoom":       regexp.MustCompile(`(?m)\bperRoom\b\s*:\s*(\d+)`),
		"perIP":         regexp.MustCompile(`(?m)\bper(?:IP|Ip)\b\s*:\s*(\d+)`),
		"perDeviceId":   regexp.MustCompile(`(?m)\bperDevice(?:Id|ID)\b\s*:\s*(\d+)`),
	}

	privateAILimitsScript = redis.NewScript(`
local keyCount = #KEYS
local windowSeconds = tonumber(ARGV[keyCount + 1]) or 86400

for i = 1, keyCount do
  local maxAllowed = tonumber(ARGV[i]) or 0
  if maxAllowed > 0 then
    local current = tonumber(redis.call("GET", KEYS[i]) or "0")
    if current >= maxAllowed then
      return {0, i, current}
    end
  end
end

for i = 1, keyCount do
  local maxAllowed = tonumber(ARGV[i]) or 0
  if maxAllowed > 0 then
    local updated = redis.call("INCR", KEYS[i])
    if updated == 1 then
      redis.call("EXPIRE", KEYS[i], windowSeconds)
    end
  end
end

return {1, 0, 0}
`)

	privateAILimitsMemoryState struct {
		mu      sync.Mutex
		entries map[string]privateAILimitMemoryEntry
	}
)

type privateAILimitValues struct {
	WindowSeconds int64
	PerUser       int64
	PerRoom       int64
	PerIP         int64
	PerDeviceID   int64
}

type privateAILimitDimension struct {
	Scope string
	Value string
	Limit int64
}

type privateAILimitMemoryEntry struct {
	Count     int64
	ExpiresAt time.Time
}

type privateAILimitExceededError struct {
	Scope string
	Limit int64
}

func (e *privateAILimitExceededError) Error() string {
	if e == nil {
		return "AI request limit reached"
	}
	return fmt.Sprintf("AI request limit reached for %s (%d)", strings.TrimSpace(e.Scope), e.Limit)
}

func (e *privateAILimitExceededError) PublicMessage() string {
	if e == nil {
		return "AI request limit reached. Please try again later."
	}
	switch strings.TrimSpace(e.Scope) {
	case aiLimitScopeUser:
		return "AI request limit reached for this user. Please try again later."
	case aiLimitScopeRoom:
		return "AI request limit reached for this room. Please try again later."
	case aiLimitScopeIP:
		return "AI request limit reached for this IP. Please try again later."
	case aiLimitScopeDeviceID:
		return "AI request limit reached for this device. Please try again later."
	default:
		return "AI request limit reached. Please try again later."
	}
}

func enforcePrivateAIRequestLimits(
	ctx context.Context,
	userID string,
	roomID string,
	ipAddress string,
	deviceID string,
) error {
	limits := loadPrivateAILimits()
	dimensions := buildPrivateAILimitDimensions(userID, roomID, ipAddress, deviceID, limits)
	if len(dimensions) == 0 {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}

	redisStore, _ := activePrivateAIChatStores()
	if redisStore != nil && redisStore.Client != nil {
		if err := enforcePrivateAIRequestLimitsViaRedis(ctx, redisStore.Client, limits.WindowSeconds, dimensions); err == nil {
			return nil
		}
	}
	return enforcePrivateAIRequestLimitsInMemory(limits.WindowDuration(), dimensions)
}

func buildPrivateAILimitDimensions(
	userID string,
	roomID string,
	ipAddress string,
	deviceID string,
	limits privateAILimitValues,
) []privateAILimitDimension {
	dimensions := make([]privateAILimitDimension, 0, 4)

	normalizedUserID := normalizeIdentifier(userID)
	if normalizedUserID == "" {
		normalizedUserID = "unknown_user"
	}
	if limits.PerUser > 0 {
		dimensions = append(dimensions, privateAILimitDimension{
			Scope: aiLimitScopeUser,
			Value: normalizedUserID,
			Limit: limits.PerUser,
		})
	}

	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		normalizedRoomID = "unknown_room"
	}
	if limits.PerRoom > 0 {
		dimensions = append(dimensions, privateAILimitDimension{
			Scope: aiLimitScopeRoom,
			Value: normalizedRoomID,
			Limit: limits.PerRoom,
		})
	}

	normalizedIP := strings.TrimSpace(ipAddress)
	if normalizedIP == "" {
		normalizedIP = "unknown_ip"
	}
	if limits.PerIP > 0 {
		dimensions = append(dimensions, privateAILimitDimension{
			Scope: aiLimitScopeIP,
			Value: normalizedIP,
			Limit: limits.PerIP,
		})
	}

	normalizedDeviceID := strings.ToLower(strings.TrimSpace(deviceID))
	if normalizedDeviceID == "" {
		normalizedDeviceID = "unknown_device"
	}
	if limits.PerDeviceID > 0 {
		dimensions = append(dimensions, privateAILimitDimension{
			Scope: aiLimitScopeDeviceID,
			Value: normalizedDeviceID,
			Limit: limits.PerDeviceID,
		})
	}

	return dimensions
}

func enforcePrivateAIRequestLimitsViaRedis(
	ctx context.Context,
	client *redis.Client,
	windowSeconds int64,
	dimensions []privateAILimitDimension,
) error {
	if client == nil || len(dimensions) == 0 {
		return nil
	}

	keys := make([]string, 0, len(dimensions))
	args := make([]any, 0, len(dimensions)+1)
	for _, dimension := range dimensions {
		keys = append(keys, privateAIRedisLimitKey(dimension.Scope, dimension.Value))
		args = append(args, dimension.Limit)
	}
	args = append(args, windowSeconds)

	result, err := privateAILimitsScript.Run(ctx, client, keys, args...).Result()
	if err != nil {
		return err
	}

	decision, blockedIndex := parsePrivateAILimitScriptResult(result)
	if decision == 1 {
		recordPrivateAILimitAllowed(dimensions)
		return nil
	}

	failedDimension := dimensionByScriptIndex(dimensions, blockedIndex)
	recordPrivateAILimitBlocked(failedDimension.Scope)
	return &privateAILimitExceededError{
		Scope: failedDimension.Scope,
		Limit: failedDimension.Limit,
	}
}

func parsePrivateAILimitScriptResult(result any) (int64, int64) {
	values, ok := result.([]any)
	if !ok || len(values) < 2 {
		return 1, 0
	}
	return toInt64(values[0]), toInt64(values[1])
}

func toInt64(value any) int64 {
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

func dimensionByScriptIndex(dimensions []privateAILimitDimension, scriptIndex int64) privateAILimitDimension {
	if len(dimensions) == 0 {
		return privateAILimitDimension{Scope: aiLimitScopeUser, Limit: defaultPrivateAILimits.PerUser}
	}

	resolvedIndex := int(scriptIndex - 1)
	if resolvedIndex < 0 || resolvedIndex >= len(dimensions) {
		return dimensions[0]
	}
	return dimensions[resolvedIndex]
}

func enforcePrivateAIRequestLimitsInMemory(
	window time.Duration,
	dimensions []privateAILimitDimension,
) error {
	if len(dimensions) == 0 {
		return nil
	}
	if window <= 0 {
		window = aiLimitDefaultWindow
	}

	now := time.Now().UTC()
	privateAILimitsMemoryState.mu.Lock()
	defer privateAILimitsMemoryState.mu.Unlock()

	if privateAILimitsMemoryState.entries == nil {
		privateAILimitsMemoryState.entries = make(map[string]privateAILimitMemoryEntry)
	}

	for key, entry := range privateAILimitsMemoryState.entries {
		if now.After(entry.ExpiresAt) {
			delete(privateAILimitsMemoryState.entries, key)
		}
	}

	for _, dimension := range dimensions {
		key := privateAIMemoryLimitKey(dimension.Scope, dimension.Value)
		entry, exists := privateAILimitsMemoryState.entries[key]
		if !exists || now.After(entry.ExpiresAt) {
			continue
		}
		if entry.Count >= dimension.Limit {
			recordPrivateAILimitBlocked(dimension.Scope)
			return &privateAILimitExceededError{
				Scope: dimension.Scope,
				Limit: dimension.Limit,
			}
		}
	}

	expiresAt := now.Add(window)
	for _, dimension := range dimensions {
		key := privateAIMemoryLimitKey(dimension.Scope, dimension.Value)
		entry, exists := privateAILimitsMemoryState.entries[key]
		if !exists || now.After(entry.ExpiresAt) {
			entry = privateAILimitMemoryEntry{
				Count:     1,
				ExpiresAt: expiresAt,
			}
			privateAILimitsMemoryState.entries[key] = entry
			continue
		}
		entry.Count++
		privateAILimitsMemoryState.entries[key] = entry
	}

	recordPrivateAILimitAllowed(dimensions)
	return nil
}

func privateAIRedisLimitKey(scope string, value string) string {
	return fmt.Sprintf("limits:ai:%s:%s", strings.TrimSpace(scope), hashPrivateAILimitValue(value))
}

func privateAIMemoryLimitKey(scope string, value string) string {
	return fmt.Sprintf("%s:%s", strings.TrimSpace(scope), hashPrivateAILimitValue(value))
}

func hashPrivateAILimitValue(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	encoded := hex.EncodeToString(sum[:])
	if len(encoded) > 24 {
		return encoded[:24]
	}
	return encoded
}

func recordPrivateAILimitAllowed(dimensions []privateAILimitDimension) {
	for _, dimension := range dimensions {
		monitor.AILimitChecksTotal.WithLabelValues(dimension.Scope, "allowed").Inc()
	}
}

func recordPrivateAILimitBlocked(scope string) {
	normalizedScope := strings.TrimSpace(scope)
	if normalizedScope == "" {
		normalizedScope = aiLimitScopeUser
	}
	monitor.AILimitChecksTotal.WithLabelValues(normalizedScope, "blocked").Inc()
	monitor.SecurityBlocksTotal.WithLabelValues("ai_" + normalizedScope + "_limit").Inc()
}

func loadPrivateAILimits() privateAILimitValues {
	privateAILimitsState.mu.Lock()
	defer privateAILimitsState.mu.Unlock()

	now := time.Now().UTC()
	if !privateAILimitsState.lastLoadedAt.IsZero() &&
		now.Sub(privateAILimitsState.lastLoadedAt) < aiLimitReloadInterval {
		if privateAILimitsState.values.WindowSeconds <= 0 {
			return defaultPrivateAILimits
		}
		return privateAILimitsState.values
	}

	path := resolvePrivateAILimitsFilePath()
	loaded := readPrivateAILimitsFromFile(path)
	privateAILimitsState.values = loaded
	privateAILimitsState.lastLoadedPath = path
	privateAILimitsState.lastLoadedAt = now
	return loaded
}

func resolvePrivateAILimitsFilePath() string {
	candidates := make([]string, 0, 8)
	if configured := strings.TrimSpace(os.Getenv("AI_LIMITS_FILE")); configured != "" {
		candidates = append(candidates, configured)
	}
	candidates = append(candidates,
		"limits.ts",
		filepath.Join("backend", "limits.ts"),
		filepath.Join("..", "limits.ts"),
		filepath.Join("..", "backend", "limits.ts"),
		filepath.Join("..", "..", "limits.ts"),
		filepath.Join("..", "..", "backend", "limits.ts"),
	)

	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		return candidate
	}
	return ""
}

func readPrivateAILimitsFromFile(path string) privateAILimitValues {
	limits := defaultPrivateAILimits
	if strings.TrimSpace(path) == "" {
		return limits
	}

	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("[private-ai] limits file read failed path=%s err=%v; using defaults", path, err)
		return limits
	}
	content := string(data)

	limits.WindowSeconds = parsePrivateAILimitValue(content, "windowSeconds", limits.WindowSeconds)
	limits.PerUser = parsePrivateAILimitValue(content, "perUser", limits.PerUser)
	limits.PerRoom = parsePrivateAILimitValue(content, "perRoom", limits.PerRoom)
	limits.PerIP = parsePrivateAILimitValue(content, "perIP", limits.PerIP)
	limits.PerDeviceID = parsePrivateAILimitValue(content, "perDeviceId", limits.PerDeviceID)

	return limits.normalized()
}

func parsePrivateAILimitValue(content string, field string, fallback int64) int64 {
	pattern, ok := privateAILimitsParsePatterns[field]
	if !ok || pattern == nil {
		return fallback
	}
	matches := pattern.FindStringSubmatch(content)
	if len(matches) < 2 {
		return fallback
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(matches[1]), 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func (v privateAILimitValues) normalized() privateAILimitValues {
	if v.WindowSeconds <= 0 {
		v.WindowSeconds = defaultPrivateAILimits.WindowSeconds
	}
	if v.PerUser < 0 {
		v.PerUser = 0
	}
	if v.PerRoom < 0 {
		v.PerRoom = 0
	}
	if v.PerIP < 0 {
		v.PerIP = 0
	}
	if v.PerDeviceID < 0 {
		v.PerDeviceID = 0
	}
	return v
}

func (v privateAILimitValues) WindowDuration() time.Duration {
	if v.WindowSeconds <= 0 {
		return aiLimitDefaultWindow
	}
	return time.Duration(v.WindowSeconds) * time.Second
}
