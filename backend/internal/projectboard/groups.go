package projectboard

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/models"
)

const (
	groupsTable        = "groups"
	taskRelationsTable = "task_relations"
)

type GroupMutation struct {
	Name         string
	StartDate    string
	EndDate      string
	Description  string
	DisplayOrder *int
}

type GroupDeleteRequest struct {
	Action            string
	ReassignToGroupID string
}

type GroupSummary struct {
	models.Group
	TaskCount int `json:"task_count"`
}

type DeleteGroupResult struct {
	GroupID           string `json:"group_id"`
	GroupName         string `json:"group_name"`
	Action            string `json:"action"`
	ReassignToGroupID string `json:"reassign_to_group_id,omitempty"`
	TaskCount         int    `json:"task_count"`
	DeletedTaskCount  int    `json:"deleted_task_count"`
	ReassignedCount   int    `json:"reassigned_count"`
}

type Service struct {
	scylla *database.ScyllaStore
}

type groupTaskRow struct {
	ID        gocql.UUID
	Title     string
	Sprint    string
	GroupID   *gocql.UUID
	TaskType  string
	UpdatedAt time.Time
}

func NewService(scylla *database.ScyllaStore) *Service {
	return &Service{scylla: scylla}
}

func (s *Service) EnsureSchema(ctx context.Context) error {
	if s == nil || s.scylla == nil || s.scylla.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}

	createQuery := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		workspace_id uuid,
		group_id uuid,
		name text,
		display_order int,
		start_date text,
		end_date text,
		description text,
		created_at timestamp,
		PRIMARY KEY (workspace_id, group_id)
	) WITH CLUSTERING ORDER BY (group_id ASC)`, s.scylla.Table(groupsTable))
	if err := s.scylla.Session.Query(createQuery).WithContext(ctx).Exec(); err != nil && !isSchemaAlreadyAppliedError(err) {
		return err
	}

	for _, query := range []string{
		fmt.Sprintf(`ALTER TABLE %s ADD name text`, s.scylla.Table(groupsTable)),
		fmt.Sprintf(`ALTER TABLE %s ADD display_order int`, s.scylla.Table(groupsTable)),
		fmt.Sprintf(`ALTER TABLE %s ADD start_date text`, s.scylla.Table(groupsTable)),
		fmt.Sprintf(`ALTER TABLE %s ADD end_date text`, s.scylla.Table(groupsTable)),
		fmt.Sprintf(`ALTER TABLE %s ADD description text`, s.scylla.Table(groupsTable)),
		fmt.Sprintf(`ALTER TABLE %s ADD created_at timestamp`, s.scylla.Table(groupsTable)),
	} {
		if err := s.scylla.Session.Query(query).WithContext(ctx).Exec(); err != nil && !isSchemaAlreadyAppliedError(err) {
			return err
		}
	}

	indexQuery := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS ON %s (name)`, s.scylla.Table(groupsTable))
	if err := s.scylla.Session.Query(indexQuery).WithContext(ctx).Exec(); err != nil && !isSchemaAlreadyAppliedError(err) {
		return err
	}

	return nil
}

