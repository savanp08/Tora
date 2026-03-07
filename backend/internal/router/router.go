package router

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/handlers"
	"github.com/savanp08/converse/internal/monitor"
	"github.com/savanp08/converse/internal/security"
	"github.com/savanp08/converse/internal/storage"
	"github.com/savanp08/converse/internal/websocket"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

func New(
	hub *websocket.Hub,
	redisStore *database.RedisStore,
	scyllaStore *database.ScyllaStore,
	r2Client *storage.R2Client,
	usageTracker *monitor.UsageTracker,
) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(websocket.CaptureOriginalRemoteAddr)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:5173", "https://tora.monokenos.com", "http://192.168.1.165:5173"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))
	if usageTracker != nil {
		r.Use(usageTracker.Middleware)
	}

	authHandler := handlers.NewAuthHandler()
	roomHandler := handlers.NewRoomHandler(hub, redisStore, scyllaStore)
	uploadHandler := handlers.NewUploadHandler(r2Client, redisStore, usageTracker)
	handlers.ConfigureCanvasPersistence(redisStore, scyllaStore, r2Client, usageTracker)
	promoteLimiter := security.NewLimiter(5, time.Minute, 5, time.Minute)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		type dependencyStatus struct {
			Status string `json:"status"`
			Error  string `json:"error,omitempty"`
		}
		type healthResponse struct {
			Status       string                      `json:"status"`
			Dependencies map[string]dependencyStatus `json:"dependencies"`
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		dependencies := map[string]dependencyStatus{
			"redis":  {Status: "up"},
			"scylla": {Status: "up"},
		}
		overallStatus := "ok"
		statusCode := http.StatusOK

		if redisStore == nil {
			dependencies["redis"] = dependencyStatus{Status: "down", Error: "redis store not configured"}
			overallStatus = "degraded"
			statusCode = http.StatusServiceUnavailable
		} else if err := redisStore.Ping(ctx); err != nil {
			dependencies["redis"] = dependencyStatus{Status: "down", Error: err.Error()}
			overallStatus = "degraded"
			statusCode = http.StatusServiceUnavailable
		}

		if scyllaStore == nil {
			dependencies["scylla"] = dependencyStatus{Status: "down", Error: "scylla store not configured"}
			overallStatus = "degraded"
			statusCode = http.StatusServiceUnavailable
		} else if err := scyllaStore.Ping(ctx); err != nil {
			dependencies["scylla"] = dependencyStatus{Status: "down", Error: err.Error()}
			overallStatus = "degraded"
			statusCode = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(healthResponse{
			Status:       overallStatus,
			Dependencies: dependencies,
		})
	})
	if usageTracker != nil {
		r.Get("/api/usage", usageTracker.HandleUsage)
	}

	r.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
		websocket.ServeWs(hub, w, r)
	})
	r.Get("/ws/canvas/{roomId}", func(w http.ResponseWriter, r *http.Request) {
		handlers.ServeCanvasWS(w, r, chi.URLParam(r, "roomId"))
	})
	r.Get("/ws/canvas", func(w http.ResponseWriter, r *http.Request) {
		roomID := strings.TrimSpace(r.URL.Query().Get("roomId"))
		if roomID == "" {
			roomID = strings.TrimSpace(r.URL.Query().Get("room"))
		}
		handlers.ServeCanvasWS(w, r, roomID)
	})

	r.Route("/api", func(r chi.Router) {
		r.Use(middleware.Timeout(60 * time.Second))
		r.Post("/execute", handlers.HandleCodeExecution)
		r.Post("/auth/signup", authHandler.SignUp)
		r.Post("/auth/login", authHandler.Login)
		r.Post("/auth/anonymous", authHandler.Anonymous)

		r.Post("/rooms", roomHandler.CreateRoom)
		r.Post("/rooms/join", roomHandler.JoinRoom)
		r.Post("/rooms/leave", roomHandler.LeaveRoom)
		r.Post("/rooms/extend", roomHandler.ExtendRoom)
		r.Post("/rooms/rename", roomHandler.RenameRoom)
		r.Post("/rooms/break", roomHandler.CreateBreakRoom)
		r.Post("/rooms/remove-member", roomHandler.RemoveRoomMember)
		r.Post("/rooms/delete", roomHandler.DeleteRoom)
		r.With(rateLimitMiddleware(promoteLimiter, "Admin promotion rate limit exceeded")).Post(
			"/rooms/{id}/promote",
			roomHandler.PromoteToAdmin,
		)
		r.Get("/rooms/sidebar", roomHandler.GetSidebarRooms)
		r.Get("/rooms/{id}", roomHandler.GetRoom)
		r.Get("/rooms/{id}/board", roomHandler.GetBoardElements)
		r.Get("/rooms/{roomId}/messages", roomHandler.GetRoomMessages)
		r.Post("/rooms/{roomId}/pins", roomHandler.UpsertRoomPin)
		r.Get("/rooms/{roomId}/pins/navigate", roomHandler.NavigateRoomPins)
		r.Get("/rooms/{roomId}/pins/{pinMessageId}/discussion/comments", roomHandler.GetPinnedDiscussionComments)
		r.Post("/rooms/{roomId}/pins/{pinMessageId}/discussion/comments", roomHandler.CreatePinnedDiscussionComment)
		r.Put("/rooms/{roomId}/pins/{pinMessageId}/discussion/comments/{commentId}", roomHandler.EditPinnedDiscussionComment)
		r.Delete("/rooms/{roomId}/pins/{pinMessageId}/discussion/comments/{commentId}", roomHandler.DeletePinnedDiscussionComment)
		r.Post("/upload/presigned", uploadHandler.GenerateUploadURL)
		r.Post("/upload", uploadHandler.UploadProxy)
		r.Get("/upload/object/*", uploadHandler.ServeObject)
		r.Get("/canvas/{roomId}/snapshot", handlers.HandleCanvasSnapshotLoad)
		r.Post("/canvas/{roomId}/snapshot", handlers.HandleCanvasSnapshotSave)
		r.Get("/canvas/snapshot", handlers.HandleCanvasSnapshotLoad)
		r.Post("/canvas/snapshot", handlers.HandleCanvasSnapshotSave)
		r.Get("/canvas/github-archive", handlers.ProxyGitHubRepoArchive)
	})

	return r
}

func rateLimitMiddleware(limiter *security.Limiter, message string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limiter != nil && !limiter.Allow(extractClientIP(r)) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": strings.TrimSpace(message)})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractClientIP(r *http.Request) string {
	if r == nil {
		return "unknown"
	}

	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
	}

	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}

	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	if strings.TrimSpace(r.RemoteAddr) != "" {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return "unknown"
}
