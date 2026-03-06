package handlers

import (
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	mrand "math/rand"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/gocql/gocql"
	"github.com/redis/go-redis/v9"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/models"
	"github.com/savanp08/converse/internal/security"
	"github.com/savanp08/converse/internal/websocket"
	namegen "github.com/savanp08/converse/utils"
)

const (
	roomDefaultTTL       = 6 * time.Hour
	roomExtendedTTL      = 24 * time.Hour
	roomMaxExtendAge     = 15 * 24 * time.Hour
	roomMaxDescendants   = 6
	roomMinDurationHours = 0.1
	roomMaxDurationHours = 15.0 * 24.0
	roomHistoryTTL       = roomDefaultTTL
	roomHistorySize      = 50
	roomCodeDigits       = 6
	roomAdminCodeLength  = 4
	roomNameMaxLength    = 20
	roomIDLength         = 14
	roomIDAlphabet       = "abcdefghijklmnopqrstuvwxyz0123456789"
	roomAdminCodeCharset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	roomTreeNumberPrefix = "user:tree_numbers:"
	roomNameIndexPrefix  = "room:name:"
	messageBreakPrefix   = "message:break:"
	roomNameRetryLimit   = 3
	roomSoftExpiryTable  = "room_message_soft_expiry"
	roomPasswordMaxLen   = 64
)

var (
	errRoomFull       = errors.New("room full")
	roomCreateLimiter = security.NewLimiter(10, time.Minute, 3, 15*time.Minute)
	JoinRoomLimiter   = security.NewLimiter(20, time.Minute, 20, 15*time.Minute)
	ExtendRoomLimiter = security.NewLimiter(5, 10*time.Minute, 5, 30*time.Minute)
)

type RoomHandler struct {
	hub    *websocket.Hub
	redis  *database.RedisStore
	scylla *database.ScyllaStore
}

func NewRoomHandler(hub *websocket.Hub, redisStore *database.RedisStore, scyllaStore *database.ScyllaStore) *RoomHandler {
	handler := &RoomHandler{hub: hub, redis: redisStore, scylla: scyllaStore}
	handler.ensureRoomSchema()
	handler.ensureRoomMessageSoftExpirySchema()
	handler.ensurePinnedDiscussionSchema()
	return handler
}

func (h *RoomHandler) broadcastRoomEvent(roomID string, eventType string, fields map[string]interface{}) {
	if h == nil || h.hub == nil {
		return
	}
	normalizedRoomID := normalizeRoomID(roomID)
	normalizedEventType := strings.ToLower(strings.TrimSpace(eventType))
	if normalizedRoomID == "" || normalizedEventType == "" {
		return
	}

	payload := map[string]interface{}{}
	for key, value := range fields {
		payload[key] = value
	}
	payload["type"] = normalizedEventType
	payload["roomId"] = normalizedRoomID
	h.hub.BroadcastToRoom(normalizedRoomID, payload)
}

type JoinRoomRequest struct {
	RoomID            string  `json:"roomId"`
	RoomName          string  `json:"roomName"`
	RoomCode          string  `json:"roomCode"`
	RoomPassword      string  `json:"roomPassword"`
	Username          string  `json:"username"`
	UserID            string  `json:"userId"`
	Type              string  `json:"type"`
	Mode              string  `json:"mode"`
	RoomDurationHours float64 `json:"roomDurationHours"`
}

type JoinRoomResponse struct {
	RoomID           string `json:"roomId"`
	RoomName         string `json:"roomName"`
	RoomCode         string `json:"roomCode,omitempty"`
	AdminCode        string `json:"adminCode,omitempty"`
	UserID           string `json:"userId"`
	Token            string `json:"token"`
	CreatedAt        int64  `json:"createdAt"`
	ExpiresAt        int64  `json:"expiresAt,omitempty"`
	IsAdmin          bool   `json:"isAdmin,omitempty"`
	RequiresPassword bool   `json:"requiresPassword,omitempty"`
	ServerNow        int64  `json:"serverNow,omitempty"`
}

type ExtendRoomRequest struct {
	RoomID string `json:"roomId"`
}

type LeaveRoomRequest struct {
	RoomID string `json:"roomId"`
	UserID string `json:"userId"`
}

type ExtendRoomResponse struct {
	RoomID           string `json:"roomId"`
	ExpiresInSeconds int64  `json:"expiresInSeconds"`
	ExpiresAt        int64  `json:"expiresAt,omitempty"`
	Message          string `json:"message"`
	ServerNow        int64  `json:"serverNow,omitempty"`
}

type LeaveRoomResponse struct {
	RoomID    string `json:"roomId"`
	UserID    string `json:"userId"`
	Message   string `json:"message"`
	ServerNow int64  `json:"serverNow,omitempty"`
}

type RenameRoomRequest struct {
	RoomID   string `json:"roomId"`
	RoomName string `json:"roomName"`
}

type RenameRoomResponse struct {
	RoomID   string `json:"roomId"`
	RoomName string `json:"roomName"`
}

type CreateBreakRoomRequest struct {
	ParentRoomID    string `json:"parentRoomId"`
	OriginMessageID string `json:"originMessageId"`
	RoomName        string `json:"roomName"`
	RoomPassword    string `json:"roomPassword"`
	UserID          string `json:"userId"`
	Username        string `json:"username"`
}

type CreateBreakRoomResponse struct {
	RoomID           string `json:"roomId"`
	RoomName         string `json:"roomName"`
	ParentRoomID     string `json:"parentRoomId"`
	OriginMessageID  string `json:"originMessageId"`
	CreatedAt        int64  `json:"createdAt"`
	ExpiresAt        int64  `json:"expiresAt,omitempty"`
	RequiresPassword bool   `json:"requiresPassword,omitempty"`
	ServerNow        int64  `json:"serverNow,omitempty"`
}

type SidebarRoom struct {
	RoomID           string `json:"roomId"`
	RoomName         string `json:"roomName"`
	Status           string `json:"status"`
	ParentRoomID     string `json:"parentRoomId,omitempty"`
	OriginMessageID  string `json:"originMessageId,omitempty"`
	TreeNumber       int    `json:"treeNumber"`
	MemberCount      int    `json:"memberCount"`
	CreatedAt        int64  `json:"createdAt"`
	ExpiresAt        int64  `json:"expiresAt,omitempty"`
	IsAdmin          bool   `json:"isAdmin,omitempty"`
	AdminCode        string `json:"adminCode,omitempty"`
	RequiresPassword bool   `json:"requiresPassword,omitempty"`
}

type SidebarRoomsResponse struct {
	Rooms     []SidebarRoom `json:"rooms"`
	ServerNow int64         `json:"serverNow,omitempty"`
}

type RoomDetailsResponse struct {
	RoomID           string `json:"roomId"`
	RoomName         string `json:"roomName"`
	RoomCode         string `json:"roomCode,omitempty"`
	AdminCode        string `json:"adminCode,omitempty"`
	MemberCount      int    `json:"memberCount"`
	CreatedAt        int64  `json:"createdAt"`
	ExpiresAt        int64  `json:"expiresAt,omitempty"`
	IsAdmin          bool   `json:"isAdmin,omitempty"`
	RequiresPassword bool   `json:"requiresPassword,omitempty"`
	ServerNow        int64  `json:"serverNow,omitempty"`
}

type RemoveRoomMemberRequest struct {
	RoomID       string `json:"roomId"`
	ActorUserID  string `json:"actorUserId"`
	TargetUserID string `json:"targetUserId"`
}

type DeleteRoomRequest struct {
	RoomID      string `json:"roomId"`
	ActorUserID string `json:"actorUserId"`
}

type RoomAdminActionResponse struct {
	RoomID        string `json:"roomId"`
	RemovedUserID string `json:"removedUserId,omitempty"`
	Message       string `json:"message"`
	ServerNow     int64  `json:"serverNow,omitempty"`
}

type PromoteToAdminRequest struct {
	Code   string `json:"code"`
	UserID string `json:"userId,omitempty"`
}

type PromoteToAdminResponse struct {
	RoomID    string `json:"roomId"`
	IsAdmin   bool   `json:"isAdmin"`
	AdminCode string `json:"adminCode,omitempty"`
	Token     string `json:"token,omitempty"`
	ServerNow int64  `json:"serverNow,omitempty"`
}

