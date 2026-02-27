package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/savanp08/converse/internal/models"
)

func (h *RoomHandler) ClearBoardElements(ctx context.Context, roomID string) error {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return fmt.Errorf("board storage unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return fmt.Errorf("invalid room id")
	}

	boardTable := h.scylla.Table("board_elements")
	query := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ?`, boardTable)
	return h.scylla.Session.Query(query, normalizedRoomID).WithContext(ctx).Exec()
}

func (h *RoomHandler) GetBoardElements(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Board storage unavailable"})
		return
	}

	roomID := normalizeRoomID(firstNonEmpty(chi.URLParam(r, "roomId"), chi.URLParam(r, "id")))
	if roomID == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Invalid room id"})
		return
	}

	boardTable := h.scylla.Table("board_elements")
	query := fmt.Sprintf(
		`SELECT room_id, element_id, type, x, y, width, height, content, z_index, created_by_user_id, created_by_name, created_at FROM %s WHERE room_id = ?`,
		boardTable,
	)
	iter := h.scylla.Session.Query(query, roomID).WithContext(r.Context()).Iter()
	elements := make([]models.BoardElement, 0, 256)
	var (
		scanRoomID  string
		elementID   string
		elementType string
		x           float32
		y           float32
		width       float32
		height      float32
		content     string
		zIndex      int
		createdByID string
		createdBy   string
		elementTime time.Time
	)
	for iter.Scan(
		&scanRoomID,
		&elementID,
		&elementType,
		&x,
		&y,
		&width,
		&height,
		&content,
		&zIndex,
		&createdByID,
		&createdBy,
		&elementTime,
	) {
		elements = append(elements, models.BoardElement{
			RoomID:          normalizeRoomID(scanRoomID),
			ElementID:       normalizeMessageID(elementID),
			Type:            elementType,
			X:               x,
			Y:               y,
			Width:           width,
			Height:          height,
			Content:         content,
			ZIndex:          zIndex,
			CreatedByUserID: strings.TrimSpace(createdByID),
			CreatedByName:   strings.TrimSpace(createdBy),
			CreatedAt:       elementTime.UTC(),
		})
	}
	if err := iter.Close(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to load board elements"})
		return
	}

	sort.SliceStable(elements, func(i, j int) bool {
		if elements[i].ZIndex == elements[j].ZIndex {
			return elements[i].CreatedAt.Before(elements[j].CreatedAt)
		}
		return elements[i].ZIndex < elements[j].ZIndex
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(elements)
}
