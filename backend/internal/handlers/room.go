package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/models"
)

const (
	roomKeyTTL         = 6 * time.Hour
	roomMaxExtendAge   = 14 * 24 * time.Hour
	roomHistoryTTL     = roomKeyTTL
	roomHistorySize    = 50
	messageBreakPrefix = "message:break:"
)

var (
	errRoomFull = errors.New("room full")

	roomSuffixWords = []string{
		"hub", "zone", "chat", "base", "net",
		"talk", "lounge", "pulse", "nest", "crew",
		"loop", "dock", "den", "forge", "space",
		"spot", "sync", "stream", "wave", "link",
	}
)

type RoomHandler struct {
	redis  *database.RedisStore
	scylla *database.ScyllaStore
}

func NewRoomHandler(redisStore *database.RedisStore, scyllaStore *database.ScyllaStore) *RoomHandler {
	return &RoomHandler{redis: redisStore, scylla: scyllaStore}
}

type JoinRoomRequest struct {
	RoomName string `json:"roomName"`
	Username string `json:"username"`
	UserID   string `json:"userId"`
	Type     string `json:"type"`
	Mode     string `json:"mode"`
}

type JoinRoomResponse struct {
	RoomID    string `json:"roomId"`
	RoomName  string `json:"roomName"`
	UserID    string `json:"userId"`
	Token     string `json:"token"`
	CreatedAt int64  `json:"createdAt"`
}

type ExtendRoomRequest struct {
	RoomID string `json:"roomId"`
}

type ExtendRoomResponse struct {
	RoomID           string `json:"roomId"`
	ExpiresInSeconds int64  `json:"expiresInSeconds"`
	Message          string `json:"message"`
}

type CreateBreakRoomRequest struct {
	ParentRoomID    string `json:"parentRoomId"`
	OriginMessageID string `json:"originMessageId"`
	RoomName        string `json:"roomName"`
	UserID          string `json:"userId"`
	Username        string `json:"username"`
}

type CreateBreakRoomResponse struct {
	RoomID          string `json:"roomId"`
	RoomName        string `json:"roomName"`
	ParentRoomID    string `json:"parentRoomId"`
	OriginMessageID string `json:"originMessageId"`
	CreatedAt       int64  `json:"createdAt"`
}

type SidebarRoom struct {
	RoomID          string `json:"roomId"`
	RoomName        string `json:"roomName"`
	Status          string `json:"status"`
	ParentRoomID    string `json:"parentRoomId,omitempty"`
	OriginMessageID string `json:"originMessageId,omitempty"`
	MemberCount     int    `json:"memberCount"`
	CreatedAt       int64  `json:"createdAt"`
}

type SidebarRoomsResponse struct {
	Rooms []SidebarRoom `json:"rooms"`
}