func (h *RoomHandler) JoinRoom(w http.ResponseWriter, r *http.Request) {
	clientIP := extractClientIP(r)
	if !JoinRoomLimiter.Allow(clientIP) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]string{"error": "Join room rate limit exceeded"})
		return
	}

	var req JoinRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	roomType := strings.TrimSpace(req.Type)
	if roomType == "" {
		roomType = "ephemeral"
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "create"
	}
	if mode != "create" && mode != "join" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "mode must be 'create' or 'join'"})
		return
	}
	initialRoomTTL, ttlErr := resolveInitialRoomTTL(req.RoomDurationHours)
	if ttlErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": ttlErr.Error()})
		return
	}
	if mode != "create" {
		initialRoomTTL = roomDefaultTTL
	}

	ctx := context.Background()
	normalizedRoomCode := normalizeRoomCode(req.RoomCode)
	requestedRoomName := normalizeRoomName(req.RoomName)
	requestedRoomID := normalizeRoomID(req.RoomID)

	if mode == "create" {
		if requestedRoomName == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Room name is required"})
			return
		}
	} else {
		if requestedRoomID == "" && requestedRoomName == "" && normalizedRoomCode == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Provide room name or 6-digit room code"})
			return
		}
	}

	rng := mrand.New(mrand.NewSource(time.Now().UnixNano()))
	createdAt := time.Now().Unix()

	normalizedUsername := normalizeUsername(req.Username)
	if normalizedUsername == "" {
		normalizedUsername = fmt.Sprintf("Guest_%04d", rng.Intn(10000))
	}

	userID := normalizeIdentifier(req.UserID)
	if userID == "" {
		userID = fmt.Sprintf("user_%d", time.Now().UnixNano())
	}
	token := "temp_token_for_" + normalizedUsername

	finalRoomID := ""
	finalRoomName := requestedRoomName
	if mode == "join" {
		if normalizedRoomCode != "" {
			resolvedRoomID, err := h.resolveRoomIDByCode(ctx, normalizedRoomCode)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "Failed to resolve room code"})
				return
			}
			if resolvedRoomID == "" {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": "Room code not found"})
				return
			}
			finalRoomID = resolvedRoomID
		} else {
			if requestedRoomName != "" {
				resolvedRoomID, err := h.resolveRoomIDByName(ctx, requestedRoomName)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(map[string]string{"error": "Failed to resolve room name"})
					return
				}
				if resolvedRoomID != "" {
					finalRoomID = resolvedRoomID
				}
			}

			if finalRoomID == "" && requestedRoomID != "" {
				existsAsID, err := h.roomExists(ctx, requestedRoomID)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(map[string]string{"error": "Failed to access room storage"})
					return
				}
				if existsAsID {
					finalRoomID = requestedRoomID
				}
			}

			// Backward compatibility for clients that still send room names in roomId.
			if finalRoomID == "" && requestedRoomID != "" {
				resolvedLegacyRoomID, err := h.resolveRoomIDByName(ctx, requestedRoomID)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(map[string]string{"error": "Failed to resolve room name"})
					return
				}
				if resolvedLegacyRoomID != "" {
					finalRoomID = resolvedLegacyRoomID
					if finalRoomName == "" {
						finalRoomName = requestedRoomID
					}
				}
			}
		}
		if finalRoomID == "" {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "Room not found"})
			return
		}

		exists, err := h.roomExists(ctx, finalRoomID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to access room storage"})
			return
		}
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "Room not found"})
			return
		}

		name, err := h.getRoomName(ctx, finalRoomID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to read room data"})
			return
		}
		if normalized := normalizeRoomName(name); normalized != "" {
			finalRoomName = normalized
		} else {
			finalRoomName = requestedRoomName
			if finalRoomName == "" {
				finalRoomName = "Room"
			}
		}
	} else {
		// "New" must always create a room. If the requested root name exists,
		// generate alternates before falling back to a 3-digit suffix.
		resolvedCreateName, resolveErr := h.resolveCreateRoomName(ctx, requestedRoomName)
		if resolveErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to resolve room name availability"})
			return
		}
		finalRoomName = resolvedCreateName

		nextRoomID, err := h.allocateUniqueRoomID(ctx)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to allocate room id"})
			return
		}
		finalRoomID = nextRoomID

		created, err := h.tryCreateRoom(ctx, finalRoomID, finalRoomName, roomType, createdAt, "", "", initialRoomTTL, "")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to access room storage"})
			return
		}
		if !created {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create room, retry"})
			return
		}
	}
	requiresPassword, err := h.isRoomPasswordProtected(ctx, finalRoomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room access settings"})
		return
	}
	if mode == "join" && requiresPassword {
		isMember, memberErr := h.redis.Client.SIsMember(ctx, roomMembersKey(finalRoomID), userID).Result()
		if memberErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room membership"})
			return
		}
		if !isMember {
			storedPasswordHash, hashErr := h.getRoomPasswordHash(ctx, finalRoomID)
			if hashErr != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room access settings"})
				return
			}
			if !h.verifyRoomPassword(req.RoomPassword, storedPasswordHash) {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error":            "Room password is required",
					"requiresPassword": true,
				})
				return
			}
		}
	}

	roomCode, err := h.ensureRoomCode(ctx, finalRoomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to resolve room code"})
		return
	}
	memberCount, err := h.registerRoomMembership(ctx, finalRoomID, userID)
	if err != nil {
		if errors.Is(err, errRoomFull) {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "Room Full"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to join room"})
		return
	}
	if mode == "create" {
		if err := h.grantRoomAdmin(ctx, finalRoomID, userID); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to initialize room admin"})
			return
		}
	}
	syncBreakErr := h.syncBreakJoinCount(ctx, finalRoomID, memberCount)
	if syncBreakErr != nil {
		log.Printf("[room] break join count sync failed room=%s err=%v", finalRoomID, syncBreakErr)
	}

	finalCreatedAt, err := h.getRoomCreatedAt(ctx, finalRoomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to resolve room created time"})
		return
	}
	if finalCreatedAt <= 0 {
		finalCreatedAt = createdAt
	}
	indexErr := h.indexRoomName(ctx, finalRoomID, finalRoomName, finalCreatedAt)
	if indexErr != nil {
		log.Printf("[room] join name-index sync failed room=%s err=%v", finalRoomID, indexErr)
	}
	expiresAt := h.getRoomExpiryUnix(ctx, finalRoomID)
	isAdmin, adminErr := h.isRoomAdmin(ctx, finalRoomID, userID)
	if adminErr != nil {
		log.Printf("[room] admin resolve failed room=%s user=%s err=%v", finalRoomID, userID, adminErr)
	}
	adminCode := ""
	if isAdmin {
		resolvedAdminCode, codeErr := h.ensureRoomAdminCode(ctx, finalRoomID)
		if codeErr != nil {
			log.Printf("[room] admin code resolve failed room=%s user=%s err=%v", finalRoomID, userID, codeErr)
		} else {
			adminCode = resolvedAdminCode
		}
	}

	response := JoinRoomResponse{
		RoomID:           finalRoomID,
		RoomName:         finalRoomName,
		RoomCode:         roomCode,
		AdminCode:        adminCode,
		UserID:           userID,
		Token:            token,
		CreatedAt:        finalCreatedAt,
		ExpiresAt:        expiresAt,
		IsAdmin:          isAdmin,
		RequiresPassword: requiresPassword,
		ServerNow:        time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (h *RoomHandler) CreateBreakRoom(w http.ResponseWriter, r *http.Request) {
	clientIP := extractClientIP(r)
	if !roomCreateLimiter.Allow(clientIP) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Create room rate limit exceeded"})
		return
	}

	var req CreateBreakRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	parentRoomID := normalizeRoomID(req.ParentRoomID)
	originMessageID := strings.TrimSpace(req.OriginMessageID)
	if parentRoomID == "" || originMessageID == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "parentRoomId and originMessageId are required"})
		return
	}

	ctx := context.Background()
	exists, err := h.roomExists(ctx, parentRoomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to access room storage"})
		return
	}
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Parent room not found"})
		return
	}

	rootRoomID, err := h.resolveRootRoomID(ctx, parentRoomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to resolve room tree"})
		return
	}
	descendantCount, err := h.countDescendants(ctx, rootRoomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to evaluate child room limit"})
		return
	}
	if descendantCount >= roomMaxDescendants {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "Root room has reached the child limit (6)"})
		return
	}

	sourceMessageText, err := h.resolveSourceMessageText(ctx, parentRoomID, originMessageID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to read source message"})
		return
	}
	sourceMessageText = strings.TrimSpace(sourceMessageText)
	if sourceMessageText == "" {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Source message text not found"})
		return
	}
	branchRoomName := deriveBranchRoomName(sourceMessageText)
	if branchRoomName == "" {
		branchRoomName = "Branch"
	}

	createdAt := time.Now().Unix()
	normalizedBreakRoomPassword := normalizeRoomPassword(req.RoomPassword)
	breakRoomPasswordHash := hashRoomPassword(normalizedBreakRoomPassword)
	roomType, err := h.getRoomType(ctx, parentRoomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to read parent room type"})
		return
	}
	if roomType == "" {
		roomType = "ephemeral"
	}

	finalRoomID, err := h.allocateUniqueRoomID(ctx)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to allocate break room id"})
		return
	}
	created, err := h.tryCreateRoom(
		ctx,
		finalRoomID,
		branchRoomName,
		roomType,
		createdAt,
		parentRoomID,
		originMessageID,
		roomDefaultTTL,
		breakRoomPasswordHash,
	)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create break room"})
		return
	}
	if !created {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create break room, retry"})
		return
	}
	finalRoomName := branchRoomName
	requiresPassword := breakRoomPasswordHash != ""

	if err := h.redis.Client.SAdd(ctx, roomChildrenKey(parentRoomID), finalRoomID).Err(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to link break room"})
		return
	}
	_ = h.redis.Client.Expire(ctx, roomChildrenKey(parentRoomID), h.effectiveRoomTTL(ctx, parentRoomID)).Err()

	creatorID := normalizeIdentifier(req.UserID)
	if creatorID == "" {
		creatorID = fmt.Sprintf("user_%d", time.Now().UnixNano())
	}
	memberCount, err := h.registerRoomMembership(ctx, finalRoomID, creatorID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to attach creator to break room"})
		return
	}
	if err := h.grantRoomAdmin(ctx, finalRoomID, creatorID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to initialize break room admin"})
		return
	}

	if err := h.redis.Client.HSet(ctx, messageBreakKey(originMessageID), map[string]interface{}{
		"has_break_room":   1,
		"break_room_id":    finalRoomID,
		"break_join_count": memberCount,
		"updated_at":       time.Now().Unix(),
	}).Err(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to update message break metadata"})
		return
	}

	if err := h.updateBreakMetadataInCachedHistory(ctx, parentRoomID, originMessageID, finalRoomID, memberCount); err != nil {
		log.Printf("[room] cached break update failed parent=%s origin=%s err=%v", parentRoomID, originMessageID, err)
	}
	h.tryUpdateBreakMetadataInScylla(parentRoomID, originMessageID, finalRoomID, memberCount)

	expiresAt := h.getRoomExpiryUnix(ctx, finalRoomID)
	h.broadcastBreakMetadataUpdate(
		parentRoomID,
		originMessageID,
		finalRoomID,
		finalRoomName,
		memberCount,
		createdAt,
		expiresAt,
		requiresPassword,
	)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(CreateBreakRoomResponse{
		RoomID:           finalRoomID,
		RoomName:         finalRoomName,
		ParentRoomID:     parentRoomID,
		OriginMessageID:  originMessageID,
		CreatedAt:        createdAt,
		ExpiresAt:        expiresAt,
		RequiresPassword: requiresPassword,
		ServerNow:        time.Now().Unix(),
	})
}

func (h *RoomHandler) GetSidebarRooms(w http.ResponseWriter, r *http.Request) {
	userID := normalizeIdentifier(r.URL.Query().Get("userId"))
	if userID == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "userId is required"})
		return
	}

	ctx := context.Background()
	joinedRoomIDsRaw, err := h.redis.Client.SMembers(ctx, userRoomsKey(userID)).Result()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load user rooms"})
		return
	}
	hiddenRoomIDsRaw, hiddenErr := h.redis.Client.SMembers(ctx, userHiddenRoomsKey(userID)).Result()
	if hiddenErr != nil && hiddenErr != redis.Nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load hidden rooms"})
		return
	}
	hiddenRoomSet := make(map[string]struct{}, len(hiddenRoomIDsRaw))
	for _, hiddenRawID := range hiddenRoomIDsRaw {
		hiddenRoomID := normalizeRoomID(hiddenRawID)
		if hiddenRoomID == "" {
			continue
		}
		hiddenRoomSet[hiddenRoomID] = struct{}{}
	}

	membershipCache := make(map[string]bool, len(joinedRoomIDsRaw))
	joinedRoomIDs := make([]string, 0, len(joinedRoomIDsRaw))
	joinedSet := make(map[string]struct{}, len(joinedRoomIDsRaw))
	for _, rawRoomID := range joinedRoomIDsRaw {
		roomID := normalizeRoomID(rawRoomID)
		if roomID == "" {
			continue
		}
		if _, hidden := hiddenRoomSet[roomID]; hidden {
			_ = h.redis.Client.SRem(ctx, userRoomsKey(userID), roomID).Err()
			continue
		}
		if _, exists := joinedSet[roomID]; exists {
			continue
		}
		joinedSet[roomID] = struct{}{}
		membershipCache[roomID] = true
		joinedRoomIDs = append(joinedRoomIDs, roomID)
	}

	roomsMap := make(map[string]SidebarRoom)
	visited := make(map[string]struct{}, len(joinedRoomIDs))

	// Always include rooms the user is already a member of.
	for _, roomID := range joinedRoomIDs {
		if roomID == "" {
			continue
		}
		if _, seen := visited[roomID]; seen {
			continue
		}
		visited[roomID] = struct{}{}

		room, ok, err := h.loadSidebarRoom(ctx, roomID, "joined")
		if err != nil {
			continue
		}
		if ok {
			roomsMap[roomID] = room
		}
	}

	// Include descendants recursively so tree navigation can drill into child rooms at any depth.
	queue := append([]string(nil), joinedRoomIDs...)
	expandedParents := make(map[string]struct{}, len(joinedRoomIDs))
	for len(queue) > 0 {
		parentRoomID := normalizeRoomID(queue[0])
		queue = queue[1:]
		if parentRoomID == "" {
			continue
		}
		if _, expanded := expandedParents[parentRoomID]; expanded {
			continue
		}
		expandedParents[parentRoomID] = struct{}{}

		children, childrenErr := h.redis.Client.SMembers(ctx, roomChildrenKey(parentRoomID)).Result()
		if childrenErr != nil {
			continue
		}
		for _, childRawID := range children {
			childRoomID := normalizeRoomID(childRawID)
			if childRoomID == "" {
				continue
			}
			if _, hidden := hiddenRoomSet[childRoomID]; hidden {
				continue
			}

			exists, existsErr := h.roomExists(ctx, childRoomID)
			if existsErr != nil {
				continue
			}
			if !exists {
				_ = h.redis.Client.SRem(ctx, roomChildrenKey(parentRoomID), childRawID).Err()
				continue
			}

			isJoined, known := membershipCache[childRoomID]
			if !known {
				isMember, memberErr := h.redis.Client.SIsMember(ctx, roomMembersKey(childRoomID), userID).Result()
				if memberErr == nil {
					membershipCache[childRoomID] = isMember
					isJoined = isMember
				}
			}
			status := "discoverable"
			if isJoined {
				status = "joined"
			}

			room, ok, err := h.loadSidebarRoom(ctx, childRoomID, status)
			if err != nil {
				continue
			}
			if ok {
				roomsMap[childRoomID] = room
				visited[childRoomID] = struct{}{}
				queue = append(queue, childRoomID)
			}
		}
	}

	// If the user left a parent room but still has visible descendants, include the hidden parent as
	// a "left" node so tree/list navigation remains contextual without restoring room membership.
	parentByRoomID := make(map[string]string, len(roomsMap))
	for mappedRoomID, room := range roomsMap {
		parentByRoomID[mappedRoomID] = normalizeRoomID(room.ParentRoomID)
	}
	parentLookupCache := make(map[string]string)
	resolveParentRoomID := func(targetRoomID string) string {
		targetRoomID = normalizeRoomID(targetRoomID)
		if targetRoomID == "" {
			return ""
		}
		if cachedParent, ok := parentByRoomID[targetRoomID]; ok {
			return cachedParent
		}
		if cachedParent, ok := parentLookupCache[targetRoomID]; ok {
			return cachedParent
		}
		rawParentID, parentErr := h.redis.Client.HGet(ctx, roomKey(targetRoomID), "parent_room_id").Result()
		if parentErr != nil {
			if parentErr != redis.Nil {
				log.Printf("[room] sidebar parent lookup failed room=%s err=%v", targetRoomID, parentErr)
			}
			parentLookupCache[targetRoomID] = ""
			return ""
		}
		parentRoomID := normalizeRoomID(rawParentID)
		parentLookupCache[targetRoomID] = parentRoomID
		return parentRoomID
	}

	hiddenAncestorRoomIDs := make(map[string]struct{})
	for _, room := range roomsMap {
		ancestorRoomID := normalizeRoomID(room.ParentRoomID)
		seenAncestors := make(map[string]struct{})
		for ancestorRoomID != "" {
			if _, seen := seenAncestors[ancestorRoomID]; seen {
				break
			}
			seenAncestors[ancestorRoomID] = struct{}{}

			if _, hidden := hiddenRoomSet[ancestorRoomID]; hidden {
				hiddenAncestorRoomIDs[ancestorRoomID] = struct{}{}
			}

			nextAncestorRoomID := resolveParentRoomID(ancestorRoomID)
			if nextAncestorRoomID == ancestorRoomID {
				break
			}
			ancestorRoomID = nextAncestorRoomID
		}
	}

	for hiddenRoomID := range hiddenAncestorRoomIDs {
		if _, alreadyAdded := roomsMap[hiddenRoomID]; alreadyAdded {
			continue
		}
		room, ok, loadErr := h.loadSidebarRoom(ctx, hiddenRoomID, "left")
		if loadErr != nil {
			continue
		}
		if !ok {
			continue
		}
		roomsMap[hiddenRoomID] = room
		parentByRoomID[hiddenRoomID] = normalizeRoomID(room.ParentRoomID)
	}

	h.assignTreeNumbers(ctx, userID, roomsMap)

	rooms := make([]SidebarRoom, 0, len(roomsMap))
	for _, room := range roomsMap {
		rooms = append(rooms, room)
	}
	sort.SliceStable(rooms, func(i, j int) bool {
		if rooms[i].CreatedAt == rooms[j].CreatedAt {
			return rooms[i].RoomName < rooms[j].RoomName
		}
		return rooms[i].CreatedAt > rooms[j].CreatedAt
	})

	for idx := range rooms {
		isAdmin, adminErr := h.isRoomAdmin(ctx, rooms[idx].RoomID, userID)
		if adminErr != nil {
			log.Printf("[room] sidebar admin resolve failed room=%s user=%s err=%v", rooms[idx].RoomID, userID, adminErr)
			continue
		}
		rooms[idx].IsAdmin = isAdmin
		if isAdmin {
			adminCode, codeErr := h.ensureRoomAdminCode(ctx, rooms[idx].RoomID)
			if codeErr != nil {
				log.Printf("[room] sidebar admin code resolve failed room=%s user=%s err=%v", rooms[idx].RoomID, userID, codeErr)
				continue
			}
			rooms[idx].AdminCode = adminCode
		} else {
			rooms[idx].AdminCode = ""
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(SidebarRoomsResponse{
		Rooms:     rooms,
		ServerNow: time.Now().Unix(),
	})
}

func (h *RoomHandler) GetRoom(w http.ResponseWriter, r *http.Request) {
	roomID := normalizeRoomID(chi.URLParam(r, "id"))
	if roomID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "room id is required"})
		return
	}

	ctx := context.Background()
	meta, err := h.redis.Client.HGetAll(ctx, roomKey(roomID)).Result()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to read room metadata"})
		return
	}
	if len(meta) == 0 {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Room not found"})
		return
	}

	roomName := normalizeRoomName(meta["name"])
	if roomName == "" {
		roomName = "Room"
	}
	createdAt, _ := strconv.ParseInt(strings.TrimSpace(meta["created_at"]), 10, 64)
	memberCount64, _ := strconv.ParseInt(strings.TrimSpace(meta["member_count"]), 10, 64)
	expiresAt := h.getRoomExpiryUnix(ctx, roomID)
	requiresPassword := normalizeRoomPasswordHash(meta["room_password_hash"]) != ""
	roomCode, codeErr := h.ensureRoomCode(ctx, roomID)
	if codeErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to resolve room code"})
		return
	}

	userID := normalizeIdentifier(r.URL.Query().Get("userId"))
	isAdmin := false
	adminCode := ""
	if userID != "" {
		resolvedIsAdmin, adminErr := h.isRoomAdmin(ctx, roomID, userID)
		if adminErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room admin"})
			return
		}
		isAdmin = resolvedIsAdmin
	}
	if isAdmin {
		resolvedAdminCode, adminCodeErr := h.ensureRoomAdminCode(ctx, roomID)
		if adminCodeErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to resolve room admin code"})
			return
		}
		adminCode = resolvedAdminCode
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(RoomDetailsResponse{
		RoomID:           roomID,
		RoomName:         roomName,
		RoomCode:         roomCode,
		AdminCode:        adminCode,
		MemberCount:      int(memberCount64),
		CreatedAt:        createdAt,
		ExpiresAt:        expiresAt,
		IsAdmin:          isAdmin,
		RequiresPassword: requiresPassword,
		ServerNow:        time.Now().Unix(),
	})
}

