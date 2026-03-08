package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/savanp08/converse/internal/monitor"
	"golang.org/x/time/rate"
)

var (
	redisClientMu sync.RWMutex
	redisClient   *redis.Client
)

type keyLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type Limiter struct {
	mu           sync.Mutex
	entries      map[string]*keyLimiter
	rateLimit    rate.Limit
	burst        int
	ttl          time.Duration
	cleanupEvery time.Duration
	lastCleanup  time.Time
}

func NewLimiter(events int, per time.Duration, burst int, ttl time.Duration) *Limiter {
	if events <= 0 {
		events = 1
	}
	if per <= 0 {
		per = time.Second
	}
	if burst <= 0 {
		burst = events
	}
	if ttl <= 0 {
		ttl = 10 * per
	}

	cleanupEvery := ttl / 2
	if cleanupEvery <= 0 {
		cleanupEvery = per
	}

	return &Limiter{
		entries:      make(map[string]*keyLimiter),
		rateLimit:    rate.Limit(float64(events) / per.Seconds()),
		burst:        burst,
		ttl:          ttl,
		cleanupEvery: cleanupEvery,
		lastCleanup:  time.Now(),
	}
}

func (l *Limiter) Allow(key string) bool {
	if l == nil {
		return true
	}

	normalizedKey := strings.TrimSpace(key)
	if normalizedKey == "" {
		normalizedKey = "global"
	}

	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.cleanupStale(now)

	entry, ok := l.entries[normalizedKey]
	if !ok {
		entry = &keyLimiter{
			limiter:  rate.NewLimiter(l.rateLimit, l.burst),
			lastSeen: now,
		}
		l.entries[normalizedKey] = entry
	}
	entry.lastSeen = now

	return entry.limiter.Allow()
}

func (l *Limiter) cleanupStale(now time.Time) {
	if now.Sub(l.lastCleanup) < l.cleanupEvery {
		return
	}
	for key, entry := range l.entries {
		if now.Sub(entry.lastSeen) > l.ttl {
			delete(l.entries, key)
		}
	}
	l.lastCleanup = now
}

func ConfigureRedisClient(client *redis.Client) {
	redisClientMu.Lock()
	redisClient = client
	redisClientMu.Unlock()
}

func RecordIPActivity(ctx context.Context, ip string, action string) {
	normalizedIP := strings.TrimSpace(ip)
	if normalizedIP == "" {
		return
	}

	normalizedAction := strings.TrimSpace(strings.ToLower(action))
	if normalizedAction == "" {
		normalizedAction = "activity"
	}
	switch normalizedAction {
	case "rooms_created", "connections":
	default:
		normalizedAction = "activity"
	}

	redisClientMu.RLock()
	client := redisClient
	redisClientMu.RUnlock()
	if client == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	hashedIP := hashIP(normalizedIP)
	baseKey := fmt.Sprintf("stats:ip:%s", hashedIP)
	pipe := client.TxPipeline()
	pipe.HIncrBy(ctx, baseKey, normalizedAction, 1)
	pipe.Expire(ctx, baseKey, 24*time.Hour)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("[security] redis ip activity write failed action=%s err=%v", normalizedAction, err)
		return
	}

	if normalizedAction != "rooms_created" {
		return
	}

	hourlyKey := fmt.Sprintf("stats:ip:%s:rooms_created:%s", hashedIP, time.Now().UTC().Format("2006010215"))
	count, err := client.Incr(ctx, hourlyKey).Result()
	if err != nil {
		log.Printf("[security] redis ip hourly room counter failed err=%v", err)
		return
	}
	_ = client.Expire(ctx, hourlyKey, time.Hour).Err()
	if count > 50 {
		monitor.SecurityBlocksTotal.WithLabelValues("ip_room_spam").Inc()
	}
}

func hashIP(ip string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(ip)))
	encoded := hex.EncodeToString(sum[:])
	if len(encoded) > 24 {
		return encoded[:24]
	}
	return encoded
}
