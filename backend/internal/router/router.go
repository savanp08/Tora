package router

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	jwtutil "github.com/savanp08/converse/internal/auth"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/handlers"
	"github.com/savanp08/converse/internal/monitor"
	"github.com/savanp08/converse/internal/netutil"
	"github.com/savanp08/converse/internal/repository"
	"github.com/savanp08/converse/internal/security"
	"github.com/savanp08/converse/internal/storage"
	"github.com/savanp08/converse/internal/websocket"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

const (
	apiDefaultRequestTimeout = 60 * time.Second
	apiLongAIRequestTimeout  = 20 * time.Minute
)

func isLongRunningAIAPIPath(path string) bool {
	normalizedPath := strings.ToLower(strings.TrimSpace(path))
	switch {
	case strings.HasSuffix(normalizedPath, "/ai-organize"):
		return true
	case strings.HasSuffix(normalizedPath, "/ai-timeline"):
		return true
	case strings.HasSuffix(normalizedPath, "/ai-timeline/stream"):
		return true
	case strings.HasSuffix(normalizedPath, "/ai-edit"):
		return true
	case strings.HasSuffix(normalizedPath, "/ai-edit/stream"):
		return true
	default:
		return false
	}
}

func apiTimeoutMiddleware() func(http.Handler) http.Handler {
	defaultTimeout := middleware.Timeout(apiDefaultRequestTimeout)
	longAITimeout := middleware.Timeout(apiLongAIRequestTimeout)

	return func(next http.Handler) http.Handler {
		defaultHandler := defaultTimeout(next)
		longAIHandler := longAITimeout(next)

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isLongRunningAIAPIPath(r.URL.Path) {
				longAIHandler.ServeHTTP(w, r)
				return
			}
			defaultHandler.ServeHTTP(w, r)
		})
	}
}

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
		AllowedOrigins: []string{
			"http://localhost:*",
			"http://127.0.0.1:*",
			"https://tora.monokenos.com",
			"http://192.168.1.165:5173",
		},
		AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{
			"Accept",
			"Authorization",
			"Content-Type",
			"X-CSRF-Token",
			"X-User-Id",
			"X-User-Name",
			"X-Username",
			"X-Device-Id",
			"X-Device-ID",
			"X-Ide-Mode",
			"X-Ide-Session-Id",
			"X-Room-Id",
		},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))
	if usageTracker != nil {
		r.Use(usageTracker.Middleware)
	}

	authHandler := handlers.NewAuthHandler(scyllaStore)
	dashboardHandler := handlers.NewDashboardHandler(scyllaStore)
	personalRepo := repository.NewPersonalRepo(scyllaStore)
	personalHandler := handlers.NewPersonalHandler(personalRepo)
	networkRepo := repository.NewNetworkRepo(scyllaStore)
	networkHandler := handlers.NewNetworkHandler(networkRepo, scyllaStore)
	roomHandler := handlers.NewRoomHandler(hub, redisStore, scyllaStore)
	uploadHandler := handlers.NewUploadHandler(r2Client, redisStore, usageTracker)
	handlers.ConfigureCanvasPersistence(redisStore, scyllaStore, r2Client, usageTracker)
	handlers.ConfigureAIChatPersistence(redisStore, scyllaStore)
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
		r.Use(apiTimeoutMiddleware())
		r.Post("/ide/execute", handlers.HandleIDECodeExecution)
		r.Post("/ide/ai/chat", handlers.HandleIDEPrivateAIChat)
		r.Post("/ide/ai/private-chat", handlers.HandleIDEPrivateAIChat)
		r.Post("/execute", handlers.HandleCodeExecution)
		r.Post("/ai/chat", handlers.HandlePrivateAIChat)
		r.Post("/ai/private-chat", handlers.HandlePrivateAIChat)
		r.Get("/templates", roomHandler.GetIndustryTemplates)
		r.Post("/auth/register", authHandler.Register)
		r.Post("/auth/signup", authHandler.SignUp)
		r.Post("/auth/login", authHandler.Login)
		r.Post("/auth/logout", authHandler.Logout)
		r.Post("/auth/forgot-password/request", authHandler.ForgotPasswordRequest)
		r.Post("/auth/forgot-password/verify", authHandler.ForgotPasswordVerify)
		r.Post("/auth/anonymous", authHandler.Anonymous)
		r.Get("/auth/google", authHandler.GoogleLogin)
		r.Get("/auth/google/callback", authHandler.GoogleCallback)
		r.With(authJWTContextMiddleware()).Get("/auth/me", authHandler.Me)
		r.With(authJWTContextMiddleware()).Get("/dashboard/rooms", dashboardHandler.GetRooms)
		r.With(authJWTContextMiddleware()).Get("/dashboard/overview", dashboardHandler.GetOverview)
		r.With(authJWTContextMiddleware()).Route("/personal/items", func(r chi.Router) {
			r.Get("/", personalHandler.GetItems)
			r.Post("/", personalHandler.CreateItem)
			r.Post("/bulk", personalHandler.CreateItemsBulk)
			r.Put("/{itemId}/status", personalHandler.UpdateItemStatus)
			r.Delete("/{itemId}", personalHandler.DeleteItem)
		})
		r.With(authJWTContextMiddleware()).Post("/network/request", networkHandler.SendConnectionRequest)
		r.With(authJWTContextMiddleware()).Post("/network/accept", networkHandler.AcceptConnectionRequest)
		r.With(authJWTContextMiddleware()).Get("/network/pending", networkHandler.ListPendingRequests)
		r.With(authJWTContextMiddleware()).Get("/network/connections", networkHandler.ListConnections)
		r.With(authJWTContextMiddleware()).Post("/rooms/direct", roomHandler.CreateDirectRoom)
		r.Post("/rooms/{roomId}/tasks", roomHandler.CreateRoomTask)
		r.Delete("/rooms/{roomId}/tasks", roomHandler.DeleteRoomTasks)
		r.Delete("/rooms/{roomId}/tasks/{taskId}", roomHandler.DeleteRoomTask)
		r.Put("/rooms/{roomId}/tasks/{taskId}", roomHandler.UpdateRoomTask)
		r.Patch("/rooms/{roomId}/tasks/{taskId}", roomHandler.UpdateRoomTask)
		r.Post("/rooms/{roomId}/tasks/{taskId}/details/generate", roomHandler.GenerateRoomTaskDetails)
		r.Get("/rooms/{roomId}/groups", roomHandler.ListGroups)
		r.Post("/rooms/{roomId}/groups", roomHandler.CreateGroup)
		r.Put("/rooms/{roomId}/groups/{groupId}", roomHandler.UpdateGroup)
		r.Delete("/rooms/{roomId}/groups/{groupId}", roomHandler.DeleteGroup)
		r.Get("/workspaces/{workspaceId}/groups", roomHandler.ListGroups)
		r.Post("/workspaces/{workspaceId}/groups", roomHandler.CreateGroup)
		r.Put("/workspaces/{workspaceId}/groups/{groupId}", roomHandler.UpdateGroup)
		r.Delete("/workspaces/{workspaceId}/groups/{groupId}", roomHandler.DeleteGroup)
		r.Put("/rooms/{roomId}/tasks/{taskId}/status", roomHandler.UpdateRoomTaskStatus)
		r.Post("/rooms/{roomId}/tasks/{taskId}/relations", roomHandler.CreateRoomTaskRelation)
		r.Patch("/rooms/{roomId}/tasks/{taskId}/relations/{toTaskId}", roomHandler.UpdateRoomTaskRelation)
		r.Delete("/rooms/{roomId}/tasks/{taskId}/relations/{toTaskId}", roomHandler.DeleteRoomTaskRelation)
		r.Get("/rooms/{roomId}/field-schemas", roomHandler.GetRoomFieldSchemas)
		r.Post("/rooms/{roomId}/field-schemas", roomHandler.CreateRoomFieldSchema)
		r.Patch("/rooms/{roomId}/field-schemas/{fieldId}", roomHandler.UpdateRoomFieldSchema)
		r.Delete("/rooms/{roomId}/field-schemas/{fieldId}", roomHandler.DeleteRoomFieldSchema)
		r.Post("/rooms/{roomId}/apply-template", roomHandler.ApplyRoomTemplate)
		r.Get("/rooms/{roomId}/forms", roomHandler.GetRoomIntakeForms)
		r.Post("/rooms/{roomId}/forms", roomHandler.CreateRoomIntakeForm)
		r.Patch("/rooms/{roomId}/forms/{formId}", roomHandler.UpdateRoomIntakeForm)
		r.Delete("/rooms/{roomId}/forms/{formId}", roomHandler.DeleteRoomIntakeForm)
		r.Get("/rooms/{roomId}/forms/{formId}/submissions", roomHandler.GetRoomIntakeFormSubmissions)
		r.Get("/f/{formId}", roomHandler.GetPublicIntakeForm)
		r.Post("/f/{formId}", roomHandler.SubmitPublicIntakeForm)
		r.Get("/rooms/{roomId}/ai-context", roomHandler.GetRoomAIContext)

		r.Post("/rooms", roomHandler.CreateRoom)
		r.Post("/rooms/revive", roomHandler.ReviveRoom)
		r.Post("/rooms/join", roomHandler.JoinRoom)
		r.Post("/rooms/leave", roomHandler.LeaveRoom)
		r.Post("/rooms/extend", roomHandler.ExtendRoom)
		r.Post("/rooms/rename", roomHandler.RenameRoom)
		r.Post("/rooms/break", roomHandler.CreateBreakRoom)
		r.Post("/rooms/remove-member", roomHandler.RemoveRoomMember)
		r.Post("/rooms/delete", roomHandler.DeleteRoom)
		r.Patch("/rooms/{id}", roomHandler.PatchRoom)
		r.Patch("/workspaces/{id}", roomHandler.PatchRoom)
		r.With(rateLimitMiddleware(promoteLimiter, "Admin promotion rate limit exceeded")).Post(
			"/rooms/{id}/promote",
			roomHandler.PromoteToAdmin,
		)
		r.Get("/rooms/sidebar", roomHandler.GetSidebarRooms)
		r.Get("/rooms/{id}", roomHandler.GetRoom)
		r.Get("/rooms/{id}/board", roomHandler.GetBoardElements)
		r.Get("/rooms/{roomId}/tasks", roomHandler.GetRoomTasks)
		r.Get("/rooms/{roomId}/messages", roomHandler.GetRoomMessages)
		r.Post("/rooms/{roomId}/ai-organize", roomHandler.AIOrganizeDashboard)
		r.Post("/rooms/{roomId}/ai-timeline", roomHandler.HandleAIGenerateTimeline)
		r.Post("/rooms/{roomId}/ai-timeline/stream", roomHandler.HandleAIGenerateTimelineStream)
		r.Post("/rooms/{roomId}/ai-edit", roomHandler.HandleAIEditTimeline)
		r.Post("/rooms/{roomId}/ai-edit/stream", roomHandler.HandleAIEditTimelineStream)
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
		r.Post("/canvas/{roomId}/files", handlers.HandleCanvasFileMirrorSync)
		r.Get("/canvas/snapshot", handlers.HandleCanvasSnapshotLoad)
		r.Post("/canvas/snapshot", handlers.HandleCanvasSnapshotSave)
		r.Get("/canvas/github-archive", handlers.ProxyGitHubRepoArchive)
	})

	return r
}

func authJWTContextMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := readJWTFromRequest(r)
			if token == "" {
				writeUnauthorizedJSON(w)
				return
			}

			claims, err := jwtutil.ValidateToken(token)
			if err != nil || claims == nil || strings.TrimSpace(claims.UserID) == "" {
				writeUnauthorizedJSON(w)
				return
			}

			ctx := handlers.WithAuthUserID(r.Context(), claims.UserID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func readJWTFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if cookie, err := r.Cookie("tora_auth"); err == nil {
		if token := strings.TrimSpace(cookie.Value); token != "" {
			return token
		}
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if authorization == "" {
		return ""
	}
	const bearerPrefix = "Bearer "
	if !strings.HasPrefix(authorization, bearerPrefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(authorization, bearerPrefix))
}

func writeUnauthorizedJSON(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "Authentication required"})
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
	return netutil.ExtractClientIP(r)
}