func (h *RoomHandler) PromoteToAdmin(w http.ResponseWriter, r *http.Request) {
	roomID := normalizeRoomID(chi.URLParam(r, "id"))
	if roomID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "room id is required"})
		return
	}

	var req PromoteToAdminRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	userID := normalizeIdentifier(req.UserID)
	if userID == "" {
		userID = normalizeIdentifier(r.URL.Query().Get("userId"))
	}
	if userID == "" {
		userID = normalizeIdentifier(r.Header.Get("X-User-Id"))
	}
	if userID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "userId is required"})
		return
	}

	ctx := context.Background()
	exists, err := h.roomExists(ctx, roomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to access room storage"})
		return
	}
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Room not found"})
		return
	}

	isMember, err := h.redis.Client.SIsMember(ctx, roomMembersKey(roomID), userID).Result()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room membership"})
		return
	}
	if !isMember {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Join the room before requesting admin access"})
		return
	}

	resolvedAdminCode, err := h.ensureRoomAdminCode(ctx, roomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to resolve room admin code"})
		return
	}
	expectedCode := normalizeRoomAdminCode(resolvedAdminCode)
	submittedCode := normalizeRoomAdminCode(strings.ToUpper(strings.TrimSpace(req.Code)))
	if expectedCode == "" || submittedCode == "" || subtle.ConstantTimeCompare([]byte(expectedCode), []byte(submittedCode)) != 1 {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid admin code"})
		return
	}

	if err := h.grantRoomAdmin(ctx, roomID, userID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to grant admin access"})
		return
	}

	updatedToken := ""
	if token, tokenErr := newToken(); tokenErr == nil {
		updatedToken = token
	} else {
		log.Printf("[room] admin promote token generation failed room=%s user=%s err=%v", roomID, userID, tokenErr)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(PromoteToAdminResponse{
		RoomID:    roomID,
		IsAdmin:   true,
		AdminCode: expectedCode,
		Token:     updatedToken,
		ServerNow: time.Now().Unix(),
	})
}

func (h *RoomHandler) ExtendRoom(w http.ResponseWriter, r *http.Request) {
	clientIP := extractClientIP(r)
	if !ExtendRoomLimiter.Allow(clientIP) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]string{"error": "Extend room rate limit exceeded"})
		return
	}

	var req ExtendRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}
	roomID := normalizeRoomID(req.RoomID)
	if roomID == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "roomId is required"})
		return
	}

	ctx := context.Background()
	roomRedisKey := roomKey(roomID)

	exists, err := h.redis.Client.Exists(ctx, roomRedisKey).Result()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to access room storage"})
		return
	}
	if exists == 0 {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Room not found"})
		return
	}

	createdAtRaw, err := h.redis.Client.HGet(ctx, roomRedisKey, "created_at").Result()
	if err == redis.Nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Room metadata is incomplete"})
		return
	}
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to read room metadata"})
		return
	}

	createdAtUnix, err := strconv.ParseInt(createdAtRaw, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Room metadata is invalid"})
		return
	}

	createdAt := time.Unix(createdAtUnix, 0)
	maxExpiry := createdAt.Add(roomMaxExtendAge)
	maxRemaining := time.Until(maxExpiry)
	if maxRemaining <= 0 {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "Room has reached its 15-day limit"})
		return
	}

	currentTTL, err := h.redis.Client.TTL(ctx, roomRedisKey).Result()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to read room expiry"})
		return
	}
	if currentTTL < 0 {
		currentTTL = 0
	}

	nextTTL := currentTTL + roomExtendedTTL
	if nextTTL > maxRemaining {
		nextTTL = maxRemaining
	}
	if nextTTL <= currentTTL {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "Room has reached its 15-day limit"})
		return
	}
	if nextTTL < time.Second {
		nextTTL = time.Second
	}

	if err := h.redis.Client.Expire(ctx, roomRedisKey, nextTTL).Err(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to extend room"})
		return
	}
	if roomCode, codeErr := h.ensureRoomCode(ctx, roomID); codeErr == nil && roomCode != "" {
		_ = h.redis.Client.Set(ctx, roomCodeKey(roomCode), roomID, nextTTL).Err()
	}
	_ = h.redis.Client.Expire(ctx, roomMembersKey(roomID), nextTTL).Err()
	_ = h.redis.Client.Expire(ctx, roomChildrenKey(roomID), nextTTL).Err()
	_ = h.redis.Client.Expire(ctx, roomHistoryKey(roomID), nextTTL).Err()

	if err := h.refreshRoomMessageTTL(ctx, roomID, nextTTL); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to extend room messages"})
		return
	}

	expiresAt := time.Now().Add(nextTTL).Unix()
	responseMessage := "Room extended for 24 hours"
	if nextTTL < currentTTL+roomExtendedTTL {
		responseMessage = "Room extended to its 15-day limit"
	}

	response := ExtendRoomResponse{
		RoomID:           roomID,
		ExpiresInSeconds: int64(nextTTL.Seconds()),
		ExpiresAt:        expiresAt,
		Message:          responseMessage,
		ServerNow:        time.Now().Unix(),
	}
	h.broadcastRoomEvent(roomID, "room_extended", map[string]interface{}{
		"expiresAt":        response.ExpiresAt,
		"expiresInSeconds": response.ExpiresInSeconds,
		"serverNow":        response.ServerNow,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (h *RoomHandler) LeaveRoom(w http.ResponseWriter, r *http.Request) {
	clientIP := extractClientIP(r)
	if !JoinRoomLimiter.Allow(clientIP) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]string{"error": "Leave room rate limit exceeded"})
		return
	}

	var req LeaveRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	roomID := normalizeRoomID(req.RoomID)
	userID := normalizeIdentifier(req.UserID)
	if roomID == "" || userID == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "roomId and userId are required"})
		return
	}

	ctx := context.Background()
	exists, err := h.roomExists(ctx, roomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to access room storage"})
		return
	}
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Room not found"})
		return
	}

	membersKey := roomMembersKey(roomID)
	removedCount, err := h.redis.Client.SRem(ctx, membersKey, userID).Result()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to update room membership"})
		return
	}
	_ = h.redis.Client.SRem(ctx, userRoomsKey(userID), roomID).Err()
	_ = h.redis.Client.HDel(ctx, roomMemberJoinedAtKey(roomID), userID).Err()
	_ = h.redis.Client.SRem(ctx, roomAdminsKey(roomID), userID).Err()
	if err := h.redis.Client.SAdd(ctx, userHiddenRoomsKey(userID), roomID).Err(); err != nil {
		log.Printf("[room] leave hidden-room set failed user=%s room=%s err=%v", userID, roomID, err)
	}
	_ = h.redis.Client.Expire(ctx, userHiddenRoomsKey(userID), roomMaxExtendAge).Err()

	memberCount, err := h.redis.Client.SCard(ctx, membersKey).Result()
	if err == nil {
		_ = h.redis.Client.HSet(ctx, roomKey(roomID), "member_count", memberCount).Err()
		if syncErr := h.syncBreakJoinCount(ctx, roomID, int(memberCount)); syncErr != nil {
			log.Printf("[room] leave break-join sync failed room=%s err=%v", roomID, syncErr)
		}
	}

	responseMessage := "Room left"
	if removedCount == 0 {
		responseMessage = "Not a room member"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(LeaveRoomResponse{
		RoomID:    roomID,
		UserID:    userID,
		Message:   responseMessage,
		ServerNow: time.Now().Unix(),
	})
}