func (s *Service) ListGroups(ctx context.Context, roomID string) ([]models.Group, error) {
	workspaceUUID, _, err := resolveWorkspaceUUID(roomID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureTaskBackedGroups(ctx, workspaceUUID); err != nil {
		return nil, err
	}
	return s.listGroupsByWorkspaceUUID(ctx, workspaceUUID)
}

func (s *Service) ListGroupSummaries(ctx context.Context, roomID string) ([]GroupSummary, error) {
	workspaceUUID, _, err := resolveWorkspaceUUID(roomID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureTaskBackedGroups(ctx, workspaceUUID); err != nil {
		return nil, err
	}

	groups, err := s.listGroupsByWorkspaceUUID(ctx, workspaceUUID)
	if err != nil {
		return nil, err
	}
	counts, err := s.countTasksByGroup(ctx, workspaceUUID)
	if err != nil {
		return nil, err
	}

	summaries := make([]GroupSummary, 0, len(groups))
	for _, group := range groups {
		summaries = append(summaries, GroupSummary{
			Group:     group,
			TaskCount: counts[groupCountKey(group.GroupID, group.Name)],
		})
	}
	return summaries, nil
}

func (s *Service) EnsureGroupByName(ctx context.Context, roomID string, name string) (*models.Group, error) {
	if err := s.EnsureSchema(ctx); err != nil {
		return nil, err
	}

	workspaceUUID, _, err := resolveWorkspaceUUID(roomID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureTaskBackedGroups(ctx, workspaceUUID); err != nil {
		return nil, err
	}

	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return nil, fmt.Errorf("group name is required")
	}

	existing, err := s.findGroupByNameForWorkspace(ctx, workspaceUUID, trimmedName)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	nextOrder, err := s.nextDisplayOrder(ctx, workspaceUUID)
	if err != nil {
		return nil, err
	}
	groupUUID := deterministicGroupUUID(workspaceUUID, trimmedName)
	if err := s.insertGroup(ctx, workspaceUUID, groupUUID, trimmedName, nextOrder); err != nil {
		return nil, err
	}
	return s.loadGroupByUUID(ctx, workspaceUUID, groupUUID)
}

func (s *Service) CreateGroup(ctx context.Context, roomID string, input GroupMutation) (*models.Group, error) {
	if err := s.EnsureSchema(ctx); err != nil {
		return nil, err
	}

	workspaceUUID, _, err := resolveWorkspaceUUID(roomID)
	if err != nil {
		return nil, err
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, fmt.Errorf("group name is required")
	}

	existing, err := s.FindGroupByName(ctx, roomID, name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("group with this name already exists")
	}

	displayOrder := 0
	if input.DisplayOrder != nil {
		displayOrder = *input.DisplayOrder
	} else {
		nextOrder, nextErr := s.nextDisplayOrder(ctx, workspaceUUID)
		if nextErr != nil {
			return nil, nextErr
		}
		displayOrder = nextOrder
	}

	groupUUID, err := gocql.RandomUUID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate group id: %w", err)
	}

	createdAt := time.Now().UTC()
	query := fmt.Sprintf(
		`INSERT INTO %s (workspace_id, group_id, name, display_order, start_date, end_date, description, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.scylla.Table(groupsTable),
	)
	if err := s.scylla.Session.Query(
		query,
		workspaceUUID,
		groupUUID,
		name,
		displayOrder,
		nullableText(input.StartDate),
		nullableText(input.EndDate),
		nullableText(input.Description),
		createdAt,
	).WithContext(ctx).Exec(); err != nil {
		return nil, err
	}

	return &models.Group{
		WorkspaceID:  strings.TrimSpace(workspaceUUID.String()),
		GroupID:      strings.TrimSpace(groupUUID.String()),
		Name:         name,
		DisplayOrder: displayOrder,
		StartDate:    strings.TrimSpace(input.StartDate),
		EndDate:      strings.TrimSpace(input.EndDate),
		Description:  strings.TrimSpace(input.Description),
		CreatedAt:    createdAt,
	}, nil
}

func (s *Service) UpdateGroup(ctx context.Context, roomID string, groupID string, input GroupMutation) (*models.Group, error) {
	workspaceUUID, _, err := resolveWorkspaceUUID(roomID)
	if err != nil {
		return nil, err
	}
	groupUUID, err := parseFlexibleUUID(groupID)
	if err != nil {
		return nil, fmt.Errorf("invalid group id")
	}

	existing, err := s.loadGroupByUUID(ctx, workspaceUUID, groupUUID)
	if err != nil {
		return nil, err
	}

	nextName := strings.TrimSpace(input.Name)
	if nextName == "" {
		nextName = existing.Name
	}

	if groupNameKey(nextName) != groupNameKey(existing.Name) {
		byName, byNameErr := s.FindGroupByName(ctx, roomID, nextName)
		if byNameErr != nil {
			return nil, byNameErr
		}
		if byName != nil && !strings.EqualFold(strings.TrimSpace(byName.GroupID), strings.TrimSpace(existing.GroupID)) {
			return nil, fmt.Errorf("group with this name already exists")
		}
	}

	nextStartDate := strings.TrimSpace(input.StartDate)
	if nextStartDate == "" {
		nextStartDate = existing.StartDate
	}
	nextEndDate := strings.TrimSpace(input.EndDate)
	if nextEndDate == "" {
		nextEndDate = existing.EndDate
	}
	nextDescription := strings.TrimSpace(input.Description)
	if nextDescription == "" && strings.TrimSpace(existing.Description) != "" {
		nextDescription = existing.Description
	}
	nextDisplayOrder := existing.DisplayOrder
	if input.DisplayOrder != nil {
		nextDisplayOrder = *input.DisplayOrder
	}

	updateQuery := fmt.Sprintf(
		`UPDATE %s SET name = ?, display_order = ?, start_date = ?, end_date = ?, description = ? WHERE workspace_id = ? AND group_id = ?`,
		s.scylla.Table(groupsTable),
	)
	if err := s.scylla.Session.Query(
		updateQuery,
		nextName,
		nextDisplayOrder,
		nullableText(nextStartDate),
		nullableText(nextEndDate),
		nullableText(nextDescription),
		workspaceUUID,
		groupUUID,
	).WithContext(ctx).Exec(); err != nil {
		return nil, err
	}

	return s.loadGroupByUUID(ctx, workspaceUUID, groupUUID)
}

func (s *Service) DeleteGroup(ctx context.Context, roomID string, groupID string, req GroupDeleteRequest) (*DeleteGroupResult, error) {
	workspaceUUID, normalizedRoomID, err := resolveWorkspaceUUID(roomID)
	if err != nil {
		return nil, err
	}
	groupUUID, err := parseFlexibleUUID(groupID)
	if err != nil {
		return nil, fmt.Errorf("invalid group id")
	}

	group, err := s.loadGroupByUUID(ctx, workspaceUUID, groupUUID)
	if err != nil {
		return nil, err
	}

	action := normalizeDeleteAction(req.Action)
	if action == "" {
		return nil, fmt.Errorf("action must be reassign or delete_tasks")
	}

	matchingTasks, err := s.loadTasksForGroup(ctx, workspaceUUID, group)
	if err != nil {
		return nil, err
	}

	result := &DeleteGroupResult{
		GroupID:   group.GroupID,
		GroupName: group.Name,
		Action:    action,
		TaskCount: len(matchingTasks),
	}

	switch action {
	case "reassign":
		targetUUID, parseErr := parseFlexibleUUID(req.ReassignToGroupID)
		if parseErr != nil {
			return nil, fmt.Errorf("reassign_to_group_id is required")
		}
		target, targetErr := s.loadGroupByUUID(ctx, workspaceUUID, targetUUID)
		if targetErr != nil {
			return nil, targetErr
		}
		if strings.EqualFold(strings.TrimSpace(target.GroupID), strings.TrimSpace(group.GroupID)) {
			return nil, fmt.Errorf("reassign target must be a different group")
		}

		updateQuery := fmt.Sprintf(`UPDATE %s SET group_id = ?, sprint_name = ?, updated_at = ? WHERE room_id = ? AND id = ?`, s.scylla.Table("tasks"))
		now := time.Now().UTC()
		for _, task := range matchingTasks {
			if err := s.scylla.Session.Query(
				updateQuery,
				targetUUID,
				target.Name,
				now,
				workspaceUUID,
				task.ID,
			).WithContext(ctx).Exec(); err != nil {
				return nil, err
			}
		}
		result.ReassignToGroupID = target.GroupID
		result.ReassignedCount = len(matchingTasks)

	case "delete_tasks":
		deleteTaskQuery := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ? AND id = ?`, s.scylla.Table("tasks"))
		for _, task := range matchingTasks {
			if err := s.deleteTaskRelationsForTask(ctx, normalizedRoomID, strings.TrimSpace(task.ID.String())); err != nil {
				return nil, err
			}
			if err := s.scylla.Session.Query(deleteTaskQuery, workspaceUUID, task.ID).WithContext(ctx).Exec(); err != nil {
				return nil, err
			}
		}
		result.DeletedTaskCount = len(matchingTasks)
	}

	deleteGroupQuery := fmt.Sprintf(`DELETE FROM %s WHERE workspace_id = ? AND group_id = ?`, s.scylla.Table(groupsTable))
	if err := s.scylla.Session.Query(deleteGroupQuery, workspaceUUID, groupUUID).WithContext(ctx).Exec(); err != nil {
		return nil, err
	}

	return result, nil
}

