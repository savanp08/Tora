package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/models"
)

type NetworkRepo struct {
	store *database.ScyllaStore
}

func NewNetworkRepo(store *database.ScyllaStore) *NetworkRepo {
	return &NetworkRepo{store: store}
}

func (r *NetworkRepo) SendRequest(ctx context.Context, fromID, toID gocql.UUID) error {
	if r == nil || r.store == nil || r.store.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	if fromID == (gocql.UUID{}) || toID == (gocql.UUID{}) {
		return fmt.Errorf("from and to user ids are required")
	}
	if fromID == toID {
		return fmt.Errorf("cannot send connection request to self")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	query := fmt.Sprintf(
		`INSERT INTO %s (user_id, target_id, status, created_at) VALUES (?, ?, ?, ?)`,
		r.store.Table("user_connections"),
	)
	return r.store.Session.Query(
		query,
		fromID,
		toID,
		"pending",
		time.Now().UTC(),
	).WithContext(ctx).Exec()
}

func (r *NetworkRepo) AcceptRequest(ctx context.Context, userID, targetID gocql.UUID) error {
	if r == nil || r.store == nil || r.store.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	if userID == (gocql.UUID{}) || targetID == (gocql.UUID{}) {
		return fmt.Errorf("user and target ids are required")
	}
	if userID == targetID {
		return fmt.Errorf("cannot accept a request from self")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	userConnectionsTable := r.store.Table("user_connections")
	now := time.Now().UTC()

	batch := r.store.Session.NewBatch(gocql.LoggedBatch)
	batch.Query(
		fmt.Sprintf(`UPDATE %s SET status = ? WHERE user_id = ? AND target_id = ?`, userConnectionsTable),
		"accepted",
		targetID,
		userID,
	)
	batch.Query(
		fmt.Sprintf(`INSERT INTO %s (user_id, target_id, status, created_at) VALUES (?, ?, ?, ?)`, userConnectionsTable),
		userID,
		targetID,
		"accepted",
		now,
	)

	return r.store.Session.ExecuteBatch(batch.WithContext(ctx))
}

func (r *NetworkRepo) GetPendingRequests(ctx context.Context, targetID gocql.UUID) ([]models.UserConnection, error) {
	if r == nil || r.store == nil || r.store.Session == nil {
		return nil, fmt.Errorf("scylla session is not configured")
	}
	if targetID == (gocql.UUID{}) {
		return nil, fmt.Errorf("target id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	query := fmt.Sprintf(
		`SELECT user_id, target_id, status, created_at FROM %s WHERE target_id = ? AND status = ? ALLOW FILTERING`,
		r.store.Table("user_connections"),
	)
	iter := r.store.Session.Query(query, targetID, "pending").WithContext(ctx).Iter()

	requests := make([]models.UserConnection, 0)
	for {
		var connection models.UserConnection
		if !iter.Scan(&connection.UserID, &connection.TargetID, &connection.Status, &connection.CreatedAt) {
			break
		}
		connection.Status = strings.TrimSpace(connection.Status)
		connection.CreatedAt = connection.CreatedAt.UTC()
		requests = append(requests, connection)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return requests, nil
}

func (r *NetworkRepo) GetConnections(ctx context.Context, userID gocql.UUID) ([]models.UserConnection, error) {
	if r == nil || r.store == nil || r.store.Session == nil {
		return nil, fmt.Errorf("scylla session is not configured")
	}
	if userID == (gocql.UUID{}) {
		return nil, fmt.Errorf("user id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	query := fmt.Sprintf(
		`SELECT user_id, target_id, status, created_at FROM %s WHERE user_id = ? AND status = ? ALLOW FILTERING`,
		r.store.Table("user_connections"),
	)
	iter := r.store.Session.Query(query, userID, "accepted").WithContext(ctx).Iter()

	connections := make([]models.UserConnection, 0)
	for {
		var connection models.UserConnection
		if !iter.Scan(&connection.UserID, &connection.TargetID, &connection.Status, &connection.CreatedAt) {
			break
		}
		connection.Status = strings.TrimSpace(connection.Status)
		connection.CreatedAt = connection.CreatedAt.UTC()
		connections = append(connections, connection)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return connections, nil
}
