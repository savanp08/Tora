package main

import (
	"log"
	"net/http"

	"github.com/savanp08/converse/internal/config"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/monitor"
	"github.com/savanp08/converse/internal/router"
	"github.com/savanp08/converse/internal/storage"
	"github.com/savanp08/converse/internal/websocket"
)

func main() {
	cfg := config.LoadConfig()
	log.Println("🚀 Starting Converse Backend...")
	websocket.SetTrustedProxies(cfg.TrustedProxies)

	redisStore, err := database.NewRedisStore(cfg.RedisAddr, cfg.RedisPass)
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	defer redisStore.Close()
	log.Println("✅ Connected to Redis")

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

	log.Printf("📡 Server listening on port %s", cfg.Port)
	if err := http.ListenAndServe(":"+cfg.Port, mainRouter); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