func (s *Service) FindGroupByName(ctx context.Context, roomID string, name string) (*models.Group, error) {
	workspaceUUID, _, err := resolveWorkspaceUUID(roomID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureTaskBackedGroups(ctx, workspaceUUID); err != nil {
		return nil, err
	}

	return s.findGroupByNameForWorkspace(ctx, workspaceUUID, name)
}

func (s *Service) findGroupByNameForWorkspace(ctx context.Context, workspaceUUID gocql.UUID, name string) (*models.Group, error) {
	groups, err := s.listGroupsByWorkspaceUUID(ctx, workspaceUUID)
	if err != nil {
		return nil, err
	}

	targetKey := groupNameKey(name)
	for _, group := range groups {
		if groupNameKey(group.Name) == targetKey {
			groupCopy := group
			return &groupCopy, nil
		}
	}
	return nil, nil
}

func (s *Service) ensureTaskBackedGroups(ctx context.Context, workspaceUUID gocql.UUID) error {
	if s == nil || s.scylla == nil || s.scylla.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return err
	}

	groups, err := s.listGroupsByWorkspaceUUID(ctx, workspaceUUID)
	if err != nil {
		return err
	}
	existing := make(map[string]models.Group, len(groups))
	maxOrder := -1
	for _, group := range groups {
		existing[groupNameKey(group.Name)] = group
		if group.DisplayOrder > maxOrder {
			maxOrder = group.DisplayOrder
		}
	}

	taskNames, err := s.loadTaskBackedGroupNames(ctx, workspaceUUID)
	if err != nil {
		return err
	}
	if len(taskNames) == 0 {
		return nil
	}

	missingNames := make([]string, 0, len(taskNames))
	for key, name := range taskNames {
		if _, ok := existing[key]; ok {
			continue
		}
		missingNames = append(missingNames, name)
	}
	if len(missingNames) == 0 {
		return nil
	}

	sort.SliceStable(missingNames, func(i, j int) bool {
		return compareFold(missingNames[i], missingNames[j]) < 0
	})

	nextOrder := maxOrder + 1
	for _, name := range missingNames {
		groupUUID := deterministicGroupUUID(workspaceUUID, name)
		if err := s.insertGroup(ctx, workspaceUUID, groupUUID, name, nextOrder); err != nil {
			return err
		}
		nextOrder++
	}
	return nil
}