func (h *RoomHandler) RemoveRoomMember(w http.ResponseWriter, r *http.Request) {
	var req RemoveRoomMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	roomID := normalizeRoomID(req.RoomID)
	actorUserID := normalizeIdentifier(req.ActorUserID)
	targetUserID := normalizeIdentifier(req.TargetUserID)
	if roomID == "" || actorUserID == "" || targetUserID == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "roomId, actorUserId and targetUserId are required"})
		return
	}
	if actorUserID == targetUserID {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Admin cannot remove self"})
		return
	}

	ctx := context.Background()
	exists, err := h.roomExists(ctx, roomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to access room storage"})
		return
	}
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Room not found"})
		return
	}

	isAdmin, err := h.isRoomAdmin(ctx, roomID, actorUserID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room admin"})
		return
	}
	if !isAdmin {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "Only room admin can remove members"})
		return
	}

	removedCount, err := h.redis.Client.SRem(ctx, roomMembersKey(roomID), targetUserID).Result()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to update room members"})
		return
	}
	_ = h.redis.Client.SRem(ctx, userRoomsKey(targetUserID), roomID).Err()
	_ = h.redis.Client.HDel(ctx, roomMemberJoinedAtKey(roomID), targetUserID).Err()
	_ = h.redis.Client.SRem(ctx, roomAdminsKey(roomID), targetUserID).Err()
	_ = h.redis.Client.SAdd(ctx, userHiddenRoomsKey(targetUserID), roomID).Err()
	_ = h.redis.Client.Expire(ctx, userHiddenRoomsKey(targetUserID), roomMaxExtendAge).Err()

	memberCount, err := h.redis.Client.SCard(ctx, roomMembersKey(roomID)).Result()
	if err == nil {
		_ = h.redis.Client.HSet(ctx, roomKey(roomID), "member_count", memberCount).Err()
		if syncErr := h.syncBreakJoinCount(ctx, roomID, int(memberCount)); syncErr != nil {
			log.Printf("[room] remove-member break-join sync failed room=%s err=%v", roomID, syncErr)
		}
	}

	if removedCount == 0 {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Target user is not a room member"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(RoomAdminActionResponse{
		RoomID:        roomID,
		RemovedUserID: targetUserID,
		Message:       "Member removed",
		ServerNow:     time.Now().Unix(),
	})
	h.broadcastRoomEvent(roomID, "member_removed", map[string]interface{}{
		"targetUserId": targetUserID,
		"memberCount":  memberCount,
		"serverNow":    time.Now().Unix(),
	})
}

func (h *RoomHandler) DeleteRoom(w http.ResponseWriter, r *http.Request) {
	var req DeleteRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	roomID := normalizeRoomID(req.RoomID)
	actorUserID := normalizeIdentifier(req.ActorUserID)
	if roomID == "" || actorUserID == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "roomId and actorUserId are required"})
		return
	}

	ctx := context.Background()
	exists, err := h.roomExists(ctx, roomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to access room storage"})
		return
	}
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Room not found"})
		return
	}

	isAdmin, err := h.isRoomAdmin(ctx, roomID, actorUserID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to verify room admin"})
		return
	}
	if !isAdmin {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "Only room admin can delete this room"})
		return
	}

	roomIDsToDelete, err := h.collectRoomSubtreeIDs(ctx, roomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to resolve room tree"})
		return
	}
	for _, deleteRoomID := range roomIDsToDelete {
		if deleteErr := h.deleteSingleRoom(ctx, deleteRoomID); deleteErr != nil {
			log.Printf("[room] delete failed room=%s err=%v", deleteRoomID, deleteErr)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to delete room"})
			return
		}
	}
	for _, deleteRoomID := range roomIDsToDelete {
		h.broadcastRoomEvent(deleteRoomID, "room_deleted", map[string]interface{}{
			"deletedRoomId": deleteRoomID,
			"rootRoomId":    roomID,
			"serverNow":     time.Now().Unix(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(RoomAdminActionResponse{
		RoomID:    roomID,
		Message:   "Room deleted",
		ServerNow: time.Now().Unix(),
	})
}

func (h *RoomHandler) RenameRoom(w http.ResponseWriter, r *http.Request) {
	var req RenameRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	roomID := normalizeRoomID(req.RoomID)
	nextName := normalizeRoomName(req.RoomName)
	if roomID == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "roomId is required"})
		return
	}
	if nextName == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "roomName is required"})
		return
	}

	ctx := context.Background()
	exists, err := h.roomExists(ctx, roomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to access room storage"})
		return
	}
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Room not found"})
		return
	}

	previousLookup := ""
	if storedLookup, lookupErr := h.redis.Client.HGet(ctx, roomKey(roomID), "name_lookup").Result(); lookupErr == nil {
		previousLookup = strings.TrimSpace(storedLookup)
	} else if lookupErr == redis.Nil {
		if existingName, nameErr := h.getRoomName(ctx, roomID); nameErr == nil {
			previousLookup = normalizeRoomNameLookup(existingName)
		}
	}
	nextLookup := normalizeRoomNameLookup(nextName)

	if err := h.redis.Client.HSet(ctx, roomKey(roomID), map[string]interface{}{
		"name":        nextName,
		"name_lookup": nextLookup,
		"updated_at":  time.Now().Unix(),
	}).Err(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to rename room"})
		return
	}
	if previousLookup != "" && previousLookup != nextLookup {
		_ = h.redis.Client.ZRem(ctx, roomNameLookupKey(previousLookup), roomID).Err()
	}
	if err := h.indexRoomName(ctx, roomID, nextName, time.Now().Unix()); err != nil {
		log.Printf("[room] rename name-index update failed room=%s err=%v", roomID, err)
	}
	h.upsertRoomRecord(ctx, roomID, nextName, "", "", "", "")
	h.broadcastRoomEvent(roomID, "room_renamed", map[string]interface{}{
		"roomName":  nextName,
		"serverNow": time.Now().Unix(),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(RenameRoomResponse{
		RoomID:   roomID,
		RoomName: nextName,
	})
}

func normalizeRoomID(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return ""
	}

	var builder strings.Builder
	for _, ch := range normalized {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			builder.WriteRune(ch)
		}
	}

	return builder.String()
}

func normalizeRoomName(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	var builder strings.Builder
	lastWasSpace := false
	for _, ch := range trimmed {
		if ch == '\n' || ch == '\r' || ch == '\t' {
			ch = ' '
		}
		if ch < 32 {
			continue
		}
		if ch == ' ' {
			if builder.Len() == 0 || lastWasSpace {
				continue
			}
			builder.WriteByte(' ')
			lastWasSpace = true
			continue
		}
		builder.WriteRune(ch)
		lastWasSpace = false
	}

	return truncateRunes(strings.TrimSpace(builder.String()), roomNameMaxLength)
}

func normalizeRoomNameLookup(raw string) string {
	normalized := normalizeRoomName(raw)
	if normalized == "" {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(normalized))
}

func deriveBranchRoomName(sourceText string) string {
	trimmed := strings.TrimSpace(sourceText)
	if trimmed == "" {
		return ""
	}

	if fromJSON := deriveBranchRoomNameFromJSON(trimmed); fromJSON != "" {
		return truncateRunes(fromJSON, roomNameMaxLength)
	}
	if fromURL := deriveBranchRoomNameFromURL(trimmed); fromURL != "" {
		return truncateRunes(fromURL, roomNameMaxLength)
	}

	return truncateRunes(normalizeRoomName(trimmed), roomNameMaxLength)
}

func deriveBranchRoomNameFromJSON(sourceText string) string {
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(sourceText), &payload); err != nil {
		return ""
	}
	if len(payload) == 0 {
		return ""
	}

	if _, hasTasks := payload["tasks"]; hasTasks {
		title := normalizeRoomName(
			firstNonEmptyStringValue(
				payload["title"],
				payload["name"],
				payload["content"],
				payload["text"],
			),
		)
		if title == "" || strings.EqualFold(title, "task") {
			return "Task"
		}
		return normalizeRoomName(fmt.Sprintf("Task: %s", title))
	}

	if title := normalizeRoomName(firstNonEmptyStringValue(payload["title"], payload["name"])); title != "" {
		return title
	}

	fileName := normalizeRoomName(
		firstNonEmptyStringValue(
			payload["fileName"],
			payload["file_name"],
			payload["filename"],
			payload["name"],
		),
	)
	if fileName != "" {
		return fileName
	}

	content := strings.TrimSpace(
		firstNonEmptyStringValue(
			payload["content"],
			payload["text"],
			payload["caption"],
			payload["message"],
			payload["mediaUrl"],
			payload["media_url"],
			payload["url"],
		),
	)
	if content == "" {
		return ""
	}
	if fromURL := deriveBranchRoomNameFromURL(content); fromURL != "" {
		return fromURL
	}

	return normalizeRoomName(content)
}

func deriveBranchRoomNameFromURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}
	lowered := strings.ToLower(trimmed)
	if !strings.HasPrefix(lowered, "http://") && !strings.HasPrefix(lowered, "https://") {
		return ""
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}

	fileName := strings.TrimSpace(path.Base(parsed.Path))
	if unescaped, unescapeErr := url.PathUnescape(fileName); unescapeErr == nil {
		fileName = strings.TrimSpace(unescaped)
	}
	if fileName == "." || fileName == "/" {
		fileName = ""
	}
	fileName = strings.Trim(fileName, "/")

	hostName := strings.TrimSpace(parsed.Hostname())
	if fileName == "" {
		return normalizeRoomName(hostName)
	}

	fileStem := strings.TrimSpace(strings.TrimSuffix(fileName, path.Ext(fileName)))
	if fileStem == "" {
		fileStem = fileName
	}
	fileStem = normalizeRoomName(fileStem)
	if fileStem == "" {
		fileStem = normalizeRoomName(hostName)
	}
	if fileStem == "" {
		return ""
	}

	kind := inferMediaLabelFromExtension(path.Ext(fileName))
	if kind == "" || kind == "File" {
		return fileStem
	}
	return normalizeRoomName(fmt.Sprintf("%s: %s", kind, fileStem))
}

func inferMediaLabelFromExtension(ext string) string {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg", ".heic":
		return "Image"
	case ".mp4", ".mov", ".mkv", ".webm", ".avi":
		return "Video"
	case ".mp3", ".wav", ".ogg", ".m4a", ".aac", ".flac":
		return "Audio"
	case ".pdf", ".doc", ".docx", ".txt", ".csv", ".zip", ".ppt", ".pptx", ".xls", ".xlsx":
		return "File"
	default:
		return ""
	}
}

