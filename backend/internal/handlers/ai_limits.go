package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/savanp08/converse/internal/config"
	"github.com/savanp08/converse/internal/monitor"
	"github.com/savanp08/converse/internal/netutil"
)

const (
	aiLimitScopeUser     = "user"
	aiLimitScopeRoom     = "room"
	aiLimitScopeIP       = "ip"
	aiLimitScopeDeviceID = "device_id"

	aiLimitWindowHour  = "hour"
	aiLimitWindowDay   = "day"
	aiLimitWindowWeek  = "week"
	aiLimitWindowMonth = "month"
)

var (
	privateAILimitsScript = redis.NewScript(`
local keyCount = #KEYS

for i = 1, keyCount do
  local maxArgIndex = ((i - 1) * 2) + 1
  local maxAllowed = tonumber(ARGV[maxArgIndex]) or 0
  if maxAllowed > 0 then
    local current = tonumber(redis.call("GET", KEYS[i]) or "0")
    if current >= maxAllowed then
      local ttl = tonumber(redis.call("TTL", KEYS[i]) or "0")
      return {0, i, current, ttl}
    end
  end
end

for i = 1, keyCount do
  local maxArgIndex = ((i - 1) * 2) + 1
  local ttlArgIndex = maxArgIndex + 1
  local maxAllowed = tonumber(ARGV[maxArgIndex]) or 0
  if maxAllowed > 0 then
    local updated = redis.call("INCR", KEYS[i])
    local ttlSeconds = tonumber(ARGV[ttlArgIndex]) or 1
    if updated == 1 then
      redis.call("EXPIRE", KEYS[i], ttlSeconds)
    end
  end
end

return {1, 0, 0, 0}
`)

	privateAILimitsMemoryState struct {
		mu      sync.Mutex
		entries map[string]privateAILimitMemoryEntry
	}
)

type privateAILimitMemoryEntry struct {
	Count     int64
	ExpiresAt time.Time
}

type privateAILimitCheck struct {
	Scope     string
	Window    string
	Value     string
	Limit     int64
	Duration  time.Duration
	MetricTag string
}

type privateAILimitExceededError struct {
	Scope      string
	Window     string
	Limit      int64
	Current    int64
	Identifier string
	ResetAt    time.Time
	ResetIn    time.Duration
}

func (e *privateAILimitExceededError) Error() string {
	if e == nil {
		return "AI request limit reached"
	}
	return fmt.Sprintf(
		"AI request limit reached for %s (%s=%d)",
		strings.TrimSpace(e.Scope),
		strings.TrimSpace(e.Window),
		e.Limit,
	)
}

func (e *privateAILimitExceededError) PublicMessage() string {
	if e == nil {
		return "AI request limit reached. Please try again later."
	}

	scopeLabel := privateAILimitScopeLabel(e.Scope)
	windowLabel := privateAILimitWindowLabel(e.Window)
	current := e.Current
	if current < e.Limit {
		current = e.Limit
	}

	usageText := ""
	if e.Limit > 0 {
		usageText = fmt.Sprintf(" (%d/%d requests)", current, e.Limit)
	}

	return fmt.Sprintf(
		"AI request limit reached for %s in the %s window%s. %s",
		scopeLabel,
		windowLabel,
		usageText,
		formatPrivateAILimitResetMessage(e.ResetAt, e.ResetIn),
	)
}

func privateAILimitScopeLabel(scope string) string {
	switch strings.TrimSpace(scope) {
	case aiLimitScopeUser:
		return "this user"
	case aiLimitScopeRoom:
		return "this room"
	case aiLimitScopeIP:
		return "this IP address"
	case aiLimitScopeDeviceID:
		return "this device"
	default:
		return "this context"
	}
}

func privateAILimitWindowLabel(window string) string {
	switch strings.TrimSpace(window) {
	case aiLimitWindowHour:
		return "hourly"
	case aiLimitWindowDay:
		return "daily"
	case aiLimitWindowWeek:
		return "weekly"
	case aiLimitWindowMonth:
		return "monthly"
	default:
		return "current"
	}
}