func (s *Service) loadTaskBackedGroupNames(ctx context.Context, workspaceUUID gocql.UUID) (map[string]string, error) {
	query := fmt.Sprintf(`SELECT sprint_name, task_type FROM %s WHERE room_id = ?`, s.scylla.Table("tasks"))
	iter := s.scylla.Session.Query(query, workspaceUUID).WithContext(ctx).Iter()

	names := make(map[string]string)
	var (
		sprintName string
		taskType   string
	)
	for iter.Scan(&sprintName, &taskType) {
		if normalizeTaskType(taskType) == "support" {
			continue
		}
		trimmedName := strings.TrimSpace(sprintName)
		if trimmedName == "" {
			continue
		}
		key := groupNameKey(trimmedName)
		if key == "" {
			continue
		}
		if _, exists := names[key]; !exists {
			names[key] = trimmedName
		}
	}
	if err := iter.Close(); err != nil {
		if isMissingTableError(err) {
			return nil, nil
		}
		return nil, err
	}
	return names, nil
}

func (s *Service) insertGroup(ctx context.Context, workspaceUUID gocql.UUID, groupUUID gocql.UUID, name string, displayOrder int) error {
	query := fmt.Sprintf(
		`INSERT INTO %s (workspace_id, group_id, name, display_order, created_at) VALUES (?, ?, ?, ?, ?)`,
		s.scylla.Table(groupsTable),
	)
	return s.scylla.Session.Query(
		query,
		workspaceUUID,
		groupUUID,
		strings.TrimSpace(name),
		displayOrder,
		time.Now().UTC(),
	).WithContext(ctx).Exec()
}

