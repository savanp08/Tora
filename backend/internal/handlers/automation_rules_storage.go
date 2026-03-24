package handlers

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gocql/gocql"
	industrytemplates "github.com/savanp08/converse/internal/templates"
)

const roomAutomationRuleTable = "automation_rules"

func (h *RoomHandler) ensureAutomationRuleSchema() {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return
	}

	query := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		room_id text,
		rule_id text,
		name text,
		trigger_type text,
		trigger_config text,
		action_type text,
		action_config text,
		enabled boolean,
		created_by text,
		created_at timestamp,
		PRIMARY KEY (room_id, rule_id)
	) WITH CLUSTERING ORDER BY (rule_id ASC)`, h.scylla.Table(roomAutomationRuleTable))
	if err := h.scylla.Session.Query(query).Exec(); err != nil && !isSchemaAlreadyAppliedError(err) {
		log.Printf("[automation-rules] ensure schema failed: %v", err)
	}
}

func (h *RoomHandler) deleteRoomAutomationRules(ctx context.Context, roomID string) error {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return fmt.Errorf("automation rule storage unavailable")
	}
	query := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ?`, h.scylla.Table(roomAutomationRuleTable))
	return h.scylla.Session.Query(query, roomID).WithContext(ctx).Exec()
}

func (h *RoomHandler) insertRoomAutomationRule(
	ctx context.Context,
	roomID string,
	createdBy string,
	index int,
	rule industrytemplates.TemplateRule,
) error {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return fmt.Errorf("automation rule storage unavailable")
	}

	name := strings.TrimSpace(rule.Name)
	if name == "" {
		name = fmt.Sprintf("Template Rule %d", index+1)
	}

	query := fmt.Sprintf(
		`INSERT INTO %s (room_id, rule_id, name, trigger_type, trigger_config, action_type, action_config, enabled, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.scylla.Table(roomAutomationRuleTable),
	)
	return h.scylla.Session.Query(
		query,
		roomID,
		generateRoomAutomationRuleID(roomID, name, index),
		name,
		strings.TrimSpace(rule.TriggerType),
		nullableTrimmedText(rule.TriggerConfig),
		strings.TrimSpace(rule.ActionType),
		nullableTrimmedText(rule.ActionConfig),
		true,
		nullableTrimmedText(createdBy),
		time.Now().UTC(),
	).WithContext(ctx).Exec()
}

func (h *RoomHandler) roomHasAutomationRules(ctx context.Context, roomID string) (bool, error) {
	if h == nil || h.scylla == nil || h.scylla.Session == nil {
		return false, fmt.Errorf("automation rule storage unavailable")
	}
	query := fmt.Sprintf(
		`SELECT rule_id FROM %s WHERE room_id = ? LIMIT 1`,
		h.scylla.Table(roomAutomationRuleTable),
	)
	var ruleID string
	err := h.scylla.Session.Query(query, roomID).WithContext(ctx).Scan(&ruleID)
	if err == nil {
		return strings.TrimSpace(ruleID) != "", nil
	}
	if err == gocql.ErrNotFound {
		return false, nil
	}
	return false, err
}

func generateRoomAutomationRuleID(roomID string, name string, index int) string {
	digest := sha1.Sum([]byte(strings.TrimSpace(roomID) + ":" + strings.TrimSpace(name) + fmt.Sprintf(":%d", index)))
	return hex.EncodeToString(digest[:12])
}
