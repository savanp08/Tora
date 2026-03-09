package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/database"
)

type RoomRepository struct {
	scylla *database.ScyllaStore
}

func NewRoomRepository(scyllaStore *database.ScyllaStore) *RoomRepository {
	return &RoomRepository{scylla: scyllaStore}
}

func (r *RoomRepository) CreatePersistentRoom(
	ctx context.Context,
	userID gocql.UUID,
	roomName string,
) (gocql.UUID, error) {
	if r == nil || r.scylla == nil || r.scylla.Session == nil {
		return gocql.UUID{}, fmt.Errorf("scylla session is not configured")
	}
	if userID == (gocql.UUID{}) {
		return gocql.UUID{}, fmt.Errorf("user id is required")
	}

	normalizedRoomName := strings.TrimSpace(roomName)
	if normalizedRoomName == "" {
		return gocql.UUID{}, fmt.Errorf("room name is required")
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return gocql.UUID{}, err
	}

	roomID, err := gocql.RandomUUID()
	if err != nil {
		return gocql.UUID{}, fmt.Errorf("generate room id: %w", err)
	}

	now := time.Now().UTC()
	roomsTable := r.scylla.Table("rooms")
	userRoomsTable := r.scylla.Table("user_rooms")

	batch := r.scylla.Session.NewBatch(gocql.LoggedBatch)
	batch.Query(
		fmt.Sprintf(
			`INSERT INTO %s (id, name, owner_id, is_ephemeral, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			roomsTable,
		),
		roomID,
		normalizedRoomName,
		userID,
		false,
		nil,
		now,
	)
	batch.Query(
		fmt.Sprintf(
			`INSERT INTO %s (user_id, room_id, room_name, role, joined_at, last_accessed) VALUES (?, ?, ?, ?, ?, ?)`,
			userRoomsTable,
		),
		userID,
		roomID,
		normalizedRoomName,
		"owner",
		now,
		now,
	)

	if err := r.scylla.Session.ExecuteBatch(batch); err != nil {
		return gocql.UUID{}, fmt.Errorf("create persistent room batch failed: %w", err)
	}

	return roomID, nil
}