func (s *Service) listGroupsByWorkspaceUUID(ctx context.Context, workspaceUUID gocql.UUID) ([]models.Group, error) {
	if s == nil || s.scylla == nil || s.scylla.Session == nil {
		return nil, fmt.Errorf("scylla session is not configured")
	}

	query := fmt.Sprintf(`SELECT group_id, name, display_order, start_date, end_date, description, created_at FROM %s WHERE workspace_id = ?`, s.scylla.Table(groupsTable))
	iter := s.scylla.Session.Query(query, workspaceUUID).WithContext(ctx).Iter()

	groups := make([]models.Group, 0, 16)
	var (
		groupUUID    gocql.UUID
		name         string
		displayOrder int
		startDate    string
		endDate      string
		description  string
		createdAt    time.Time
	)
	for iter.Scan(&groupUUID, &name, &displayOrder, &startDate, &endDate, &description, &createdAt) {
		groups = append(groups, models.Group{
			WorkspaceID:  strings.TrimSpace(workspaceUUID.String()),
			GroupID:      strings.TrimSpace(groupUUID.String()),
			Name:         strings.TrimSpace(name),
			DisplayOrder: displayOrder,
			StartDate:    strings.TrimSpace(startDate),
			EndDate:      strings.TrimSpace(endDate),
			Description:  strings.TrimSpace(description),
			CreatedAt:    createdAt.UTC(),
		})
	}
	if err := iter.Close(); err != nil {
		if isMissingTableError(err) {
			return nil, nil
		}
		return nil, err
	}

	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].DisplayOrder != groups[j].DisplayOrder {
			return groups[i].DisplayOrder < groups[j].DisplayOrder
		}
		return compareFold(groups[i].Name, groups[j].Name) < 0
	})
	return groups, nil
}

func (s *Service) loadGroupByUUID(ctx context.Context, workspaceUUID gocql.UUID, groupUUID gocql.UUID) (*models.Group, error) {
	query := fmt.Sprintf(
		`SELECT name, display_order, start_date, end_date, description, created_at FROM %s WHERE workspace_id = ? AND group_id = ? LIMIT 1`,
		s.scylla.Table(groupsTable),
	)

	var (
		name         string
		displayOrder int
		startDate    string
		endDate      string
		description  string
		createdAt    time.Time
	)
	if err := s.scylla.Session.Query(query, workspaceUUID, groupUUID).WithContext(ctx).Scan(
		&name,
		&displayOrder,
		&startDate,
		&endDate,
		&description,
		&createdAt,
	); err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return nil, fmt.Errorf("group not found")
		}
		return nil, err
	}

	return &models.Group{
		WorkspaceID:  strings.TrimSpace(workspaceUUID.String()),
		GroupID:      strings.TrimSpace(groupUUID.String()),
		Name:         strings.TrimSpace(name),
		DisplayOrder: displayOrder,
		StartDate:    strings.TrimSpace(startDate),
		EndDate:      strings.TrimSpace(endDate),
		Description:  strings.TrimSpace(description),
		CreatedAt:    createdAt.UTC(),
	}, nil
}

func (s *Service) nextDisplayOrder(ctx context.Context, workspaceUUID gocql.UUID) (int, error) {
	groups, err := s.listGroupsByWorkspaceUUID(ctx, workspaceUUID)
	if err != nil {
		return 0, err
	}
	maxOrder := -1
	for _, group := range groups {
		if group.DisplayOrder > maxOrder {
			maxOrder = group.DisplayOrder
		}
	}
	return maxOrder + 1, nil
}