func firstNonEmptyStringValue(values ...interface{}) string {
	for _, value := range values {
		candidate := strings.TrimSpace(toString(value))
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func truncateRunes(input string, max int) string {
	trimmed := strings.TrimSpace(input)
	if max <= 0 || trimmed == "" {
		return ""
	}
	if utf8.RuneCountInString(trimmed) <= max {
		return trimmed
	}
	runes := []rune(trimmed)
	return strings.TrimSpace(string(runes[:max]))
}

func normalizeUsername(raw string) string {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return ""
	}

	var builder strings.Builder
	prevSeparator := false
	for _, ch := range normalized {
		switch {
		case (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9'):
			builder.WriteRune(ch)
			prevSeparator = false
		case ch == ' ' || ch == '-' || ch == '_':
			if builder.Len() > 0 && !prevSeparator {
				builder.WriteByte('_')
				prevSeparator = true
			}
		}
	}

	return strings.Trim(builder.String(), "_")
}

func normalizeIdentifier(raw string) string {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return ""
	}

	var builder strings.Builder
	prevSeparator := false
	for _, ch := range normalized {
		switch {
		case (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9'):
			builder.WriteRune(ch)
			prevSeparator = false
		case ch == ' ' || ch == '-' || ch == '_':
			if builder.Len() > 0 && !prevSeparator {
				builder.WriteByte('_')
				prevSeparator = true
			}
		}
	}

	return strings.Trim(builder.String(), "_")
}

func normalizeRoomCode(raw string) string {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return ""
	}

	var builder strings.Builder
	for _, ch := range normalized {
		if ch >= '0' && ch <= '9' {
			builder.WriteRune(ch)
		}
	}

	code := builder.String()
	if len(code) != roomCodeDigits {
		return ""
	}
	return code
}

func normalizeRoomPassword(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return truncateRunes(trimmed, roomPasswordMaxLen)
}

func normalizeRoomPasswordHash(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func hashRoomPassword(password string) string {
	normalized := normalizeRoomPassword(password)
	if normalized == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func normalizeRoomAdminCode(raw string) string {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	if len(normalized) != roomAdminCodeLength {
		return ""
	}
	for _, ch := range normalized {
		if (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') {
			return ""
		}
	}
	return normalized
}

func generateRoomAdminCode() (string, error) {
	randomBytes := make([]byte, roomAdminCodeLength)
	if _, err := crand.Read(randomBytes); err != nil {
		return "", err
	}
	code := make([]byte, roomAdminCodeLength)
	for index, value := range randomBytes {
		code[index] = roomAdminCodeCharset[int(value)%len(roomAdminCodeCharset)]
	}
	return string(code), nil
}

func resolveInitialRoomTTL(requestedHours float64) (time.Duration, error) {
	if requestedHours <= 0 {
		return roomDefaultTTL, nil
	}
	safeHours := math.Round(requestedHours*10) / 10
	if !isFinite(safeHours) {
		return 0, fmt.Errorf("roomDurationHours is invalid")
	}
	if safeHours < roomMinDurationHours || safeHours > roomMaxDurationHours {
		return 0, fmt.Errorf(
			"roomDurationHours must be between %.1f and %.1f",
			roomMinDurationHours,
			roomMaxDurationHours,
		)
	}
	minutes := math.Round(safeHours * 60)
	if minutes < 1 {
		minutes = 1
	}
	return time.Duration(minutes) * time.Minute, nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func (h *RoomHandler) resolveCreateRoomName(ctx context.Context, requestedRoomName string) (string, error) {
	candidate := normalizeRoomName(requestedRoomName)
	if candidate == "" {
		candidate = "Room"
	}

	existingRootRoomID, err := h.resolveRoomIDByName(ctx, candidate)
	if err != nil {
		return "", err
	}
	if existingRootRoomID == "" {
		return candidate, nil
	}

	lastGenerated := ""
	for attempt := 0; attempt < roomNameRetryLimit; attempt++ {
		generated := normalizeRoomName(namegen.GenerateRoomName())
		if generated == "" {
			continue
		}
		lastGenerated = generated
		existsID, existsErr := h.resolveRoomIDByName(ctx, generated)
		if existsErr != nil {
			return "", existsErr
		}
		if existsID == "" {
			return generated, nil
		}
	}

	if lastGenerated == "" {
		lastGenerated = normalizeRoomName(namegen.GenerateRoomName())
	}
	if lastGenerated == "" {
		lastGenerated = "room"
	}

	base := truncateRunes(lastGenerated, roomNameMaxLength-3)
	if base == "" {
		base = "room"
	}

	for attempt := 0; attempt < 1000; attempt++ {
		fallback := normalizeRoomName(fmt.Sprintf("%s%03d", base, randomThreeDigit()))
		if fallback == "" {
			continue
		}
		existsID, existsErr := h.resolveRoomIDByName(ctx, fallback)
		if existsErr != nil {
			return "", existsErr
		}
		if existsID == "" {
			return fallback, nil
		}
	}

	return "", fmt.Errorf("failed to resolve available room name")
}

func randomThreeDigit() int {
	rng := mrand.New(mrand.NewSource(time.Now().UnixNano()))
	return rng.Intn(1000)
}

func (h *RoomHandler) allocateUniqueRoomID(ctx context.Context) (string, error) {
	for attempts := 0; attempts < 64; attempts++ {
		candidate, err := generateRoomID(roomIDLength)
		if err != nil {
			return "", err
		}
		exists, err := h.roomExists(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("failed to allocate unique room id")
}

func generateRoomID(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("room id length must be positive")
	}
	randomBytes := make([]byte, length)
	if _, err := crand.Read(randomBytes); err != nil {
		return "", err
	}

	encoded := make([]byte, length)
	for index, value := range randomBytes {
		encoded[index] = roomIDAlphabet[int(value)%len(roomIDAlphabet)]
	}
	return string(encoded), nil
}

func (h *RoomHandler) resolveSourceMessageText(ctx context.Context, roomID string, messageID string) (string, error) {
	roomID = normalizeRoomID(roomID)
	messageID = strings.TrimSpace(messageID)
	if roomID == "" || messageID == "" {
		return "", nil
	}

	if h.redis != nil && h.redis.Client != nil {
		entries, err := h.redis.Client.LRange(ctx, roomHistoryKey(roomID), 0, -1).Result()
		if err != nil && err != redis.Nil {
			return "", err
		}
		for index := len(entries) - 1; index >= 0; index-- {
			raw := entries[index]

			var message models.Message
			if err := json.Unmarshal([]byte(raw), &message); err == nil && strings.TrimSpace(message.ID) == messageID {
				if content := strings.TrimSpace(message.Content); content != "" {
					return content, nil
				}
				if mediaFallback := strings.TrimSpace(firstNonEmptyStringValue(message.FileName, message.MediaURL)); mediaFallback != "" {
					return mediaFallback, nil
				}
			}

			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				continue
			}
			if strings.TrimSpace(toString(payload["id"])) != messageID {
				continue
			}
			for _, key := range []string{"content", "text", "caption", "fileName", "file_name", "mediaUrl", "media_url"} {
				if content := strings.TrimSpace(toString(payload[key])); content != "" {
					return content, nil
				}
			}
		}
	}

	if h.scylla != nil && h.scylla.Session != nil {
		messagesTable := h.scylla.Table("messages")
		query := fmt.Sprintf(
			`SELECT content, media_url, file_name FROM %s WHERE room_id = ? AND message_id = ? LIMIT 1 ALLOW FILTERING`,
			messagesTable,
		)
		iter := h.scylla.Session.Query(query, roomID, messageID).WithContext(ctx).Iter()
		var content string
		var mediaURL string
		var fileName string
		if iter.Scan(&content, &mediaURL, &fileName) {
			_ = iter.Close()
			if decrypted, decryptErr := security.DecryptMessage(content); decryptErr == nil {
				content = decrypted
			}
			content = strings.TrimSpace(content)
			if content != "" {
				return content, nil
			}
			if mediaFallback := strings.TrimSpace(firstNonEmptyStringValue(fileName, mediaURL)); mediaFallback != "" {
				return mediaFallback, nil
			}
			return "", nil
		}
		if err := iter.Close(); err != nil {
			return "", err
		}
	}

	return "", nil
}

func (h *RoomHandler) assignTreeNumbers(ctx context.Context, userID string, roomsMap map[string]SidebarRoom) {
	if len(roomsMap) == 0 {
		return
	}

	rootByRoom := make(map[string]string, len(roomsMap))
	rootSet := make(map[string]struct{}, len(roomsMap))
	rootCache := make(map[string]string, len(roomsMap))
	for roomID := range roomsMap {
		rootID := h.resolveTreeRootID(ctx, roomID, roomsMap, rootCache)
		if rootID == "" {
			rootID = roomID
		}
		rootByRoom[roomID] = rootID
		rootSet[rootID] = struct{}{}
	}

	rootNumbers := make(map[string]int, len(rootSet))
	maxAssigned := 0
	if h.redis != nil && h.redis.Client != nil {
		stored, err := h.redis.Client.HGetAll(ctx, roomTreeNumbersKey(userID)).Result()
		if err == nil {
			for rawRoot, rawNumber := range stored {
				rootID := normalizeRoomID(rawRoot)
				if rootID == "" {
					continue
				}
				number, convErr := strconv.Atoi(rawNumber)
				if convErr != nil || number <= 0 {
					continue
				}
				rootNumbers[rootID] = number
				if number > maxAssigned {
					maxAssigned = number
				}
			}
		}
	}

	roots := make([]string, 0, len(rootSet))
	for rootID := range rootSet {
		roots = append(roots, rootID)
	}
	sort.Strings(roots)

	pendingWrites := make(map[string]interface{})
	for _, rootID := range roots {
		if _, exists := rootNumbers[rootID]; exists {
			continue
		}
		maxAssigned++
		rootNumbers[rootID] = maxAssigned
		pendingWrites[rootID] = maxAssigned
	}
	if len(pendingWrites) > 0 && h.redis != nil && h.redis.Client != nil {
		_ = h.redis.Client.HSet(ctx, roomTreeNumbersKey(userID), pendingWrites).Err()
		_ = h.redis.Client.Expire(ctx, roomTreeNumbersKey(userID), 90*24*time.Hour).Err()
	}

	for roomID, room := range roomsMap {
		rootID := rootByRoom[roomID]
		number := rootNumbers[rootID]
		if number <= 0 {
			number = 1
		}
		room.TreeNumber = number
		roomsMap[roomID] = room
	}
}

func (h *RoomHandler) resolveTreeRootID(ctx context.Context, roomID string, roomsMap map[string]SidebarRoom, rootCache map[string]string) string {
	roomID = normalizeRoomID(roomID)
	if roomID == "" {
		return ""
	}
	if cached, exists := rootCache[roomID]; exists {
		return cached
	}

	seen := make(map[string]struct{})
	trail := make([]string, 0, 8)
	cursor := roomID
	for cursor != "" {
		if cached, exists := rootCache[cursor]; exists {
			cursor = cached
			break
		}
		if _, exists := seen[cursor]; exists {
			break
		}
		seen[cursor] = struct{}{}
		trail = append(trail, cursor)

		parentID := ""
		if room, exists := roomsMap[cursor]; exists {
			parentID = normalizeRoomID(room.ParentRoomID)
		} else {
			resolvedParentID, err := h.getParentRoomID(ctx, cursor)
			if err != nil {
				parentID = ""
			} else {
				parentID = resolvedParentID
			}
		}
		if parentID == "" {
			break
		}
		cursor = parentID
	}

	rootID := cursor
	if rootID == "" && len(trail) > 0 {
		rootID = trail[len(trail)-1]
	}
	for _, traversed := range trail {
		rootCache[traversed] = rootID
	}
	return rootID
}

func (h *RoomHandler) getParentRoomID(ctx context.Context, roomID string) (string, error) {
	roomID = normalizeRoomID(roomID)
	if roomID == "" {
		return "", nil
	}
	if h == nil || h.redis == nil || h.redis.Client == nil {
		return "", nil
	}
	parentID, err := h.redis.Client.HGet(ctx, roomKey(roomID), "parent_room_id").Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return normalizeRoomID(parentID), nil
}

func (h *RoomHandler) resolveRootRoomID(ctx context.Context, roomID string) (string, error) {
	roomID = normalizeRoomID(roomID)
	if roomID == "" {
		return "", nil
	}

	seen := make(map[string]struct{})
	cursor := roomID
	for cursor != "" {
		if _, loop := seen[cursor]; loop {
			return roomID, nil
		}
		seen[cursor] = struct{}{}

		parentID, err := h.getParentRoomID(ctx, cursor)
		if err != nil {
			return "", err
		}
		if parentID == "" {
			return cursor, nil
		}
		cursor = parentID
	}

	return roomID, nil
}

func (h *RoomHandler) countDescendants(ctx context.Context, rootRoomID string) (int, error) {
	rootRoomID = normalizeRoomID(rootRoomID)
	if rootRoomID == "" || h == nil || h.redis == nil || h.redis.Client == nil {
		return 0, nil
	}

	seen := map[string]struct{}{rootRoomID: {}}
	queue := []string{rootRoomID}
	count := 0
	for len(queue) > 0 {
		parentID := queue[0]
		queue = queue[1:]

		children, err := h.redis.Client.SMembers(ctx, roomChildrenKey(parentID)).Result()
		if err != nil && err != redis.Nil {
			return count, err
		}

		for _, rawChildID := range children {
			childID := normalizeRoomID(rawChildID)
			if childID == "" {
				_ = h.redis.Client.SRem(ctx, roomChildrenKey(parentID), rawChildID).Err()
				continue
			}
			if _, exists := seen[childID]; exists {
				continue
			}

			exists, err := h.roomExists(ctx, childID)
			if err != nil {
				return count, err
			}
			if !exists {
				_ = h.redis.Client.SRem(ctx, roomChildrenKey(parentID), rawChildID).Err()
				continue
			}

			seen[childID] = struct{}{}
			count++
			queue = append(queue, childID)
		}
	}

	return count, nil
}

func (h *RoomHandler) ensureRoomSchema() {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return
	}

	roomsTable := h.scylla.Table("rooms")
	createQuery := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		room_id text PRIMARY KEY,
		name text,
		type text,
		parent_room_id text,
		origin_message_id text,
		admin_code text,
		canvas_has_data boolean,
		created_at timestamp,
		updated_at timestamp
	)`, roomsTable)
	if err := h.scylla.Session.Query(createQuery).Exec(); err != nil {
		log.Printf("[room] ensure rooms schema failed: %v", err)
		return
	}

	// Ensure upgraded nodes have the tree-link column even if the table was created earlier.
	alterQueries := []string{
		fmt.Sprintf(`ALTER TABLE %s ADD parent_room_id text`, roomsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD admin_code text`, roomsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD canvas_has_data boolean`, roomsTable),
	}
	for _, query := range alterQueries {
		if err := h.scylla.Session.Query(query).Exec(); err != nil && !isSchemaAlreadyAppliedError(err) {
			log.Printf("[room] ensure rooms schema alter failed: %v", err)
		}
	}
}

func (h *RoomHandler) ensureRoomMessageSoftExpirySchema() {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return
	}

	softExpiryTable := h.scylla.Table(roomSoftExpiryTable)
	createQuery := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		room_id text PRIMARY KEY,
		extended_expiry_time timestamp,
		updated_at timestamp
	)`, softExpiryTable)
	if err := h.scylla.Session.Query(createQuery).Exec(); err != nil {
		log.Printf("[room] ensure message soft-expiry schema failed: %v", err)
	}
}

func isSchemaAlreadyAppliedError(err error) bool {
	if err == nil {
		return false
	}
	lowered := strings.ToLower(err.Error())
	return strings.Contains(lowered, "already exists") ||
		strings.Contains(lowered, "conflicts with an existing column") ||
		strings.Contains(lowered, "duplicate")
}

func (h *RoomHandler) upsertRoomRecord(ctx context.Context, roomID, roomName, roomType, parentRoomID, originMessageID, adminCode string) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return
	}

	roomID = normalizeRoomID(roomID)
	if roomID == "" {
		return
	}
	roomName = normalizeRoomName(roomName)
	if roomName == "" {
		if cachedName, err := h.getRoomName(ctx, roomID); err == nil {
			roomName = normalizeRoomName(cachedName)
		}
		if roomName == "" {
			roomName = truncateRunes(roomID, roomNameMaxLength)
		}
	}
	roomType = strings.TrimSpace(roomType)
	if roomType == "" {
		if cachedType, err := h.getRoomType(ctx, roomID); err == nil {
			roomType = strings.TrimSpace(cachedType)
		}
		if roomType == "" {
			roomType = "ephemeral"
		}
	}
	parentRoomID = normalizeRoomID(parentRoomID)
	if parentRoomID == "" {
		if cachedParentID, err := h.getParentRoomID(ctx, roomID); err == nil {
			parentRoomID = normalizeRoomID(cachedParentID)
		}
	}
	originMessageID = strings.TrimSpace(originMessageID)
	if originMessageID == "" && h.redis != nil && h.redis.Client != nil {
		if cachedOrigin, err := h.redis.Client.HGet(ctx, roomKey(roomID), "origin_message_id").Result(); err == nil {
			originMessageID = strings.TrimSpace(cachedOrigin)
		}
	}
	adminCode = normalizeRoomAdminCode(adminCode)
	if adminCode == "" && h.redis != nil && h.redis.Client != nil {
		if cachedAdminCode, err := h.redis.Client.HGet(ctx, roomKey(roomID), "admin_code").Result(); err == nil {
			adminCode = normalizeRoomAdminCode(cachedAdminCode)
		}
	}

	createdAt := time.Now().UTC()
	if storedCreatedAt, err := h.getRoomCreatedAt(ctx, roomID); err == nil && storedCreatedAt > 0 {
		createdAt = time.Unix(storedCreatedAt, 0).UTC()
	}
	updatedAt := time.Now().UTC()

	roomsTable := h.scylla.Table("rooms")
	query := fmt.Sprintf(
		`INSERT INTO %s (room_id, name, type, parent_room_id, origin_message_id, admin_code, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?) USING TTL %d`,
		roomsTable,
		hardScyllaTTLSeconds,
	)
	if err := h.scylla.Session.Query(
		query,
		roomID,
		roomName,
		roomType,
		parentRoomID,
		originMessageID,
		adminCode,
		createdAt,
		updatedAt,
	).WithContext(ctx).Exec(); err != nil {
		log.Printf("[room] upsert scylla room failed room=%s err=%v", roomID, err)
	}
}