func formatPrivateAILimitResetMessage(resetAt time.Time, resetIn time.Duration) string {
	resolvedResetAt := resetAt.UTC()
	if resetIn <= 0 && !resolvedResetAt.IsZero() {
		resetIn = time.Until(resolvedResetAt)
	}
	if resetIn < 0 {
		resetIn = 0
	}
	if resolvedResetAt.IsZero() && resetIn > 0 {
		resolvedResetAt = time.Now().UTC().Add(resetIn)
	}
	if resolvedResetAt.IsZero() {
		return "Reset timing unavailable."
	}
	return fmt.Sprintf(
		"Resets in %s at %s.",
		formatPrivateAILimitDuration(resetIn),
		resolvedResetAt.Format(time.RFC3339),
	)
}

func formatPrivateAILimitDuration(value time.Duration) string {
	if value <= 0 {
		return "0s"
	}
	value = value.Round(time.Second)
	days := value / (24 * time.Hour)
	value %= 24 * time.Hour
	hours := value / time.Hour
	value %= time.Hour
	minutes := value / time.Minute
	value %= time.Minute
	seconds := value / time.Second

	parts := make([]string, 0, 2)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 && len(parts) < 2 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 && len(parts) < 2 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}
	return strings.Join(parts, "")
}

func enforcePrivateAIRequestLimits(
	ctx context.Context,
	userID string,
	roomID string,
	ipAddress string,
	deviceID string,
) error {
	checks := buildPrivateAILimitChecks(userID, roomID, ipAddress, deviceID)
	if len(checks) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	redisStore, _ := activePrivateAIChatStores()
	if redisStore != nil && redisStore.Client != nil {
		if err := enforcePrivateAILimitsViaRedis(ctx, redisStore.Client, checks); err == nil {
			return nil
		}
	}
	return enforcePrivateAILimitsInMemory(checks)
}

func buildPrivateAILimitChecks(
	userID string,
	roomID string,
	ipAddress string,
	deviceID string,
) []privateAILimitCheck {
	loaded := config.LoadAppLimits().AI

	normalizedUserID := normalizeIdentifier(userID)
	normalizedRoomID := normalizeRoomID(roomID)
	normalizedIP := normalizePrivateAILimitIP(ipAddress)
	normalizedDeviceID := normalizeDeviceIdentifier(deviceID)

	checks := make([]privateAILimitCheck, 0, 16)
	if normalizedUserID != "" && normalizedUserID != "guest" {
		checks = append(checks, expandPrivateAILimitChecks(aiLimitScopeUser, normalizedUserID, loaded.UserRequestLimits)...)
	}
	if normalizedRoomID != "" {
		checks = append(checks, expandPrivateAILimitChecks(aiLimitScopeRoom, normalizedRoomID, loaded.RoomRequestLimits)...)
	}

	// IP buckets are only applied for unauthenticated/guest traffic.
	if normalizedIP != "" && (normalizedUserID == "" || normalizedUserID == "guest") {
		checks = append(checks, expandPrivateAILimitChecks(aiLimitScopeIP, normalizedIP, loaded.IPRequestLimits)...)
	}
	if normalizedDeviceID != "" {
		checks = append(checks, expandPrivateAILimitChecks(aiLimitScopeDeviceID, normalizedDeviceID, loaded.DeviceRequestLimits)...)
	}
	return checks
}