func (s *Service) countTasksByGroup(ctx context.Context, workspaceUUID gocql.UUID) (map[string]int, error) {
	query := fmt.Sprintf(`SELECT sprint_name, group_id, task_type FROM %s WHERE room_id = ?`, s.scylla.Table("tasks"))
	iter := s.scylla.Session.Query(query, workspaceUUID).WithContext(ctx).Iter()

	counts := make(map[string]int)
	var (
		sprintName string
		groupID    *gocql.UUID
		taskType   string
	)
	for iter.Scan(&sprintName, &groupID, &taskType) {
		if normalizeTaskType(taskType) == "support" {
			continue
		}
		groupIDText := ""
		if groupID != nil {
			groupIDText = strings.TrimSpace(groupID.String())
		}
		counts[groupCountKey(groupIDText, sprintName)]++
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return counts, nil
}

func (s *Service) loadTasksForGroup(ctx context.Context, workspaceUUID gocql.UUID, group *models.Group) ([]groupTaskRow, error) {
	if group == nil {
		return nil, fmt.Errorf("group is required")
	}

	query := fmt.Sprintf(`SELECT id, title, sprint_name, group_id, task_type, updated_at FROM %s WHERE room_id = ?`, s.scylla.Table("tasks"))
	iter := s.scylla.Session.Query(query, workspaceUUID).WithContext(ctx).Iter()

	tasks := make([]groupTaskRow, 0, 32)
	targetGroupID := strings.TrimSpace(group.GroupID)
	targetNameKey := groupNameKey(group.Name)
	var (
		taskID    gocql.UUID
		title     string
		sprint    string
		groupID   *gocql.UUID
		taskType  string
		updatedAt time.Time
	)
	for iter.Scan(&taskID, &title, &sprint, &groupID, &taskType, &updatedAt) {
		if normalizeTaskType(taskType) == "support" {
			continue
		}
		matches := false
		if groupID != nil && strings.EqualFold(strings.TrimSpace(groupID.String()), targetGroupID) {
			matches = true
		}
		if !matches && groupNameKey(sprint) == targetNameKey {
			matches = true
		}
		if !matches {
			continue
		}
		tasks = append(tasks, groupTaskRow{
			ID:        taskID,
			Title:     strings.TrimSpace(title),
			Sprint:    strings.TrimSpace(sprint),
			GroupID:   groupID,
			TaskType:  strings.TrimSpace(taskType),
			UpdatedAt: updatedAt.UTC(),
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *Service) deleteTaskRelationsForTask(ctx context.Context, roomID string, taskID string) error {
	if strings.TrimSpace(roomID) == "" || strings.TrimSpace(taskID) == "" {
		return nil
	}

	deleteOutgoingQuery := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ? AND from_task_id = ?`, s.scylla.Table(taskRelationsTable))
	if err := s.scylla.Session.Query(deleteOutgoingQuery, roomID, taskID).WithContext(ctx).Exec(); err != nil {
		if isMissingTableError(err) {
			return nil
		}
		return err
	}

	selectQuery := fmt.Sprintf(`SELECT from_task_id, to_task_id, relation_type FROM %s WHERE room_id = ?`, s.scylla.Table(taskRelationsTable))
	iter := s.scylla.Session.Query(selectQuery, roomID).WithContext(ctx).Iter()
	type relationKey struct {
		FromTaskID string
		ToTaskID   string
	}
	toDelete := make([]relationKey, 0, 8)
	var (
		fromTaskID   string
		toTaskID     string
		relationType string
	)
	for iter.Scan(&fromTaskID, &toTaskID, &relationType) {
		if normalizeRelationType(relationType) != "blocked_by" {
			continue
		}
		if strings.TrimSpace(toTaskID) != strings.TrimSpace(taskID) {
			continue
		}
		toDelete = append(toDelete, relationKey{
			FromTaskID: strings.TrimSpace(fromTaskID),
			ToTaskID:   strings.TrimSpace(toTaskID),
		})
	}
	if err := iter.Close(); err != nil {
		if isMissingTableError(err) {
			return nil
		}
		return err
	}

	deleteRelationQuery := fmt.Sprintf(`DELETE FROM %s WHERE room_id = ? AND from_task_id = ? AND to_task_id = ?`, s.scylla.Table(taskRelationsTable))
	for _, relation := range toDelete {
		if relation.FromTaskID == "" || relation.ToTaskID == "" {
			continue
		}
		if err := s.scylla.Session.Query(deleteRelationQuery, roomID, relation.FromTaskID, relation.ToTaskID).WithContext(ctx).Exec(); err != nil {
			return err
		}
	}

	return nil
}

func groupCountKey(groupID string, name string) string {
	if strings.TrimSpace(groupID) != "" {
		return "id:" + strings.TrimSpace(groupID)
	}
	return "name:" + groupNameKey(name)
}

func groupNameKey(name string) string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(name)))
	return strings.Join(fields, " ")
}

func normalizeDeleteAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "reassign":
		return "reassign"
	case "delete_tasks":
		return "delete_tasks"
	default:
		return ""
	}
}

func normalizeTaskType(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" || normalized == "sprint" || normalized == "general" {
		return "sprint"
	}
	return normalized
}

func normalizeRelationType(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, " ", "_")
	switch normalized {
	case "blocked_by", "depends_on", "dependency", "blocks", "blocked":
		return "blocked_by"
	case "subtask", "subtasks", "checklist":
		return "subtask"
	default:
		return normalized
	}
}

func compareFold(left string, right string) int {
	return strings.Compare(strings.ToLower(strings.TrimSpace(left)), strings.ToLower(strings.TrimSpace(right)))
}

func nullableText(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func resolveWorkspaceUUID(roomID string) (gocql.UUID, string, error) {
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return gocql.UUID{}, "", fmt.Errorf("room id is required")
	}
	if parsed, err := parseFlexibleUUID(strings.TrimSpace(roomID)); err == nil {
		return parsed, normalizedRoomID, nil
	}
	if parsed, err := parseFlexibleUUID(normalizedRoomID); err == nil {
		return parsed, normalizedRoomID, nil
	}
	return deterministicRoomUUID(normalizedRoomID), normalizedRoomID, nil
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

func deterministicRoomUUID(normalizedRoomID string) gocql.UUID {
	digest := sha1.Sum([]byte("converse-task-room:" + normalizedRoomID))
	uuidBytes := make([]byte, 16)
	copy(uuidBytes, digest[:16])
	uuidBytes[6] = (uuidBytes[6] & 0x0f) | 0x50
	uuidBytes[8] = (uuidBytes[8] & 0x3f) | 0x80
	compact := hex.EncodeToString(uuidBytes)
	formatted := fmt.Sprintf(
		"%s-%s-%s-%s-%s",
		compact[0:8],
		compact[8:12],
		compact[12:16],
		compact[16:20],
		compact[20:32],
	)
	parsed, err := gocql.ParseUUID(formatted)
	if err != nil {
		return gocql.UUID{}
	}
	return parsed
}

func deterministicGroupUUID(workspaceUUID gocql.UUID, name string) gocql.UUID {
	digest := sha1.Sum([]byte("converse-group:" + strings.TrimSpace(workspaceUUID.String()) + ":" + groupNameKey(name)))
	uuidBytes := make([]byte, 16)
	copy(uuidBytes, digest[:16])
	uuidBytes[6] = (uuidBytes[6] & 0x0f) | 0x50
	uuidBytes[8] = (uuidBytes[8] & 0x3f) | 0x80
	compact := hex.EncodeToString(uuidBytes)
	formatted := fmt.Sprintf(
		"%s-%s-%s-%s-%s",
		compact[0:8],
		compact[8:12],
		compact[12:16],
		compact[16:20],
		compact[20:32],
	)
	parsed, err := gocql.ParseUUID(formatted)
	if err != nil {
		return gocql.UUID{}
	}
	return parsed
}

func parseFlexibleUUID(raw string) (gocql.UUID, error) {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return gocql.UUID{}, fmt.Errorf("uuid value is required")
	}
	if parsed, err := gocql.ParseUUID(normalized); err == nil {
		return parsed, nil
	}
	compact := strings.ReplaceAll(normalized, "-", "")
	if len(compact) != 32 {
		return gocql.UUID{}, fmt.Errorf("invalid uuid value")
	}
	formatted := fmt.Sprintf("%s-%s-%s-%s-%s", compact[0:8], compact[8:12], compact[12:16], compact[16:20], compact[20:32])
	return gocql.ParseUUID(formatted)
}

func isSchemaAlreadyAppliedError(err error) bool {
	if err == nil {
		return false
	}
	lowered := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(lowered, "already exists") ||
		strings.Contains(lowered, "conflicts with an existing column") ||
		strings.Contains(lowered, "duplicate")
}

func isMissingTableError(err error) bool {
	if err == nil {
		return false
	}
	lowered := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(lowered, "unconfigured table") ||
		strings.Contains(lowered, "undefined table") ||
		strings.Contains(lowered, "cannot find table") ||
		strings.Contains(lowered, "table groups does not exist") ||
		strings.Contains(lowered, "table task_relations does not exist")
}