func (h *RoomHandler) tryCreateRoom(
	ctx context.Context,
	roomID,
	roomName,
	roomType string,
	createdAt int64,
	parentRoomID string,
	originMessageID string,
	roomTTL time.Duration,
	roomPasswordHash string,
) (bool, error) {
	exists, err := h.roomExists(ctx, roomID)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

	if err := h.createRoom(ctx, roomID, roomName, roomType, createdAt, parentRoomID, originMessageID, roomTTL, roomPasswordHash); err != nil {
		return false, err
	}

	return true, nil
}

func (h *RoomHandler) roomExists(ctx context.Context, roomID string) (bool, error) {
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return false, nil
	}
	count, err := h.redis.Client.Exists(ctx, roomKey(normalizedRoomID)).Result()
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (h *RoomHandler) effectiveRoomTTL(ctx context.Context, roomID string) time.Duration {
	normalizedRoomID := normalizeRoomID(roomID)
	if h == nil || h.redis == nil || h.redis.Client == nil || normalizedRoomID == "" {
		return roomDefaultTTL
	}

	ttl, err := h.redis.Client.TTL(ctx, roomKey(normalizedRoomID)).Result()
	if err != nil || ttl <= 0 {
		return roomDefaultTTL
	}
	return ttl
}

func (h *RoomHandler) getRoomName(ctx context.Context, roomID string) (string, error) {
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return "", nil
	}
	name, err := h.redis.Client.HGet(ctx, roomKey(normalizedRoomID), "name").Result()
	if err == redis.Nil {
		return "", nil
	}
	return name, err
}

func (h *RoomHandler) getRoomType(ctx context.Context, roomID string) (string, error) {
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return "", nil
	}
	roomType, err := h.redis.Client.HGet(ctx, roomKey(normalizedRoomID), "type").Result()
	if err == redis.Nil {
		return "", nil
	}
	return roomType, err
}

func (h *RoomHandler) getRoomPasswordHash(ctx context.Context, roomID string) (string, error) {
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return "", nil
	}
	rawHash, err := h.redis.Client.HGet(ctx, roomKey(normalizedRoomID), "room_password_hash").Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return normalizeRoomPasswordHash(rawHash), nil
}

func (h *RoomHandler) isRoomPasswordProtected(ctx context.Context, roomID string) (bool, error) {
	passwordHash, err := h.getRoomPasswordHash(ctx, roomID)
	if err != nil {
		return false, err
	}
	return passwordHash != "", nil
}

func (h *RoomHandler) verifyRoomPassword(submittedPassword string, storedHash string) bool {
	normalizedHash := normalizeRoomPasswordHash(storedHash)
	if normalizedHash == "" {
		return true
	}
	submittedHash := hashRoomPassword(submittedPassword)
	if submittedHash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(submittedHash), []byte(normalizedHash)) == 1
}

func (h *RoomHandler) getRoomCreatedAt(ctx context.Context, roomID string) (int64, error) {
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return 0, nil
	}
	raw, err := h.redis.Client.HGet(ctx, roomKey(normalizedRoomID), "created_at").Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}

	return parsed, nil
}

func (h *RoomHandler) getRoomExpiryUnix(ctx context.Context, roomID string) int64 {
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" || h == nil || h.redis == nil || h.redis.Client == nil {
		return 0
	}
	ttl, err := h.redis.Client.TTL(ctx, roomKey(normalizedRoomID)).Result()
	if err != nil || ttl <= 0 {
		return 0
	}
	return time.Now().Add(ttl).Unix()
}

func (h *RoomHandler) resolveRoomIDByCode(ctx context.Context, roomCode string) (string, error) {
	if roomCode == "" {
		return "", nil
	}

	roomID, err := h.redis.Client.Get(ctx, roomCodeKey(roomCode)).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return "", nil
	}

	exists, err := h.roomExists(ctx, normalizedRoomID)
	if err != nil {
		return "", err
	}
	if !exists {
		_ = h.redis.Client.Del(ctx, roomCodeKey(roomCode)).Err()
		return "", nil
	}

	return normalizedRoomID, nil
}

func (h *RoomHandler) resolveRoomIDByName(ctx context.Context, roomName string) (string, error) {
	nameLookup := normalizeRoomNameLookup(roomName)
	if nameLookup == "" {
		return "", nil
	}

	nameKey := roomNameLookupKey(nameLookup)
	// Name-based joins from landing/auth flows must resolve only to root rooms.
	// Branches can duplicate names and should never be selected here.
	candidateIDs, err := h.redis.Client.ZRange(ctx, nameKey, 0, 100).Result()
	if err != nil {
		return "", err
	}

	for _, rawCandidate := range candidateIDs {
		candidateID := normalizeRoomID(rawCandidate)
		if candidateID == "" {
			_ = h.redis.Client.ZRem(ctx, nameKey, rawCandidate).Err()
			continue
		}
		exists, existsErr := h.roomExists(ctx, candidateID)
		if existsErr != nil {
			return "", existsErr
		}
		if !exists {
			_ = h.redis.Client.ZRem(ctx, nameKey, rawCandidate).Err()
			continue
		}

		meta, metaErr := h.redis.Client.HMGet(ctx, roomKey(candidateID), "name_lookup", "name", "parent_room_id").Result()
		if metaErr != nil {
			return "", metaErr
		}

		candidateLookup := ""
		if len(meta) > 0 {
			candidateLookup = normalizeRoomNameLookup(toString(meta[0]))
		}
		if candidateLookup == "" && len(meta) > 1 {
			candidateLookup = normalizeRoomNameLookup(toString(meta[1]))
		}
		if candidateLookup != nameLookup {
			_ = h.redis.Client.ZRem(ctx, nameKey, rawCandidate).Err()
			continue
		}

		parentRoomID := ""
		if len(meta) > 2 {
			parentRoomID = normalizeRoomID(toString(meta[2]))
		}
		if parentRoomID != "" {
			continue
		}
		return candidateID, nil
	}

	return h.resolveRoomIDByNameFromScan(ctx, nameLookup)
}