func (h *RoomHandler) JoinRoom(w http.ResponseWriter, r *http.Request) {
	var req JoinRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}
	log.Printf("[room] join requested raw_room=%q username=%q type=%q mode=%q", req.RoomName, req.Username, req.Type, req.Mode)

	if strings.TrimSpace(req.RoomName) == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Room name cannot be empty"})
		return
	}

	baseSlug := slugifyRoomName(req.RoomName)
	if baseSlug == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Room name must contain letters or numbers"})
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

	ctx := context.Background()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
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

	finalRoomID := baseSlug
	finalRoomName := baseSlug
	if mode == "join" {
		exists, err := h.roomExists(ctx, baseSlug)
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

		name, err := h.getRoomName(ctx, baseSlug)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to read room data"})
			return
		}
		if strings.TrimSpace(name) != "" {
			finalRoomName = name
		}
	} else {
		created, err := h.tryCreateRoom(ctx, baseSlug, baseSlug, roomType, createdAt, "", "")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to access room storage"})
			return
		}

		if !created {
			suffixOrder := rng.Perm(len(roomSuffixWords))
			for i := 0; i < 3 && i < len(suffixOrder); i++ {
				candidateID := fmt.Sprintf("%s_%s", baseSlug, roomSuffixWords[suffixOrder[i]])
				candidateName := candidateID

				created, err = h.tryCreateRoom(ctx, candidateID, candidateName, roomType, createdAt, "", "")
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(map[string]string{"error": "Failed to access room storage"})
					return
				}

				if created {
					finalRoomID = candidateID
					finalRoomName = candidateName
					break
				}
			}
		}

		if !created {
			for attempts := 0; attempts < 10; attempts++ {
				fallbackID := fmt.Sprintf("%s_%04d", baseSlug, rng.Intn(9000)+1000)
				fallbackName := fallbackID

				created, err = h.tryCreateRoom(ctx, fallbackID, fallbackName, roomType, createdAt, "", "")
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(map[string]string{"error": "Failed to access room storage"})
					return
				}

				if created {
					finalRoomID = fallbackID
					finalRoomName = fallbackName
					break
				}
			}
		}

		if !created {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to allocate unique room name"})
			return
		}
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
	if err := h.syncBreakJoinCount(ctx, finalRoomID, memberCount); err != nil {
		log.Printf("[room] break join count sync failed room=%s err=%v", finalRoomID, err)
	}

	log.Printf("[room] join resolved room_id=%s room_name=%s user_id=%s mode=%s members=%d", finalRoomID, finalRoomName, userID, mode, memberCount)

	finalCreatedAt, err := h.getRoomCreatedAt(ctx, finalRoomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to resolve room created time"})
		return
	}
	if finalCreatedAt <= 0 {
		finalCreatedAt = createdAt
	}

	response := JoinRoomResponse{
		RoomID:    finalRoomID,
		RoomName:  finalRoomName,
		UserID:    userID,
		Token:     token,
		CreatedAt: finalCreatedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (h *RoomHandler) CreateBreakRoom(w http.ResponseWriter, r *http.Request) {
	var req CreateBreakRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}

	parentRoomID := slugifyRoomName(req.ParentRoomID)
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

	parentRoomName, err := h.getRoomName(ctx, parentRoomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to read parent room"})
		return
	}
	if parentRoomName == "" {
		parentRoomName = parentRoomID
	}

	messageSlug := slugifyRoomName(truncate(req.RoomName, 20))
	if messageSlug == "" {
		messageSlug = "break"
	}
	parentSlug := slugifyRoomName(parentRoomName)
	if parentSlug == "" {
		parentSlug = parentRoomID
	}

	baseSlug := slugifyRoomName(fmt.Sprintf("%s_%s", messageSlug, parentSlug))
	if baseSlug == "" {
		baseSlug = fmt.Sprintf("break_%s", parentRoomID)
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	createdAt := time.Now().Unix()
	roomType, err := h.getRoomType(ctx, parentRoomID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to read parent room type"})
		return
	}
	if roomType == "" {
		roomType = "ephemeral"
	}

	finalRoomID := baseSlug
	finalRoomName := baseSlug
	created, err := h.tryCreateRoom(ctx, baseSlug, baseSlug, roomType, createdAt, parentRoomID, originMessageID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create break room"})
		return
	}
	if !created {
		suffixOrder := rng.Perm(len(roomSuffixWords))
		for i := 0; i < 3 && i < len(suffixOrder); i++ {
			candidateID := fmt.Sprintf("%s_%s", baseSlug, roomSuffixWords[suffixOrder[i]])
			created, err = h.tryCreateRoom(ctx, candidateID, candidateID, roomType, createdAt, parentRoomID, originMessageID)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create break room"})
				return
			}
			if created {
				finalRoomID = candidateID
				finalRoomName = candidateID
				break
			}
		}
	}
	if !created {
		for attempts := 0; attempts < 10; attempts++ {
			candidateID := fmt.Sprintf("%s_%04d", baseSlug, rng.Intn(9000)+1000)
			created, err = h.tryCreateRoom(ctx, candidateID, candidateID, roomType, createdAt, parentRoomID, originMessageID)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "Failed to create break room"})
				return
			}
			if created {
				finalRoomID = candidateID
				finalRoomName = candidateID
				break
			}
		}
	}
	if !created {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to allocate unique break room"})
		return
	}

	if err := h.redis.Client.SAdd(ctx, roomChildrenKey(parentRoomID), finalRoomID).Err(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to link break room"})
		return
	}
	_ = h.redis.Client.Expire(ctx, roomChildrenKey(parentRoomID), roomKeyTTL).Err()

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

	log.Printf("[room] break created room=%s parent=%s origin=%s", finalRoomID, parentRoomID, originMessageID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(CreateBreakRoomResponse{
		RoomID:          finalRoomID,
		RoomName:        finalRoomName,
		ParentRoomID:    parentRoomID,
		OriginMessageID: originMessageID,
		CreatedAt:       createdAt,
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
	joinedRoomIDs, err := h.redis.Client.SMembers(ctx, userRoomsKey(userID)).Result()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load user rooms"})
		return
	}

	roomsMap := make(map[string]SidebarRoom)
	for _, roomID := range joinedRoomIDs {
		room, ok, err := h.loadSidebarRoom(ctx, roomID, "joined")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load sidebar rooms"})
			return
		}
		if ok {
			roomsMap[roomID] = room
		}
	}

	for _, parentRoomID := range joinedRoomIDs {
		children, err := h.redis.Client.SMembers(ctx, roomChildrenKey(parentRoomID)).Result()
		if err != nil {
			continue
		}
		for _, childRoomID := range children {
			if _, exists := roomsMap[childRoomID]; exists {
				continue
			}

			status := "discoverable"
			isMember, err := h.redis.Client.SIsMember(ctx, roomMembersKey(childRoomID), userID).Result()
			if err == nil && isMember {
				status = "joined"
			}

			room, ok, err := h.loadSidebarRoom(ctx, childRoomID, status)
			if err != nil {
				continue
			}
			if ok {
				roomsMap[childRoomID] = room
			}
		}
	}

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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(SidebarRoomsResponse{Rooms: rooms})
}