func expandPrivateAILimitChecks(scope string, value string, limits config.TimeWindowLimit) []privateAILimitCheck {
	checks := make([]privateAILimitCheck, 0, 4)
	appendIfValid := func(window string, limit int64, duration time.Duration) {
		if limit <= 0 || duration <= 0 {
			return
		}
		checks = append(checks, privateAILimitCheck{
			Scope:     scope,
			Window:    window,
			Value:     value,
			Limit:     limit,
			Duration:  duration,
			MetricTag: scope + "_" + window,
		})
	}

	appendIfValid(aiLimitWindowHour, limits.PerHour, time.Hour)
	appendIfValid(aiLimitWindowDay, limits.PerDay, 24*time.Hour)
	appendIfValid(aiLimitWindowWeek, limits.PerWeek, 7*24*time.Hour)
	appendIfValid(aiLimitWindowMonth, limits.PerMonth, 30*24*time.Hour)
	return checks
}

func enforcePrivateAILimitsViaRedis(
	ctx context.Context,
	client *redis.Client,
	checks []privateAILimitCheck,
) error {
	if client == nil || len(checks) == 0 {
		return nil
	}

	keys := make([]string, 0, len(checks))
	args := make([]any, 0, len(checks)*2)
	for _, check := range checks {
		keys = append(keys, privateAIRedisLimitKey(check.Scope, check.Window, check.Value))
		args = append(args, check.Limit)
		args = append(args, int64(check.Duration/time.Second))
	}

	result, err := privateAILimitsScript.Run(ctx, client, keys, args...).Result()
	if err != nil {
		return err
	}

	allowed, blockedIndex, blockedCount, blockedTTLSeconds := parsePrivateAILimitScriptResult(result)
	if allowed {
		recordPrivateAILimitAllowed(checks)
		return nil
	}

	blockedCheck := privateAILimitCheckByScriptIndex(checks, blockedIndex)
	recordPrivateAILimitBlocked(blockedCheck.MetricTag)
	resetIn := time.Duration(blockedTTLSeconds) * time.Second
	if resetIn <= 0 {
		resetIn = blockedCheck.Duration
	}
	if resetIn < 0 {
		resetIn = 0
	}
	if blockedCount <= 0 {
		blockedCount = blockedCheck.Limit
	}
	return &privateAILimitExceededError{
		Scope:      blockedCheck.Scope,
		Window:     blockedCheck.Window,
		Limit:      blockedCheck.Limit,
		Current:    blockedCount,
		Identifier: blockedCheck.Value,
		ResetIn:    resetIn,
		ResetAt:    time.Now().UTC().Add(resetIn),
	}
}