func (h *RoomHandler) resolveRoomIDByNameFromScan(ctx context.Context, nameLookup string) (string, error) {
	var (
		cursor      uint64
		bestRoomID  string
		bestCreated int64
	)

	for {
		keys, nextCursor, err := h.redis.Client.Scan(ctx, cursor, "room:*", 200).Result()
		if err != nil {
			return "", err
		}
		for _, key := range keys {
			// Skip secondary keys like room:<id>:members / :children.
			if strings.Count(key, ":") != 1 {
				continue
			}
			roomID := normalizeRoomID(strings.TrimPrefix(key, "room:"))
			if roomID == "" {
				continue
			}

			meta, metaErr := h.redis.Client.HMGet(ctx, roomKey(roomID), "name_lookup", "name", "parent_room_id", "created_at").Result()
			if metaErr != nil {
				continue
			}

			lookup := ""
			if len(meta) > 0 {
				lookup = normalizeRoomNameLookup(toString(meta[0]))
			}
			if lookup == "" && len(meta) > 1 {
				lookup = normalizeRoomNameLookup(toString(meta[1]))
			}
			if lookup != nameLookup {
				continue
			}

			parentRoomID := ""
			if len(meta) > 2 {
				parentRoomID = normalizeRoomID(toString(meta[2]))
			}
			if parentRoomID != "" {
				continue
			}

			createdAt := int64(0)
			if len(meta) > 3 {
				createdAt, _ = strconv.ParseInt(toString(meta[3]), 10, 64)
			}
			if bestRoomID == "" {
				bestCreated = createdAt
				bestRoomID = roomID
				continue
			}
			// Prefer the earliest root room for stable user-facing joins by name.
			if createdAt > 0 {
				if bestCreated <= 0 || createdAt < bestCreated {
					bestCreated = createdAt
					bestRoomID = roomID
				}
				continue
			}
			if bestCreated <= 0 && roomID < bestRoomID {
				bestRoomID = roomID
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	if bestRoomID != "" {
		_ = h.indexRoomName(ctx, bestRoomID, nameLookup, bestCreated)
	}
	return bestRoomID, nil
}

func (h *RoomHandler) ensureRoomCode(ctx context.Context, roomID string) (string, error) {
	roomID = normalizeRoomID(roomID)
	if roomID == "" {
		return "", fmt.Errorf("room id is required")
	}
	codeTTL := h.effectiveRoomTTL(ctx, roomID)

	existing, err := h.redis.Client.HGet(ctx, roomKey(roomID), "room_code").Result()
	if err == nil {
		normalized := normalizeRoomCode(existing)
		if normalized != "" {
			_ = h.redis.Client.Set(ctx, roomCodeKey(normalized), roomID, codeTTL).Err()
			return normalized, nil
		}
	} else if err != redis.Nil {
		return "", err
	}

	rng := mrand.New(mrand.NewSource(time.Now().UnixNano()))
	for attempts := 0; attempts < 40; attempts++ {
		code := fmt.Sprintf("%0*d", roomCodeDigits, rng.Intn(1000000))
		created, err := h.redis.Client.SetNX(ctx, roomCodeKey(code), roomID, codeTTL).Result()
		if err != nil {
			return "", err
		}
		if !created {
			continue
		}

		if err := h.redis.Client.HSet(ctx, roomKey(roomID), "room_code", code).Err(); err != nil {
			_ = h.redis.Client.Del(ctx, roomCodeKey(code)).Err()
			return "", err
		}
		return code, nil
	}

	return "", fmt.Errorf("failed to allocate room code")
}

func (h *RoomHandler) ensureRoomAdminCode(ctx context.Context, roomID string) (string, error) {
	roomID = normalizeRoomID(roomID)
	if roomID == "" {
		return "", fmt.Errorf("room id is required")
	}

	if h == nil || h.redis == nil || h.redis.Client == nil {
		return "", fmt.Errorf("redis is not configured")
	}

	existing, err := h.redis.Client.HGet(ctx, roomKey(roomID), "admin_code").Result()
	if err == nil {
		if normalized := normalizeRoomAdminCode(existing); normalized != "" {
			if normalized != existing {
				_ = h.redis.Client.HSet(ctx, roomKey(roomID), "admin_code", normalized).Err()
			}
			return normalized, nil
		}
	} else if err != redis.Nil {
		return "", err
	}

	if h.scylla != nil && h.scylla.Session != nil {
		roomsTable := h.scylla.Table("rooms")
		query := fmt.Sprintf(`SELECT admin_code FROM %s WHERE room_id = ? LIMIT 1`, roomsTable)
		var scyllaAdminCode string
		scanErr := h.scylla.Session.Query(query, roomID).WithContext(ctx).Scan(&scyllaAdminCode)
		if scanErr == nil {
			if normalized := normalizeRoomAdminCode(scyllaAdminCode); normalized != "" {
				if err := h.redis.Client.HSet(ctx, roomKey(roomID), "admin_code", normalized).Err(); err != nil {
					return "", err
				}
				return normalized, nil
			}
		} else if !errors.Is(scanErr, gocql.ErrNotFound) {
			log.Printf("[room] admin code scylla lookup failed room=%s err=%v", roomID, scanErr)
		}
	}

	generated, err := generateRoomAdminCode()
	if err != nil {
		return "", err
	}
	if err := h.redis.Client.HSet(ctx, roomKey(roomID), "admin_code", generated).Err(); err != nil {
		return "", err
	}
	h.syncRoomAdminCodeToScylla(ctx, roomID, generated)
	return generated, nil
}

func (h *RoomHandler) syncRoomAdminCodeToScylla(ctx context.Context, roomID string, adminCode string) {
	normalizedRoomID := normalizeRoomID(roomID)
	normalizedAdminCode := normalizeRoomAdminCode(adminCode)
	if normalizedRoomID == "" || normalizedAdminCode == "" {
		return
	}
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return
	}

	roomsTable := h.scylla.Table("rooms")
	query := fmt.Sprintf(
		`UPDATE %s USING TTL %d SET admin_code = ?, updated_at = ? WHERE room_id = ?`,
		roomsTable,
		hardScyllaTTLSeconds,
	)
	if err := h.scylla.Session.Query(
		query,
		normalizedAdminCode,
		time.Now().UTC(),
		normalizedRoomID,
	).WithContext(ctx).Exec(); err != nil {
		log.Printf("[room] sync scylla admin code failed room=%s err=%v", normalizedRoomID, err)
	}
}

func (h *RoomHandler) grantRoomAdmin(ctx context.Context, roomID string, userID string) error {
	normalizedRoomID := normalizeRoomID(roomID)
	normalizedUserID := normalizeIdentifier(userID)
	if normalizedRoomID == "" || normalizedUserID == "" {
		return fmt.Errorf("room and user are required")
	}
	if h == nil || h.redis == nil || h.redis.Client == nil {
		return fmt.Errorf("redis is not configured")
	}

	membersKey := roomMembersKey(normalizedRoomID)
	isMember, err := h.redis.Client.SIsMember(ctx, membersKey, normalizedUserID).Result()
	if err != nil {
		return err
	}
	if !isMember {
		return fmt.Errorf("user is not a room member")
	}

	adminKey := roomAdminsKey(normalizedRoomID)
	if err := h.redis.Client.SAdd(ctx, adminKey, normalizedUserID).Err(); err != nil {
		return err
	}
	roomTTL := h.effectiveRoomTTL(ctx, normalizedRoomID)
	if roomTTL > 0 {
		_ = h.redis.Client.Expire(ctx, adminKey, roomTTL).Err()
	}
	return nil
}

func (h *RoomHandler) indexRoomName(ctx context.Context, roomID, roomName string, createdAt int64) error {
	roomID = normalizeRoomID(roomID)
	lookup := normalizeRoomNameLookup(roomName)
	if roomID == "" || lookup == "" {
		return nil
	}
	if createdAt <= 0 {
		createdAt = time.Now().Unix()
	}

	nameKey := roomNameLookupKey(lookup)
	if err := h.redis.Client.ZAdd(
		ctx,
		nameKey,
		redis.Z{
			Score:  float64(createdAt),
			Member: roomID,
		},
	).Err(); err != nil {
		return err
	}
	_ = h.redis.Client.Expire(ctx, nameKey, roomMaxExtendAge).Err()
	return nil
}

func (h *RoomHandler) createRoom(
	ctx context.Context,
	roomID,
	roomName,
	roomType string,
	createdAt int64,
	parentRoomID string,
	originMessageID string,
	roomTTL time.Duration,
	roomPasswordHash string,
) error {
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return fmt.Errorf("room id is required")
	}
	normalizedRoomName := normalizeRoomName(roomName)
	if normalizedRoomName == "" {
		normalizedRoomName = "Room"
	}
	normalizedRoomType := strings.TrimSpace(roomType)
	if normalizedRoomType == "" {
		normalizedRoomType = "ephemeral"
	}
	normalizedNameLookup := normalizeRoomNameLookup(normalizedRoomName)
	normalizedParentID := normalizeRoomID(parentRoomID)
	normalizedOriginMessageID := strings.TrimSpace(originMessageID)
	normalizedRoomPasswordHash := normalizeRoomPasswordHash(roomPasswordHash)

	if err := h.redis.Client.HSet(ctx, roomKey(normalizedRoomID), map[string]interface{}{
		"id":                 normalizedRoomID,
		"name":               normalizedRoomName,
		"name_lookup":        normalizedNameLookup,
		"type":               normalizedRoomType,
		"created_at":         createdAt,
		"parent_room_id":     normalizedParentID,
		"origin_message_id":  normalizedOriginMessageID,
		"room_password_hash": normalizedRoomPasswordHash,
		"member_count":       0,
	}).Err(); err != nil {
		return err
	}

	if roomTTL <= 0 {
		roomTTL = roomDefaultTTL
	}

	if err := h.redis.Client.Expire(ctx, roomKey(normalizedRoomID), roomTTL).Err(); err != nil {
		return err
	}

	if _, err := h.ensureRoomCode(ctx, normalizedRoomID); err != nil {
		return err
	}
	adminCode, err := h.ensureRoomAdminCode(ctx, normalizedRoomID)
	if err != nil {
		return err
	}
	if err := h.indexRoomName(ctx, normalizedRoomID, normalizedRoomName, createdAt); err != nil {
		return err
	}
	h.upsertRoomRecord(
		ctx,
		normalizedRoomID,
		normalizedRoomName,
		normalizedRoomType,
		normalizedParentID,
		normalizedOriginMessageID,
		adminCode,
	)

	return nil
}

func (h *RoomHandler) registerRoomMembership(ctx context.Context, roomID, userID string) (int, error) {
	roomID = normalizeRoomID(roomID)
	userID = normalizeIdentifier(userID)
	if roomID == "" || userID == "" {
		return 0, fmt.Errorf("room and user are required")
	}

	membersKey := roomMembersKey(roomID)
	alreadyMember, err := h.redis.Client.SIsMember(ctx, membersKey, userID).Result()
	if err != nil {
		return 0, err
	}

	count, err := h.redis.Client.SCard(ctx, membersKey).Result()
	if err != nil {
		return 0, err
	}
	if !alreadyMember && count >= models.MaxRoomMembers {
		return int(count), errRoomFull
	}

	if !alreadyMember {
		if err := h.redis.Client.SAdd(ctx, membersKey, userID).Err(); err != nil {
			return 0, err
		}
	}

	count, err = h.redis.Client.SCard(ctx, membersKey).Result()
	if err != nil {
		return 0, err
	}

	if err := h.redis.Client.HSet(ctx, roomKey(roomID), "member_count", count).Err(); err != nil {
		return int(count), err
	}
	if err := h.redis.Client.SAdd(ctx, userRoomsKey(userID), roomID).Err(); err != nil {
		return int(count), err
	}
	_ = h.redis.Client.SRem(ctx, userHiddenRoomsKey(userID), roomID).Err()
	roomTTL := h.effectiveRoomTTL(ctx, roomID)
	_ = h.redis.Client.Expire(ctx, membersKey, roomTTL).Err()
	if err := h.redis.Client.HSetNX(ctx, roomMemberJoinedAtKey(roomID), userID, time.Now().Unix()).Err(); err != nil {
		return int(count), err
	}
	_ = h.redis.Client.Expire(ctx, roomMemberJoinedAtKey(roomID), roomTTL).Err()

	return int(count), nil
}

func (h *RoomHandler) isRoomAdmin(ctx context.Context, roomID, userID string) (bool, error) {
	roomID = normalizeRoomID(roomID)
	userID = normalizeIdentifier(userID)
	if roomID == "" || userID == "" {
		return false, nil
	}
	if h == nil || h.redis == nil || h.redis.Client == nil {
		return false, nil
	}

	adminMembers, err := h.redis.Client.SMembers(ctx, roomAdminsKey(roomID)).Result()
	if err != nil && err != redis.Nil {
		return false, err
	}
	normalizedAdmins := make(map[string]struct{}, len(adminMembers))
	for _, rawAdminID := range adminMembers {
		adminID := normalizeIdentifier(rawAdminID)
		if adminID == "" {
			_ = h.redis.Client.SRem(ctx, roomAdminsKey(roomID), rawAdminID).Err()
			continue
		}
		normalizedAdmins[adminID] = struct{}{}
	}
	if len(normalizedAdmins) > 0 {
		_, ok := normalizedAdmins[userID]
		return ok, nil
	}

	adminUserID, err := h.resolveRoomAdminUserID(ctx, roomID)
	if err != nil {
		return false, err
	}
	if adminUserID == userID {
		if grantErr := h.grantRoomAdmin(ctx, roomID, userID); grantErr != nil {
			log.Printf("[room] migrate implicit admin failed room=%s user=%s err=%v", roomID, userID, grantErr)
		}
	}
	return adminUserID == userID, nil
}

func (h *RoomHandler) resolveRoomAdminUserID(ctx context.Context, roomID string) (string, error) {
	roomID = normalizeRoomID(roomID)
	if roomID == "" || h == nil || h.redis == nil || h.redis.Client == nil {
		return "", nil
	}

	members, err := h.redis.Client.SMembers(ctx, roomMembersKey(roomID)).Result()
	if err != nil {
		return "", err
	}
	if len(members) == 0 {
		return "", nil
	}

	memberSet := make(map[string]struct{}, len(members))
	normalizedMembers := make([]string, 0, len(members))
	for _, rawMember := range members {
		memberID := normalizeIdentifier(rawMember)
		if memberID == "" {
			_ = h.redis.Client.SRem(ctx, roomMembersKey(roomID), rawMember).Err()
			continue
		}
		memberSet[memberID] = struct{}{}
		normalizedMembers = append(normalizedMembers, memberID)
	}
	if len(normalizedMembers) == 0 {
		return "", nil
	}

	joinedRaw, err := h.redis.Client.HGetAll(ctx, roomMemberJoinedAtKey(roomID)).Result()
	if err != nil && err != redis.Nil {
		return "", err
	}

	type memberJoin struct {
		userID   string
		joinedAt int64
	}
	entries := make([]memberJoin, 0, len(normalizedMembers))
	for _, memberID := range normalizedMembers {
		joinedAt := int64(0)
		if rawTimestamp, ok := joinedRaw[memberID]; ok {
			if parsed, parseErr := strconv.ParseInt(strings.TrimSpace(rawTimestamp), 10, 64); parseErr == nil {
				joinedAt = parsed
			}
		}
		if joinedAt <= 0 {
			joinedAt = time.Now().Unix()
			_ = h.redis.Client.HSet(ctx, roomMemberJoinedAtKey(roomID), memberID, joinedAt).Err()
		}
		entries = append(entries, memberJoin{
			userID:   memberID,
			joinedAt: joinedAt,
		})
	}

	for trackedMemberID := range joinedRaw {
		if _, exists := memberSet[trackedMemberID]; exists {
			continue
		}
		_ = h.redis.Client.HDel(ctx, roomMemberJoinedAtKey(roomID), trackedMemberID).Err()
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].joinedAt == entries[j].joinedAt {
			return entries[i].userID < entries[j].userID
		}
		return entries[i].joinedAt < entries[j].joinedAt
	})
	return entries[0].userID, nil
}

func (h *RoomHandler) collectRoomSubtreeIDs(ctx context.Context, rootRoomID string) ([]string, error) {
	rootRoomID = normalizeRoomID(rootRoomID)
	if rootRoomID == "" {
		return nil, nil
	}

	seen := map[string]struct{}{rootRoomID: {}}
	queue := []string{rootRoomID}
	ordered := make([]string, 0, 8)
	for len(queue) > 0 {
		roomID := queue[0]
		queue = queue[1:]
		ordered = append(ordered, roomID)

		children, err := h.redis.Client.SMembers(ctx, roomChildrenKey(roomID)).Result()
		if err != nil && err != redis.Nil {
			return nil, err
		}
		for _, rawChildID := range children {
			childID := normalizeRoomID(rawChildID)
			if childID == "" {
				_ = h.redis.Client.SRem(ctx, roomChildrenKey(roomID), rawChildID).Err()
				continue
			}
			if _, exists := seen[childID]; exists {
				continue
			}
			seen[childID] = struct{}{}
			queue = append(queue, childID)
		}
	}

	// Delete descendants first, root last.
	for left, right := 0, len(ordered)-1; left < right; left, right = left+1, right-1 {
		ordered[left], ordered[right] = ordered[right], ordered[left]
	}
	return ordered, nil
}