func (h *RoomHandler) ExtendRoom(w http.ResponseWriter, r *http.Request) {
	var req ExtendRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid JSON format"})
		return
	}
	log.Printf("[room] extend requested room_id=%q", req.RoomID)

	roomID := slugifyRoomName(req.RoomID)
	if roomID == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "roomId is required"})
		return
	}

	ctx := context.Background()
	roomKey := roomKey(roomID)

	exists, err := h.redis.Client.Exists(ctx, roomKey).Result()
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

	createdAtRaw, err := h.redis.Client.HGet(ctx, roomKey, "created_at").Result()
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

	age := time.Since(time.Unix(createdAtUnix, 0))
	if age >= roomMaxExtendAge {
		log.Printf("[room] extend denied room_id=%s age_hours=%.2f", roomID, age.Hours())
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "Room has reached its 15-day limit"})
		return
	}

	if err := h.redis.Client.Expire(ctx, roomKey, roomKeyTTL).Err(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to extend room"})
		return
	}
	_ = h.redis.Client.Expire(ctx, roomMembersKey(roomID), roomKeyTTL).Err()
	_ = h.redis.Client.Expire(ctx, roomChildrenKey(roomID), roomKeyTTL).Err()
	_ = h.redis.Client.Expire(ctx, roomHistoryKey(roomID), roomKeyTTL).Err()

	if err := h.refreshRoomMessageTTL(ctx, roomID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Failed to extend room messages"})
		return
	}

	log.Printf("[room] extend success room_id=%s ttl_seconds=%d", roomID, int64(roomKeyTTL.Seconds()))

	response := ExtendRoomResponse{
		RoomID:           roomID,
		ExpiresInSeconds: int64(roomKeyTTL.Seconds()),
		Message:          "Room extended",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func slugifyRoomName(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return ""
	}

	var builder strings.Builder
	prevSeparator := false

	for _, ch := range normalized {
		switch {
		case (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9'):
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

func (h *RoomHandler) tryCreateRoom(
	ctx context.Context,
	roomID,
	roomName,
	roomType string,
	createdAt int64,
	parentRoomID string,
	originMessageID string,
) (bool, error) {
	exists, err := h.roomExists(ctx, roomID)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

	if err := h.createRoom(ctx, roomID, roomName, roomType, createdAt, parentRoomID, originMessageID); err != nil {
		return false, err
	}

	return true, nil
}

func (h *RoomHandler) roomExists(ctx context.Context, roomID string) (bool, error) {
	count, err := h.redis.Client.Exists(ctx, roomKey(roomID)).Result()
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (h *RoomHandler) getRoomName(ctx context.Context, roomID string) (string, error) {
	name, err := h.redis.Client.HGet(ctx, roomKey(roomID), "name").Result()
	if err == redis.Nil {
		return "", nil
	}
	return name, err
}

func (h *RoomHandler) getRoomType(ctx context.Context, roomID string) (string, error) {
	roomType, err := h.redis.Client.HGet(ctx, roomKey(roomID), "type").Result()
	if err == redis.Nil {
		return "", nil
	}
	return roomType, err
}

func (h *RoomHandler) getRoomCreatedAt(ctx context.Context, roomID string) (int64, error) {
	raw, err := h.redis.Client.HGet(ctx, roomKey(roomID), "created_at").Result()
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

func (h *RoomHandler) createRoom(
	ctx context.Context,
	roomID,
	roomName,
	roomType string,
	createdAt int64,
	parentRoomID string,
	originMessageID string,
) error {
	if err := h.redis.Client.HSet(ctx, roomKey(roomID), map[string]interface{}{
		"id":                roomID,
		"name":              roomName,
		"type":              roomType,
		"created_at":        createdAt,
		"parent_room_id":    parentRoomID,
		"origin_message_id": originMessageID,
		"member_count":      0,
	}).Err(); err != nil {
		return err
	}

	if err := h.redis.Client.Expire(ctx, roomKey(roomID), roomKeyTTL).Err(); err != nil {
		return err
	}

	return nil
}

func (h *RoomHandler) registerRoomMembership(ctx context.Context, roomID, userID string) (int, error) {
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
	_ = h.redis.Client.Expire(ctx, membersKey, roomKeyTTL).Err()

	return int(count), nil
}

func (h *RoomHandler) syncBreakJoinCount(ctx context.Context, roomID string, memberCount int) error {
	meta, err := h.redis.Client.HGetAll(ctx, roomKey(roomID)).Result()
	if err != nil {
		return err
	}

	parentRoomID := slugifyRoomName(meta["parent_room_id"])
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
	return nil
}

func (h *RoomHandler) loadSidebarRoom(ctx context.Context, roomID, status string) (SidebarRoom, bool, error) {
	meta, err := h.redis.Client.HGetAll(ctx, roomKey(roomID)).Result()
	if err != nil {
		return SidebarRoom{}, false, err
	}
	if len(meta) == 0 {
		return SidebarRoom{}, false, nil
	}

	name := strings.TrimSpace(meta["name"])
	if name == "" {
		name = roomID
	}
	createdAt, _ := strconv.ParseInt(meta["created_at"], 10, 64)
	memberCount64, _ := strconv.ParseInt(meta["member_count"], 10, 64)

	return SidebarRoom{
		RoomID:          roomID,
		RoomName:        name,
		Status:          status,
		ParentRoomID:    strings.TrimSpace(meta["parent_room_id"]),
		OriginMessageID: strings.TrimSpace(meta["origin_message_id"]),
		MemberCount:     int(memberCount64),
		CreatedAt:       createdAt,
	}, true, nil
}

func (h *RoomHandler) refreshRoomMessageTTL(ctx context.Context, roomID string) error {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return nil
	}

	messagesTable := h.scylla.Table("messages")
	selectQuery := fmt.Sprintf(
		`SELECT created_at, message_id, sender_id, sender_name, content, type FROM %s WHERE room_id = ?`,
		messagesTable,
	)
	upsertQuery := fmt.Sprintf(
		`INSERT INTO %s (room_id, created_at, message_id, sender_id, sender_name, content, type) VALUES (?, ?, ?, ?, ?, ?, ?) USING TTL ?`,
		messagesTable,
	)
	ttlSeconds := int(roomKeyTTL / time.Second)

	iter := h.scylla.Session.Query(selectQuery, roomID).WithContext(ctx).Iter()
	var (
		createdAt  time.Time
		messageID  string
		senderID   string
		senderName string
		content    string
		msgType    string
	)

	refreshedCount := 0
	for iter.Scan(&createdAt, &messageID, &senderID, &senderName, &content, &msgType) {
		if err := h.scylla.Session.Query(
			upsertQuery,
			roomID,
			createdAt,
			messageID,
			senderID,
			senderName,
			content,
			msgType,
			ttlSeconds,
		).WithContext(ctx).Exec(); err != nil {
			_ = iter.Close()
			return err
		}
		refreshedCount++
	}
	if err := iter.Close(); err != nil {
		return err
	}
	if refreshedCount > 0 {
		log.Printf("[room] message ttl refreshed room_id=%s count=%d ttl_seconds=%d", roomID, refreshedCount, ttlSeconds)
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
		pipe.Expire(ctx, historyKey, roomHistoryTTL)
	}
	_, err = pipe.Exec(ctx)
	return err
}

func (h *RoomHandler) tryUpdateBreakMetadataInScylla(parentRoomID, originMessageID, breakRoomID string, joinCount int) {
	if h.scylla == nil || h.scylla.Session == nil {
		return
	}

	messagesTable := h.scylla.Table("messages")
	err := h.scylla.Session.Query(
		fmt.Sprintf(`UPDATE %s SET has_break_room = ?, break_room_id = ?, break_join_count = ? WHERE room_id = ? AND message_id = ?`, messagesTable),
		true,
		breakRoomID,
		joinCount,
		parentRoomID,
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

func roomChildrenKey(roomID string) string {
	return "room:" + roomID + ":children"
}

func userRoomsKey(userID string) string {
	return "user:" + userID + ":rooms"
}

func messageBreakKey(messageID string) string {
	return messageBreakPrefix + messageID
}

func roomHistoryKey(roomID string) string {
	return "room:history:" + roomID
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
	w.WriteHeader(http.StatusNotImplemented)
}
