package router

import (
	"net/http"
	"time"

	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/handlers"
	"github.com/savanp08/converse/internal/monitor"
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
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:5173"},
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
	roomHandler := handlers.NewRoomHandler(redisStore, scyllaStore)
	uploadHandler := handlers.NewUploadHandler(r2Client, usageTracker)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	if usageTracker != nil {
		r.Get("/api/usage", usageTracker.HandleUsage)
	}

	r.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
		websocket.ServeWs(hub, w, r)
	})

	r.Route("/api", func(r chi.Router) {
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
		r.Get("/rooms/sidebar", roomHandler.GetSidebarRooms)
		r.Get("/rooms/{roomId}/messages", roomHandler.GetRoomMessages)
		r.Get("/rooms/{roomId}/pins/navigate", roomHandler.NavigateRoomPins)
		r.Get("/rooms/{roomId}/pins/{pinMessageId}/discussion/comments", roomHandler.GetPinnedDiscussionComments)
		r.Post("/rooms/{roomId}/pins/{pinMessageId}/discussion/comments", roomHandler.CreatePinnedDiscussionComment)
		r.Put("/rooms/{roomId}/pins/{pinMessageId}/discussion/comments/{commentId}", roomHandler.EditPinnedDiscussionComment)
		r.Delete("/rooms/{roomId}/pins/{pinMessageId}/discussion/comments/{commentId}", roomHandler.DeletePinnedDiscussionComment)
		r.Post("/upload/presigned", uploadHandler.GenerateUploadURL)
		r.Post("/upload", uploadHandler.UploadProxy)
		r.Get("/upload/object/*", uploadHandler.ServeObject)
	})

	return r
}