func (h *RoomHandler) deleteSingleRoom(ctx context.Context, roomID string) error {
	roomID = normalizeRoomID(roomID)
	if roomID == "" {
		return nil
	}

	meta, err := h.redis.Client.HGetAll(ctx, roomKey(roomID)).Result()
	if err != nil && err != redis.Nil {
		return err
	}
	if len(meta) == 0 {
		return nil
	}

	parentRoomID := normalizeRoomID(meta["parent_room_id"])
	if parentRoomID != "" {
		_ = h.redis.Client.SRem(ctx, roomChildrenKey(parentRoomID), roomID).Err()
	}

	if roomCode := normalizeRoomCode(meta["room_code"]); roomCode != "" {
		_ = h.redis.Client.Del(ctx, roomCodeKey(roomCode)).Err()
	}

	nameLookup := normalizeRoomNameLookup(meta["name_lookup"])
	if nameLookup == "" {
		nameLookup = normalizeRoomNameLookup(meta["name"])
	}
	if nameLookup != "" {
		_ = h.redis.Client.ZRem(ctx, roomNameLookupKey(nameLookup), roomID).Err()
	}

	members, err := h.redis.Client.SMembers(ctx, roomMembersKey(roomID)).Result()
	if err == nil {
		for _, rawMember := range members {
			memberID := normalizeIdentifier(rawMember)
			if memberID == "" {
				continue
			}
			_ = h.redis.Client.SRem(ctx, userRoomsKey(memberID), roomID).Err()
		}
	}

	if h.scylla != nil && h.scylla.Session != nil {
		roomsTable := h.scylla.Table("rooms")
		deleteQuery := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ?`, roomsTable)
		if execErr := h.scylla.Session.Query(deleteQuery, roomID).WithContext(ctx).Exec(); execErr != nil {
			log.Printf("[room] delete scylla room failed room=%s err=%v", roomID, execErr)
		}
	}

	_, _ = h.redis.Client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Del(ctx, roomMembersKey(roomID))
		pipe.Del(ctx, roomAdminsKey(roomID))
		pipe.Del(ctx, roomMemberJoinedAtKey(roomID))
		pipe.Del(ctx, roomChildrenKey(roomID))
		pipe.Del(ctx, roomHistoryKey(roomID))
		pipe.Del(ctx, roomKey(roomID))
		return nil
	})

	return nil
}

func (h *RoomHandler) syncBreakJoinCount(ctx context.Context, roomID string, memberCount int) error {
	roomID = normalizeRoomID(roomID)
	if roomID == "" {
		return nil
	}
	meta, err := h.redis.Client.HGetAll(ctx, roomKey(roomID)).Result()
	if err != nil {
		return err
	}

	parentRoomID := normalizeRoomID(meta["parent_room_id"])
	originMessageID := strings.TrimSpace(meta["origin_message_id"])
	if parentRoomID == "" || originMessageID == "" {
		return nil
	}

	if err := h.redis.Client.HSet(ctx, messageBreakKey(originMessageID), map[string]interface{}{
		"has_break_room":   1,
		"break_room_id":    roomID,
		"break_join_count": memberCount,
		"updated_at":       time.Now().Unix(),
	}).Err(); err != nil {
		return err
	}

	if err := h.updateBreakMetadataInCachedHistory(ctx, parentRoomID, originMessageID, roomID, memberCount); err != nil {
		return err
	}
	h.tryUpdateBreakMetadataInScylla(parentRoomID, originMessageID, roomID, memberCount)
	roomName := normalizeRoomName(meta["name"])
	createdAt, _ := strconv.ParseInt(strings.TrimSpace(meta["created_at"]), 10, 64)
	expiresAt := h.getRoomExpiryUnix(ctx, roomID)
	requiresPassword := normalizeRoomPasswordHash(meta["room_password_hash"]) != ""
	h.broadcastBreakMetadataUpdate(
		parentRoomID,
		originMessageID,
		roomID,
		roomName,
		memberCount,
		createdAt,
		expiresAt,
		requiresPassword,
	)
	return nil
}

func (h *RoomHandler) broadcastBreakMetadataUpdate(
	parentRoomID string,
	originMessageID string,
	breakRoomID string,
	breakRoomName string,
	memberCount int,
	createdAt int64,
	expiresAt int64,
	requiresPassword bool,
) {
	normalizedParentRoomID := normalizeRoomID(parentRoomID)
	normalizedOriginMessageID := strings.TrimSpace(originMessageID)
	normalizedBreakRoomID := normalizeRoomID(breakRoomID)
	if normalizedParentRoomID == "" || normalizedOriginMessageID == "" || normalizedBreakRoomID == "" {
		return
	}
	normalizedBreakRoomName := normalizeRoomName(breakRoomName)
	if normalizedBreakRoomName == "" {
		normalizedBreakRoomName = "Room"
	}

	serverNow := time.Now().Unix()
	payload := map[string]interface{}{
		"hasBreakRoom":      true,
		"has_break_room":    true,
		"originMessageId":   normalizedOriginMessageID,
		"origin_message_id": normalizedOriginMessageID,
		"breakRoomId":       normalizedBreakRoomID,
		"break_room_id":     normalizedBreakRoomID,
		"breakJoinCount":    memberCount,
		"break_join_count":  memberCount,
		"breakRoomName":     normalizedBreakRoomName,
		"break_room_name":   normalizedBreakRoomName,
		"requiresPassword":  requiresPassword,
		"requires_password": requiresPassword,
		"parentRoomId":      normalizedParentRoomID,
		"parent_room_id":    normalizedParentRoomID,
		"serverNow":         serverNow,
		"server_now":        serverNow,
	}
	if createdAt > 0 {
		payload["createdAt"] = createdAt
		payload["created_at"] = createdAt
	}
	if expiresAt > 0 {
		payload["expiresAt"] = expiresAt
		payload["expires_at"] = expiresAt
	}

	h.broadcastRoomEvent(normalizedParentRoomID, "message_break_updated", payload)
}

func (h *RoomHandler) loadSidebarRoom(ctx context.Context, roomID, status string) (SidebarRoom, bool, error) {
	roomID = normalizeRoomID(roomID)
	if roomID == "" {
		return SidebarRoom{}, false, nil
	}
	meta, err := h.redis.Client.HGetAll(ctx, roomKey(roomID)).Result()
	if err != nil {
		return SidebarRoom{}, false, err
	}
	if len(meta) == 0 {
		return SidebarRoom{}, false, nil
	}

	name := strings.TrimSpace(meta["name"])
	if name == "" {
		name = "Room"
	}
	name = normalizeRoomName(name)
	if name == "" {
		name = "Room"
	}
	createdAt, _ := strconv.ParseInt(meta["created_at"], 10, 64)
	memberCount64, _ := strconv.ParseInt(meta["member_count"], 10, 64)
	expiresAt := h.getRoomExpiryUnix(ctx, roomID)
	requiresPassword := normalizeRoomPasswordHash(meta["room_password_hash"]) != ""

	return SidebarRoom{
		RoomID:           roomID,
		RoomName:         name,
		Status:           status,
		ParentRoomID:     normalizeRoomID(meta["parent_room_id"]),
		OriginMessageID:  strings.TrimSpace(meta["origin_message_id"]),
		TreeNumber:       0,
		MemberCount:      int(memberCount64),
		CreatedAt:        createdAt,
		ExpiresAt:        expiresAt,
		RequiresPassword: requiresPassword,
	}, true, nil
}

func (h *RoomHandler) refreshRoomMessageTTL(ctx context.Context, roomID string, ttl time.Duration) error {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return nil
	}

	roomID = normalizeRoomID(roomID)
	if roomID == "" {
		return nil
	}

	// Soft-expiry cutoff: only messages newer than this timestamp are visible.
	softCutoff := time.Now().UTC().Add(-ttl)
	softExpiryTable := h.scylla.Table(roomSoftExpiryTable)
	upsertQuery := fmt.Sprintf(
		`INSERT INTO %s (room_id, extended_expiry_time, updated_at) VALUES (?, ?, ?)`,
		softExpiryTable,
	)
	if err := h.scylla.Session.Query(
		upsertQuery,
		roomID,
		softCutoff,
		time.Now().UTC(),
	).WithContext(ctx).Exec(); err != nil {
		return err
	}

	return nil
}

func (h *RoomHandler) updateBreakMetadataInCachedHistory(
	ctx context.Context,
	roomID string,
	originMessageID string,
	breakRoomID string,
	joinCount int,
) error {
	historyKey := roomHistoryKey(roomID)
	entries, err := h.redis.Client.LRange(ctx, historyKey, 0, -1).Result()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}

	updated := false
	for i, raw := range entries {
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			continue
		}

		msgID := toString(payload["id"])
		if msgID == "" {
			continue
		}
		if msgID != originMessageID {
			continue
		}

		payload["hasBreakRoom"] = true
		payload["breakRoomId"] = breakRoomID
		payload["breakJoinCount"] = joinCount
		payload["has_break_room"] = true
		payload["break_room_id"] = breakRoomID
		payload["break_join_count"] = joinCount

		encoded, err := json.Marshal(payload)
		if err != nil {
			continue
		}
		entries[i] = string(encoded)
		updated = true
	}

	if !updated {
		return nil
	}

	pipe := h.redis.Client.TxPipeline()
	pipe.Del(ctx, historyKey)
	items := make([]interface{}, 0, len(entries))
	for _, entry := range entries {
		items = append(items, entry)
	}
	if len(items) > 0 {
		pipe.RPush(ctx, historyKey, items...)
		pipe.LTrim(ctx, historyKey, -roomHistorySize, -1)
		pipe.Expire(ctx, historyKey, h.effectiveRoomTTL(ctx, roomID))
	}
	_, err = pipe.Exec(ctx)
	return err
}

func (h *RoomHandler) tryUpdateBreakMetadataInScylla(parentRoomID, originMessageID, breakRoomID string, joinCount int) {
	if h.scylla == nil || h.scylla.Session == nil {
		return
	}

	messagesTable := h.scylla.Table("messages")
	var createdAt time.Time
	iter := h.scylla.Session.Query(
		fmt.Sprintf(`SELECT created_at FROM %s WHERE room_id = ? AND message_id = ? LIMIT 1 ALLOW FILTERING`, messagesTable),
		parentRoomID,
		originMessageID,
	).Iter()
	if !iter.Scan(&createdAt) {
		_ = iter.Close()
		return
	}
	if err := iter.Close(); err != nil {
		log.Printf("[room] scylla break metadata lookup failed room=%s msg=%s err=%v", parentRoomID, originMessageID, err)
		return
	}

	err := h.scylla.Session.Query(
		fmt.Sprintf(`UPDATE %s SET has_break_room = ?, break_room_id = ?, break_join_count = ? WHERE room_id = ? AND created_at = ? AND message_id = ?`, messagesTable),
		true,
		breakRoomID,
		joinCount,
		parentRoomID,
		createdAt,
		originMessageID,
	).Exec()
	if err != nil {
		log.Printf("[room] scylla break metadata update skipped room=%s msg=%s err=%v", parentRoomID, originMessageID, err)
	}
}

func truncate(input string, max int) string {
	trimmed := strings.TrimSpace(input)
	if max <= 0 || len(trimmed) <= max {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:max])
}

func roomKey(roomID string) string {
	return "room:" + roomID
}

func roomMembersKey(roomID string) string {
	return "room:" + roomID + ":members"
}

func roomAdminsKey(roomID string) string {
	return "room:" + roomID + ":admins"
}

func roomMemberJoinedAtKey(roomID string) string {
	return "room:" + roomID + ":member_joined_at"
}

func roomChildrenKey(roomID string) string {
	return "room:" + roomID + ":children"
}

func userRoomsKey(userID string) string {
	return "user:" + userID + ":rooms"
}

func userHiddenRoomsKey(userID string) string {
	return "user:" + userID + ":hidden_rooms"
}

func messageBreakKey(messageID string) string {
	return messageBreakPrefix + messageID
}

func roomHistoryKey(roomID string) string {
	return "room:history:" + roomID
}

func roomFilesKey(roomID string) string {
	return "room:" + roomID + ":files"
}

func roomCodeKey(roomCode string) string {
	return "room:code:" + roomCode
}

func roomNameLookupKey(nameLookup string) string {
	return roomNameIndexPrefix + strings.ToLower(strings.TrimSpace(nameLookup))
}

func roomTreeNumbersKey(userID string) string {
	return roomTreeNumberPrefix + normalizeIdentifier(userID)
}

func toString(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	default:
		return ""
	}
}

func (h *RoomHandler) CreateRoom(w http.ResponseWriter, r *http.Request) {
	clientIP := extractClientIP(r)
	if !roomCreateLimiter.Allow(clientIP) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Create room rate limit exceeded"})
		return
	}

	w.WriteHeader(http.StatusNotImplemented)
}