func enforcePrivateAILimitsInMemory(checks []privateAILimitCheck) error {
	if len(checks) == 0 {
		return nil
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

	for _, check := range checks {
		key := privateAIMemoryLimitKey(check.Scope, check.Window, check.Value)
		entry, exists := privateAILimitsMemoryState.entries[key]
		if !exists || now.After(entry.ExpiresAt) {
			continue
		}
		if entry.Count >= check.Limit {
			recordPrivateAILimitBlocked(check.MetricTag)
			resetIn := time.Until(entry.ExpiresAt)
			if resetIn < 0 {
				resetIn = 0
			}
			return &privateAILimitExceededError{
				Scope:      check.Scope,
				Window:     check.Window,
				Limit:      check.Limit,
				Current:    entry.Count,
				Identifier: check.Value,
				ResetIn:    resetIn,
				ResetAt:    entry.ExpiresAt.UTC(),
			}
		}
	}

	for _, check := range checks {
		key := privateAIMemoryLimitKey(check.Scope, check.Window, check.Value)
		entry, exists := privateAILimitsMemoryState.entries[key]
		if !exists || now.After(entry.ExpiresAt) {
			privateAILimitsMemoryState.entries[key] = privateAILimitMemoryEntry{
				Count:     1,
				ExpiresAt: now.Add(check.Duration),
			}
			continue
		}
		entry.Count++
		privateAILimitsMemoryState.entries[key] = entry
	}

	recordPrivateAILimitAllowed(checks)
	return nil
}

func parsePrivateAILimitScriptResult(result any) (bool, int64, int64, int64) {
	values, ok := result.([]any)
	if !ok || len(values) < 2 {
		return true, 0, 0, 0
	}
	decision := toInt64(values[0])
	blockedIndex := toInt64(values[1])
	blockedCount := int64(0)
	ttlSeconds := int64(0)
	if len(values) > 2 {
		blockedCount = toInt64(values[2])
	}
	if len(values) > 3 {
		ttlSeconds = toInt64(values[3])
	}
	return decision == 1, blockedIndex, blockedCount, ttlSeconds
}

func privateAILimitCheckByScriptIndex(checks []privateAILimitCheck, scriptIndex int64) privateAILimitCheck {
	if len(checks) == 0 {
		return privateAILimitCheck{
			Scope:  aiLimitScopeUser,
			Window: aiLimitWindowDay,
			Limit:  1,
		}
	}
	resolvedIndex := int(scriptIndex - 1)
	if resolvedIndex < 0 || resolvedIndex >= len(checks) {
		return checks[0]
	}
	return checks[resolvedIndex]
}

func privateAIRedisLimitKey(scope string, window string, value string) string {
	return fmt.Sprintf(
		"limits:ai:%s:%s:%s",
		strings.TrimSpace(scope),
		strings.TrimSpace(window),
		hashPrivateAILimitValue(value),
	)
}

func privateAIMemoryLimitKey(scope string, window string, value string) string {
	return fmt.Sprintf(
		"%s:%s:%s",
		strings.TrimSpace(scope),
		strings.TrimSpace(window),
		hashPrivateAILimitValue(value),
	)
}

func hashPrivateAILimitValue(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	encoded := hex.EncodeToString(sum[:])
	if len(encoded) > 24 {
		return encoded[:24]
	}
	return encoded
}

func recordPrivateAILimitAllowed(checks []privateAILimitCheck) {
	for _, check := range checks {
		monitor.AILimitChecksTotal.WithLabelValues(check.MetricTag, "allowed").Inc()
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

func normalizeDeviceIdentifier(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return ""
	}
	var builder strings.Builder
	for _, ch := range trimmed {
		if (ch >= 'a' && ch <= 'z') ||
			(ch >= '0' && ch <= '9') ||
			ch == '_' ||
			ch == '-' {
			builder.WriteRune(ch)
		}
	}
	return strings.TrimSpace(builder.String())
}

func normalizePrivateAILimitIP(raw string) string {
	normalized := netutil.NormalizeIP(raw)
	if normalized == "" {
		return ""
	}
	if !netutil.IsPublicIP(normalized) {
		return ""
	}
	return normalized
}

func logPrivateAILimitExceeded(
	endpoint string,
	exceeded *privateAILimitExceededError,
	userID string,
	roomID string,
	ipAddress string,
	deviceID string,
) {
	if exceeded == nil {
		return
	}

	resetIn := exceeded.ResetIn
	if resetIn <= 0 && !exceeded.ResetAt.IsZero() {
		resetIn = time.Until(exceeded.ResetAt)
	}
	if resetIn < 0 {
		resetIn = 0
	}

	resetAt := ""
	if !exceeded.ResetAt.IsZero() {
		resetAt = exceeded.ResetAt.UTC().Format(time.RFC3339)
	}

	normalizedIP := netutil.NormalizeIP(ipAddress)
	if normalizedIP == "" {
		normalizedIP = strings.TrimSpace(ipAddress)
	}

	log.Printf(
		"[ai-limit] blocked endpoint=%q scope=%q window=%q current=%d limit=%d reset_in=%s reset_at=%q scope_value=%q user_id=%q room_id=%q ip=%q device_id=%q",
		strings.TrimSpace(endpoint),
		strings.TrimSpace(exceeded.Scope),
		strings.TrimSpace(exceeded.Window),
		exceeded.Current,
		exceeded.Limit,
		resetIn.Round(time.Second).String(),
		resetAt,
		strings.TrimSpace(exceeded.Identifier),
		normalizeIdentifier(userID),
		normalizeRoomID(roomID),
		normalizedIP,
		normalizeDeviceIdentifier(deviceID),
	)
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
