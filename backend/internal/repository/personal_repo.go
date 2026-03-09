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

type PersonalRepo struct {
	store *database.ScyllaStore
}

func NewPersonalRepo(store *database.ScyllaStore) *PersonalRepo {
	return &PersonalRepo{store: store}
}

func (r *PersonalRepo) CreateItem(ctx context.Context, item models.PersonalItem) error {
	if r == nil || r.store == nil || r.store.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	if item.UserID == (gocql.UUID{}) {
		return fmt.Errorf("user id is required")
	}
	if item.ItemID == (gocql.UUID{}) {
		return fmt.Errorf("item id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	createdAt := item.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	query := fmt.Sprintf(
		`INSERT INTO %s (user_id, item_id, type, content, status, due_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.store.Table("personal_items"),
	)
	return r.store.Session.Query(
		query,
		item.UserID,
		item.ItemID,
		strings.TrimSpace(item.Type),
		strings.TrimSpace(item.Content),
		strings.TrimSpace(item.Status),
		item.DueAt,
		createdAt,
	).WithContext(ctx).Exec()
}

func (r *PersonalRepo) GetItemsByUserID(ctx context.Context, userID gocql.UUID) ([]models.PersonalItem, error) {
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
		`SELECT item_id, type, content, status, due_at, created_at FROM %s WHERE user_id = ?`,
		r.store.Table("personal_items"),
	)
	iter := r.store.Session.Query(query, userID).WithContext(ctx).Iter()

	items := make([]models.PersonalItem, 0)
	for {
		var (
			itemID    gocql.UUID
			itemType  string
			content   string
			status    string
			dueAt     *time.Time
			createdAt time.Time
		)
		if !iter.Scan(&itemID, &itemType, &content, &status, &dueAt, &createdAt) {
			break
		}
		items = append(items, models.PersonalItem{
			UserID:    userID,
			ItemID:    itemID,
			Type:      itemType,
			Content:   content,
			Status:    status,
			DueAt:     dueAt,
			CreatedAt: createdAt,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *PersonalRepo) UpdateItemStatus(ctx context.Context, userID gocql.UUID, itemID gocql.UUID, status string) error {
	if r == nil || r.store == nil || r.store.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	if userID == (gocql.UUID{}) {
		return fmt.Errorf("user id is required")
	}
	if itemID == (gocql.UUID{}) {
		return fmt.Errorf("item id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	query := fmt.Sprintf(
		`UPDATE %s SET status = ? WHERE user_id = ? AND item_id = ?`,
		r.store.Table("personal_items"),
	)
	return r.store.Session.Query(query, strings.TrimSpace(status), userID, itemID).WithContext(ctx).Exec()
}

func (r *PersonalRepo) DeleteItem(ctx context.Context, userID gocql.UUID, itemID gocql.UUID) error {
	if r == nil || r.store == nil || r.store.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	if userID == (gocql.UUID{}) {
		return fmt.Errorf("user id is required")
	}
	if itemID == (gocql.UUID{}) {
		return fmt.Errorf("item id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	query := fmt.Sprintf(
		`DELETE FROM %s WHERE user_id = ? AND item_id = ?`,
		r.store.Table("personal_items"),
	)
	return r.store.Session.Query(query, userID, itemID).WithContext(ctx).Exec()
}
