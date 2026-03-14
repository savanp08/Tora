package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/savanp08/converse/internal/ai"
	"github.com/savanp08/converse/internal/config"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/handlers"
	"github.com/savanp08/converse/internal/monitor"
	"github.com/savanp08/converse/internal/router"
	"github.com/savanp08/converse/internal/security"
	"github.com/savanp08/converse/internal/storage"
	"github.com/savanp08/converse/internal/websocket"
	"github.com/savanp08/converse/internal/workers"
)

func main() {
	cfg := config.LoadConfig()
	if isServerLoggingEnabled() {
		log.SetOutput(newPrivacyLogWriter(os.Stderr))
	} else {
		log.SetOutput(io.Discard)
	}
	ai.RefreshDefaultProvidersFromEnv()
	log.Println("🚀 Starting Converse Backend...")
	websocket.SetTrustedProxies(cfg.TrustedProxies)

	redisStore, err := database.NewRedisStore(cfg.RedisAddr, cfg.RedisPass)
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	defer redisStore.Close()
	log.Println("✅ Connected to Redis")
	security.ConfigureRedisClient(redisStore.Client)

	var scyllaStore *database.ScyllaStore
	if (cfg.AstraToken != "" && cfg.AstraDatabaseID != "") || len(cfg.ScyllaHosts) > 0 {
		scyllaStore, err = database.NewScyllaStore(*cfg)
		if err != nil {
			log.Printf("⚠️  Warning: Could not connect to ScyllaDB: %v", err)
			log.Println("   (Running in 'Ephemeral Only' mode)")
		} else {
			defer scyllaStore.Close()
			log.Println("✅ Connected to ScyllaDB")
		}
	}

	msgService := websocket.NewMessageService(redisStore, scyllaStore)
	usageTracker := monitor.NewUsageTracker(scyllaStore, monitor.UsageLimits{
		MaxDailyRequests:       cfg.MaxDailyRequests,
		MaxDailyUploadBytes:    cfg.MaxDailyUploadBytes,
		MaxDailyBandwidthBytes: cfg.MaxDailyBandwidthBytes,
		MaxDailyMessages:       cfg.MaxDailyMessages,
		MaxDailyWsConnections:  cfg.MaxDailyWsConnections,
		MaxDailyFilesUploaded:  cfg.MaxDailyFilesUploaded,
	})
	defer usageTracker.Close()

	hub := websocket.NewHub(msgService, usageTracker)
	go hub.Run()

	var r2Client *storage.R2Client
	r2Client, err = storage.NewR2Client(*cfg)
	if err != nil {
		log.Printf("⚠️  Warning: Could not initialize R2 client: %v", err)
		log.Println("   (Uploads will be unavailable until R2 env vars are configured)")
	} else {
		log.Println("✅ Connected to Cloudflare R2")
	}

	mainRouter := router.New(hub, redisStore, scyllaStore, r2Client, usageTracker)
	mainRouter.Get("/metrics", promhttp.Handler().ServeHTTP)
	go startRoomExpiryCleanupWorker(redisStore, scyllaStore, r2Client)
	go workers.StartR2OrphanSweeper(context.Background(), redisStore, scyllaStore, r2Client)

	expiryEmailQueue, queueErr := workers.NewExpiryEmailQueue(
		cfg.RedisAddr,
		cfg.RedisPass,
		redisStore,
		scyllaStore,
		r2Client,
	)
	if queueErr != nil {
		log.Printf("⚠️  Warning: Could not initialize expiry email queue: %v", queueErr)
	} else {
		defer expiryEmailQueue.Shutdown()
		go func() {
			if runErr := expiryEmailQueue.Run(); runErr != nil {
				log.Printf("[expiry-email-worker] run failed: %v", runErr)
			}
		}()
	}

	log.Printf("📡 Server listening on port %s", cfg.Port)
	server := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: mainRouter,
	}

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- server.ListenAndServe()
	}()

	var listenErr error
	select {
	case <-signalCtx.Done():
		log.Println("🛑 Shutdown signal received")
	case err := <-serverErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr = err
			log.Printf("Server failed: %v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil &&
		!errors.Is(err, context.Canceled) &&
		!errors.Is(err, http.ErrServerClosed) {
		log.Printf("Server shutdown encountered an error: %v", err)
	}
	handlers.ShutdownExecutionManager()

	if listenErr != nil {
		return
	}
}

func startRoomExpiryCleanupWorker(
	redisStore *database.RedisStore,
	scyllaStore *database.ScyllaStore,
	r2Client *storage.R2Client,
) {
	if redisStore == nil || redisStore.Client == nil {
		return
	}

	ctx := context.Background()
	const keyEventChannel = "__keyevent@0__:expired"
	for {
		pubsub := redisStore.Client.Subscribe(ctx, keyEventChannel)
		if _, err := pubsub.Receive(ctx); err != nil {
			log.Printf("[expiry-worker] subscribe failed: %v", err)
			_ = pubsub.Close()
			time.Sleep(time.Second)
			continue
		}

		channel := pubsub.Channel()
		for message := range channel {
			if message == nil {
				continue
			}
			roomID := extractRoomIDFromExpiredKey(message.Payload)
			if roomID == "" {
				continue
			}
			go cleanupExpiredRoom(context.Background(), redisStore, scyllaStore, r2Client, roomID)
		}

		_ = pubsub.Close()
		time.Sleep(time.Second)
	}
}

func cleanupExpiredRoom(
	ctx context.Context,
	redisStore *database.RedisStore,
	scyllaStore *database.ScyllaStore,
	r2Client *storage.R2Client,
	roomID string,
) {
	normalizedRoomID := normalizeRoomIDForCleanup(roomID)
	if normalizedRoomID == "" {
		return
	}

	filesKey := fmt.Sprintf("room:%s:files", normalizedRoomID)
	var objectKeys []string
	if redisStore != nil && redisStore.Client != nil {
		keys, err := redisStore.Client.SMembers(ctx, filesKey).Result()
		if err != nil {
			log.Printf("[expiry-worker] room file index lookup failed: %v", err)
		} else {
			objectKeys = keys
		}
	}

	if r2Client != nil && len(objectKeys) > 0 {
		deleteCtx, cancelDelete := context.WithTimeout(ctx, 45*time.Second)
		deletedBytes, sizeErr := r2Client.SumObjectSizes(deleteCtx, objectKeys)
		if sizeErr != nil {
			log.Printf("[expiry-worker] r2 object size lookup failed files=%d err=%v", len(objectKeys), sizeErr)
		}
		if err := r2Client.DeleteObjects(deleteCtx, objectKeys); err != nil {
			log.Printf("[expiry-worker] r2 cleanup failed files=%d err=%v", len(objectKeys), err)
			cancelDelete()
			return
		} else if deletedBytes > 0 {
			if _, usageErr := storage.DecrementR2UsageBytes(deleteCtx, redisStore, deletedBytes); usageErr != nil {
				log.Printf("[expiry-worker] failed to decrement r2 usage bytes deleted_bytes=%d err=%v", deletedBytes, usageErr)
			}
		}
		cancelDelete()
	}

	if redisStore != nil && redisStore.Client != nil {
		if err := redisStore.Client.Del(ctx, filesKey).Err(); err != nil {
			log.Printf("[expiry-worker] failed to clear room file index: %v", err)
		}
	}

	if scyllaStore != nil && scyllaStore.Session != nil {
		deleteCtx, cancelDelete := context.WithTimeout(ctx, 30*time.Second)
		messagesTable := scyllaStore.Table("messages")
		deleteQuery := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ?`, messagesTable)
		if err := scyllaStore.Session.Query(deleteQuery, normalizedRoomID).WithContext(deleteCtx).Exec(); err != nil {
			log.Printf("[expiry-worker] scylla partition delete failed: %v", err)
		}
		cancelDelete()
	}
}

func isServerLoggingEnabled() bool {
	value := strings.TrimSpace(os.Getenv("ENABLE_SERVER_LOGS"))
	if value == "" {
		return true
	}
	switch strings.ToLower(value) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

type privacyLogWriter struct {
	target io.Writer
}

var (
	privacyLogKeyValuePattern = regexp.MustCompile(`\b(room|user|email|message|comment|pin|device|ip|remote|object|key|owner|repo|ref|filename|parent|origin|msg|action)=([^\s,]+)`)
	privacyLogEmailPattern    = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	privacyLogIPv4Pattern     = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?\b`)
	privacyLogIDAfterWord     = regexp.MustCompile(`\b(room|user|client|sender)\s+[A-Za-z0-9._:\-]{3,}`)
)

func newPrivacyLogWriter(target io.Writer) io.Writer {
	return &privacyLogWriter{target: target}
}

func (w *privacyLogWriter) Write(p []byte) (int, error) {
	if w == nil || w.target == nil {
		return len(p), nil
	}
	sanitized := sanitizeLogLine(string(p))
	if _, err := w.target.Write([]byte(sanitized)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func sanitizeLogLine(raw string) string {
	sanitized := privacyLogKeyValuePattern.ReplaceAllString(raw, `$1=[redacted]`)
	sanitized = privacyLogEmailPattern.ReplaceAllString(sanitized, "[redacted-email]")
	sanitized = privacyLogIPv4Pattern.ReplaceAllString(sanitized, "[redacted-ip]")
	sanitized = privacyLogIDAfterWord.ReplaceAllStringFunc(sanitized, func(fragment string) string {
		parts := strings.Fields(fragment)
		if len(parts) == 0 {
			return "[redacted]"
		}
		return parts[0] + " [redacted]"
	})
	return sanitized
}

func extractRoomIDFromExpiredKey(key string) string {
	trimmed := strings.TrimSpace(key)
	if !strings.HasPrefix(trimmed, "room:") {
		return ""
	}
	withoutPrefix := strings.TrimPrefix(trimmed, "room:")
	if withoutPrefix == "" || strings.Contains(withoutPrefix, ":") {
		return ""
	}
	return normalizeRoomIDForCleanup(withoutPrefix)
}

func normalizeRoomIDForCleanup(raw string) string {
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
