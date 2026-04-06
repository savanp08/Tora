package ai

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/models"
	"github.com/savanp08/converse/internal/security"
)

const (
	defaultChatMessageLimit   = 20
	defaultTaskLimit          = 500
	defaultCanvasExcerptLines = 50

	roomMessageSoftExpiryTable = "room_message_soft_expiry"
)

type WorkspaceContext struct {
	RoomID         string
	RoomName       string
	ProjectType    string
	RequestedBy    UserCtx
	Members        []UserCtx
	Tasks          []TaskCtx
	SupportTickets []TaskCtx
	Sprints        []SprintCtx
	CanvasFiles    []CanvasFileCtx
	RecentMessages []MessageCtx
	RoomSummary    string
	Timestamp      time.Time
}

type UserCtx struct {
	ID       string
	Username string
	FullName string
	IsOwner  bool
}

type TaskCtx struct {
	ID           string
	TaskNumber   int // short sequential number within the room; 0 = not assigned
	Title        string
	Description  string
	Status       string
	TaskType     string
	SprintName   string
	AssigneeID   string
	AssigneeName string
	Budget       *float64
	ActualCost   *float64
	StartDate    *time.Time
	DueDate      *time.Time
	Roles        []RoleCtx
	Subtasks     []SubtaskCtx
	BlockedBy    []string
	Blocks       []string
	CustomFields map[string]any
	UpdatedAt    time.Time
}

type RoleCtx struct {
	Role             string
	Responsibilities string
}

type SubtaskCtx struct {
	Content   string
	Completed bool
}

type SprintCtx struct {
	Name       string
	TaskCount  int
	Done       int
	InProgress int
	Todo       int
}

type CanvasFileCtx struct {
	Path     string
	Language string
	Lines    int
	Excerpt  string
}

type MessageCtx struct {
	SenderName string
	Content    string
	Timestamp  time.Time
}

type BuildOptions struct {
	IncludeCanvas      bool
	IncludeChat        bool
	ChatMessageLimit   int
	TaskLimit          int
	CanvasExcerptLines int
}

type ContextBuilder struct {
	scylla *database.ScyllaStore
	redis  *database.RedisStore // optional — enables task number generation
}

func NewContextBuilder(scylla *database.ScyllaStore) *ContextBuilder {
	return &ContextBuilder{scylla: scylla}
}

// WithRedis attaches an optional Redis store used for atomic task number generation.
func (cb *ContextBuilder) WithRedis(redis *database.RedisStore) *ContextBuilder {
	cb.redis = redis
	return cb
}

// IncrTaskNumber atomically increments the task counter for a room.
// Returns 0 if Redis is not configured (task_number will not be set).
func (cb *ContextBuilder) IncrTaskNumber(ctx context.Context, roomID string) int {
	if cb == nil || cb.redis == nil {
		return 0
	}
	n, err := cb.redis.IncrTaskNumber(ctx, roomID)
	if err != nil {
		log.Printf("[ai/ctx] task number incr failed room=%s: %v", roomID, err)
		return 0
	}
	return n
}

func (cb *ContextBuilder) Build(ctx context.Context, roomID string, requestedByID string, opts BuildOptions) (*WorkspaceContext, error) {
	if cb == nil || cb.scylla == nil || cb.scylla.Session == nil {
		return nil, fmt.Errorf("scylla store is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	normalizedRoomID := normalizeContextRoomID(roomID)
	if normalizedRoomID == "" {
		return nil, fmt.Errorf("room id is required")
	}

	opts = normalizeBuildOptions(opts)
	workspace := &WorkspaceContext{
		RoomID:      normalizedRoomID,
		RoomName:    normalizedRoomID,
		ProjectType: models.DefaultProjectType,
		Timestamp:   time.Now().UTC(),
	}

	roomUUID, _, roomUUIDErr := resolveContextTaskRoomUUID(roomID)

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)

	run := func(label string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("[ai/context] %s load failed room=%s err=%v", label, normalizedRoomID, err)
			}
		}()
	}

	run("room_meta", func() error {
		roomName, roomSummary, projectType, err := cb.loadRoomMeta(ctx, normalizedRoomID)
		if err != nil {
			return err
		}
		mu.Lock()
		if roomName != "" {
			workspace.RoomName = roomName
		}
		if strings.TrimSpace(projectType) != "" {
			workspace.ProjectType = models.NormalizeProjectType(projectType)
		}
		workspace.RoomSummary = roomSummary
		mu.Unlock()
		return nil
	})

	run("members", func() error {
		members, err := cb.loadMembers(ctx, roomID, normalizedRoomID)
		if err != nil {
			return err
		}
		mu.Lock()
		workspace.Members = members
		mu.Unlock()
		return nil
	})

	if opts.TaskLimit != 0 {
		run("tasks", func() error {
			if roomUUIDErr != nil {
				return roomUUIDErr
			}
			tasks, supportTickets, sprints, err := cb.loadTasks(ctx, roomUUID, opts.TaskLimit)
			if err != nil {
				return err
			}
			mu.Lock()
			workspace.Tasks = tasks
			workspace.SupportTickets = supportTickets
			workspace.Sprints = sprints
			mu.Unlock()
			return nil
		})
	}

	if opts.IncludeCanvas {
		run("canvas_files", func() error {
			files, err := cb.loadCanvasFiles(ctx, normalizedRoomID, opts.CanvasExcerptLines)
			if err != nil {
				return err
			}
			mu.Lock()
			workspace.CanvasFiles = files
			mu.Unlock()
			return nil
		})
	}

	if opts.IncludeChat {
		run("recent_messages", func() error {
			messages, err := cb.loadRecentMessages(ctx, normalizedRoomID, opts.ChatMessageLimit)
			if err != nil {
				return err
			}
			mu.Lock()
			workspace.RecentMessages = messages
			mu.Unlock()
			return nil
		})
	}

	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	workspace.RequestedBy = cb.resolveRequestedBy(ctx, requestedByID, workspace.Members)
	sortWorkspaceContext(workspace)

	return workspace, nil
}

func (wc *WorkspaceContext) RenderForAI(opts BuildOptions) string {
	if wc == nil {
		return ""
	}

	opts = normalizeBuildOptions(opts)

	tasks := append([]TaskCtx(nil), wc.Tasks...)
	supportTickets := append([]TaskCtx(nil), wc.SupportTickets...)
	sprints := append([]SprintCtx(nil), wc.Sprints...)
	canvasFiles := append([]CanvasFileCtx(nil), wc.CanvasFiles...)
	recentMessages := append([]MessageCtx(nil), wc.RecentMessages...)
	members := append([]UserCtx(nil), wc.Members...)

	sort.SliceStable(tasks, func(i, j int) bool { return taskCtxLess(tasks[i], tasks[j]) })
	sort.SliceStable(supportTickets, func(i, j int) bool { return taskCtxLess(supportTickets[i], supportTickets[j]) })
	sort.SliceStable(sprints, func(i, j int) bool { return sprintCtxLess(sprints[i], sprints[j]) })
	sort.SliceStable(canvasFiles, func(i, j int) bool {
		return compareFold(canvasFiles[i].Path, canvasFiles[j].Path) < 0
	})
	sort.SliceStable(recentMessages, func(i, j int) bool {
		if recentMessages[i].Timestamp.Equal(recentMessages[j].Timestamp) {
			return compareFold(recentMessages[i].SenderName, recentMessages[j].SenderName) < 0
		}
		return recentMessages[i].Timestamp.Before(recentMessages[j].Timestamp)
	})
	sort.SliceStable(members, func(i, j int) bool { return userCtxLess(members[i], members[j]) })

	var sb strings.Builder
	sb.WriteString("=== WORKSPACE CONTEXT ===\n")
	sb.WriteString(fmt.Sprintf("Room: %s\n", firstContextValue(strings.TrimSpace(wc.RoomName), wc.RoomID)))
	sb.WriteString(fmt.Sprintf("Room ID: %s\n", strings.TrimSpace(wc.RoomID)))
	sb.WriteString(fmt.Sprintf("Timestamp: %s\n", wc.Timestamp.UTC().Format(time.RFC3339)))

	if requestedByLabel := formatUserLabel(wc.RequestedBy); requestedByLabel != "" {
		sb.WriteString(fmt.Sprintf("Requested by: %s\n", requestedByLabel))
	}

	if len(members) > 0 {
		sb.WriteString(fmt.Sprintf("Members: %d\n", len(members)))
		for _, member := range members {
			sb.WriteString("  - ")
			sb.WriteString(formatUserLabel(member))
			if member.IsOwner {
				sb.WriteString(" [owner]")
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString(fmt.Sprintf("Total tasks: %d (support tickets excluded)\n", len(tasks)))
	sb.WriteString("Tasks:\n")
	if len(tasks) == 0 {
		sb.WriteString("  (none)\n")
	} else {
		for _, task := range tasks {
			sb.WriteString("  - ")
			sb.WriteString(renderTaskLine(task))
			sb.WriteString("\n")
		}
	}

	sb.WriteString(fmt.Sprintf("Support tickets: %d (separate from tasks)\n", len(supportTickets)))
	if len(supportTickets) > 0 {
		sb.WriteString("Support Tickets:\n")
		for _, ticket := range supportTickets {
			sb.WriteString("  - ")
			sb.WriteString(renderTaskLine(ticket))
			sb.WriteString("\n")
		}
	}

	if len(sprints) > 0 {
		sb.WriteString("Sprints:\n")
		for _, sprint := range sprints {
			sb.WriteString(fmt.Sprintf(
				"  Sprint %q - %d tasks (todo=%d, in_progress=%d, done=%d)\n",
				sprint.Name,
				sprint.TaskCount,
				sprint.Todo,
				sprint.InProgress,
				sprint.Done,
			))
		}
	}

	if summary := collapseWhitespace(wc.RoomSummary); summary != "" {
		sb.WriteString("Room summary:\n")
		sb.WriteString("  ")
		sb.WriteString(summary)
		sb.WriteString("\n")
	}

	if opts.IncludeCanvas && len(canvasFiles) > 0 {
		sb.WriteString(fmt.Sprintf("Canvas files: %d\n", len(canvasFiles)))
		for _, file := range canvasFiles {
			sb.WriteString(fmt.Sprintf("  - %s [%s] %d lines\n", file.Path, firstContextValue(file.Language, "plaintext"), file.Lines))
			if strings.TrimSpace(file.Excerpt) != "" {
				sb.WriteString("    excerpt:\n")
				for _, line := range strings.Split(file.Excerpt, "\n") {
					sb.WriteString("      ")
					sb.WriteString(line)
					sb.WriteString("\n")
				}
			}
		}
	}

	if opts.IncludeChat && len(recentMessages) > 0 {
		sb.WriteString(fmt.Sprintf("Recent chat: %d\n", len(recentMessages)))
		for _, message := range recentMessages {
			label := firstContextValue(strings.TrimSpace(message.SenderName), "Unknown")
			content := truncateWithEllipsis(collapseWhitespace(message.Content), 120)
			sb.WriteString(fmt.Sprintf(
				"  [%s] %s: %s\n",
				message.Timestamp.UTC().Format("15:04"),
				label,
				content,
			))
		}
	}

	sb.WriteString("=== END WORKSPACE CONTEXT ===\n")
	return sb.String()
}

func (cb *ContextBuilder) loadRoomMeta(ctx context.Context, roomID string) (string, string, string, error) {
	query := fmt.Sprintf(`SELECT name, project_type FROM %s WHERE room_id = ? LIMIT 1`, cb.scylla.Table("rooms"))
	var roomName string
	var projectType string
	if err := cb.scylla.Session.Query(query, roomID).WithContext(ctx).Scan(&roomName, &projectType); err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return "", "", "", err
	}

	summary, err := cb.scylla.GetRoomSummary(ctx, roomID)
	if err != nil {
		return strings.TrimSpace(roomName), "", models.NormalizeProjectType(projectType), err
	}
	return strings.TrimSpace(roomName), strings.TrimSpace(summary), models.NormalizeProjectType(projectType), nil
}

func (cb *ContextBuilder) loadMembers(ctx context.Context, rawRoomID, normalizedRoomID string) ([]UserCtx, error) {
	roleByUserID := make(map[string]string)

	if roomUUID, _, err := resolveContextTaskRoomUUID(rawRoomID); err == nil {
		query := fmt.Sprintf(
			`SELECT user_id, role FROM %s WHERE room_id = ? ALLOW FILTERING`,
			cb.scylla.Table("user_rooms"),
		)
		iter := cb.scylla.Session.Query(query, roomUUID).WithContext(ctx).Iter()
		var (
			userID gocql.UUID
			role   string
		)
		for iter.Scan(&userID, &role) {
			userIDStr := strings.TrimSpace(userID.String())
			if userIDStr == "" {
				continue
			}
			mergeRole(roleByUserID, userIDStr, role)
		}
		if err := iter.Close(); err != nil {
			return nil, err
		}
	}

	query := fmt.Sprintf(
		`SELECT user_id, role FROM %s WHERE room_id = ? ALLOW FILTERING`,
		cb.scylla.Table("user_rooms_text"),
	)
	iter := cb.scylla.Session.Query(query, normalizedRoomID).WithContext(ctx).Iter()
	var (
		userID gocql.UUID
		role   string
	)
	for iter.Scan(&userID, &role) {
		userIDStr := strings.TrimSpace(userID.String())
		if userIDStr == "" {
			continue
		}
		mergeRole(roleByUserID, userIDStr, role)
	}
	if err := iter.Close(); err != nil && !isMissingTableError(err) {
		return nil, err
	}

	if len(roleByUserID) == 0 {
		return nil, nil
	}

	userMap, err := cb.loadUsersByIDs(ctx, sortedKeys(roleByUserID))
	if err != nil {
		return nil, err
	}

	members := make([]UserCtx, 0, len(roleByUserID))
	for _, userID := range sortedKeys(roleByUserID) {
		member, ok := userMap[userID]
		if !ok {
			member = UserCtx{ID: userID}
		}
		member.IsOwner = strings.EqualFold(strings.TrimSpace(roleByUserID[userID]), "owner")
		members = append(members, member)
	}

	sort.SliceStable(members, func(i, j int) bool { return userCtxLess(members[i], members[j]) })
	return members, nil
}

func (cb *ContextBuilder) loadTasks(ctx context.Context, roomUUID gocql.UUID, taskLimit int) ([]TaskCtx, []TaskCtx, []SprintCtx, error) {
	type taskRow struct {
		ID           string
		TaskNumber   int
		Title        string
		Description  string
		Status       string
		TaskType     string
		SprintName   string
		AssigneeID   string
		CustomFields *string
		DueDate      *time.Time
		StartDate    *time.Time
		RolesRaw     *string
		UpdatedAt    time.Time
	}

	query := fmt.Sprintf(
		`SELECT id, task_number, title, description, status, task_type, sprint_name, assignee_id, custom_fields, due_date, start_date, roles, updated_at FROM %s WHERE room_id = ? LIMIT %d`,
		cb.scylla.Table("tasks"),
		taskLimit,
	)
	iter := cb.scylla.Session.Query(query, roomUUID).WithContext(ctx).Iter()

	rows := make([]taskRow, 0, minInt(taskLimit, 128))
	assigneeIDs := make(map[string]struct{})

	var (
		taskID       gocql.UUID
		taskNumber   *int
		title        string
		description  string
		status       string
		taskType     string
		sprintName   string
		assigneeID   *gocql.UUID
		customFields *string
		dueDate      *time.Time
		startDate    *time.Time
		rolesRaw     *string
		updatedAt    time.Time
	)
	for iter.Scan(
		&taskID,
		&taskNumber,
		&title,
		&description,
		&status,
		&taskType,
		&sprintName,
		&assigneeID,
		&customFields,
		&dueDate,
		&startDate,
		&rolesRaw,
		&updatedAt,
	) {
		row := taskRow{
			ID:           strings.TrimSpace(taskID.String()),
			Title:        strings.TrimSpace(title),
			Description:  strings.TrimSpace(description),
			Status:       normalizeContextTaskStatus(status),
			TaskType:     normalizeContextTaskType(taskType),
			SprintName:   strings.TrimSpace(sprintName),
			CustomFields: customFields,
			DueDate:      cloneTimePtr(dueDate),
			StartDate:    cloneTimePtr(startDate),
			RolesRaw:     rolesRaw,
			UpdatedAt:    updatedAt.UTC(),
		}
		if taskNumber != nil {
			row.TaskNumber = *taskNumber
		}
		if assigneeID != nil {
			row.AssigneeID = strings.TrimSpace(assigneeID.String())
			if row.AssigneeID != "" {
				assigneeIDs[row.AssigneeID] = struct{}{}
			}
		}
		rows = append(rows, row)
	}
	if err := iter.Close(); err != nil {
		return nil, nil, nil, err
	}

	assigneeNames, err := cb.loadUserDisplayNames(ctx, sortedKeysFromSet(assigneeIDs))
	if err != nil {
		return nil, nil, nil, err
	}

	relationSnapshot, err := cb.loadTaskRelations(ctx, roomUUID)
	if err != nil {
		return nil, nil, nil, err
	}

	tasks := make([]TaskCtx, 0, len(rows))
	supportTickets := make([]TaskCtx, 0, len(rows))
	for _, row := range rows {
		customFieldsMap := parseJSONMap(row.CustomFields)
		task := TaskCtx{
			ID:           row.ID,
			TaskNumber:   row.TaskNumber,
			Title:        firstContextValue(row.Title, "(untitled task)"),
			Description:  row.Description,
			Status:       row.Status,
			TaskType:     row.TaskType,
			SprintName:   row.SprintName,
			AssigneeID:   row.AssigneeID,
			AssigneeName: assigneeNames[row.AssigneeID],
			Budget:       extractTaskBudget(row.Description, customFieldsMap),
			ActualCost:   extractTaskActualCost(row.Description, customFieldsMap),
			StartDate:    cloneTimePtr(row.StartDate),
			DueDate:      cloneTimePtr(row.DueDate),
			Roles:        parseRoleContexts(row.RolesRaw),
			Subtasks:     cloneSubtasks(relationSnapshot.Subtasks[row.ID]),
			BlockedBy:    cloneStringSlice(relationSnapshot.BlockedBy[row.ID]),
			Blocks:       cloneStringSlice(relationSnapshot.Blocks[row.ID]),
			CustomFields: cloneStringAnyMap(customFieldsMap),
			UpdatedAt:    row.UpdatedAt,
		}
		if task.TaskType == "support" {
			supportTickets = append(supportTickets, task)
		} else {
			tasks = append(tasks, task)
		}
	}

	sort.SliceStable(tasks, func(i, j int) bool { return taskCtxLess(tasks[i], tasks[j]) })
	sort.SliceStable(supportTickets, func(i, j int) bool { return taskCtxLess(supportTickets[i], supportTickets[j]) })

	sprints := deriveSprintContexts(tasks)
	return tasks, supportTickets, sprints, nil
}

type contextTaskRelationSnapshot struct {
	BlockedBy map[string][]string
	Blocks    map[string][]string
	Subtasks  map[string][]SubtaskCtx
}

func (cb *ContextBuilder) loadTaskRelations(ctx context.Context, roomUUID gocql.UUID) (contextTaskRelationSnapshot, error) {
	query := fmt.Sprintf(
		`SELECT from_task_id, to_task_id, relation_type, position, content, completed FROM %s WHERE room_id = ?`,
		cb.scylla.Table("task_relations"),
	)
	iter := cb.scylla.Session.Query(query, roomUUID.String()).WithContext(ctx).Iter()

	type subtaskRow struct {
		Position int
		Subtask  SubtaskCtx
	}

	snapshot := contextTaskRelationSnapshot{
		BlockedBy: make(map[string][]string),
		Blocks:    make(map[string][]string),
		Subtasks:  make(map[string][]SubtaskCtx),
	}
	rowsByTask := make(map[string][]subtaskRow)
	var (
		fromTaskID   string
		relationType string
		toTaskID     string
		position     int
		content      string
		completed    bool
	)
	for iter.Scan(&fromTaskID, &toTaskID, &relationType, &position, &content, &completed) {
		taskID := strings.TrimSpace(fromTaskID)
		if taskID == "" {
			continue
		}
		switch strings.TrimSpace(relationType) {
		case "subtask":
			rowsByTask[taskID] = append(rowsByTask[taskID], subtaskRow{
				Position: position,
				Subtask: SubtaskCtx{
					Content:   firstContextValue(strings.TrimSpace(content), "Subtask"),
					Completed: completed,
				},
			})
		case "blocked_by":
			targetTaskID := strings.TrimSpace(toTaskID)
			if targetTaskID == "" || targetTaskID == taskID {
				continue
			}
			snapshot.BlockedBy[taskID] = append(snapshot.BlockedBy[taskID], targetTaskID)
			snapshot.Blocks[targetTaskID] = append(snapshot.Blocks[targetTaskID], taskID)
		}
	}
	if err := iter.Close(); err != nil {
		if isMissingTableError(err) {
			return snapshot, nil
		}
		return snapshot, err
	}

	for taskID, rows := range rowsByTask {
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Position != rows[j].Position {
				return rows[i].Position < rows[j].Position
			}
			return compareFold(rows[i].Subtask.Content, rows[j].Subtask.Content) < 0
		})
		subtasks := make([]SubtaskCtx, 0, len(rows))
		for _, row := range rows {
			subtasks = append(subtasks, row.Subtask)
		}
		snapshot.Subtasks[taskID] = subtasks
	}

	for taskID, relationIDs := range snapshot.BlockedBy {
		snapshot.BlockedBy[taskID] = uniqueSortedContextIDs(relationIDs)
	}
	for taskID, relationIDs := range snapshot.Blocks {
		snapshot.Blocks[taskID] = uniqueSortedContextIDs(relationIDs)
	}

	return snapshot, nil
}

func (cb *ContextBuilder) loadRecentMessages(ctx context.Context, roomID string, limit int) ([]MessageCtx, error) {
	if limit <= 0 {
		return nil, nil
	}

	softCutoff := time.Unix(0, 0).UTC()
	if cutoff, err := cb.loadRoomMessageSoftCutoff(ctx, roomID); err == nil && !cutoff.IsZero() {
		softCutoff = cutoff
	}

	query := fmt.Sprintf(
		`SELECT sender_name, content, created_at FROM %s WHERE room_id = ? AND created_at >= ? ORDER BY created_at DESC LIMIT %d`,
		cb.scylla.Table("messages"),
		limit,
	)
	iter := cb.scylla.Session.Query(query, roomID, softCutoff).WithContext(ctx).Iter()

	messages := make([]MessageCtx, 0, limit)
	var (
		senderName string
		content    string
		createdAt  time.Time
	)
	for iter.Scan(&senderName, &content, &createdAt) {
		if decrypted, err := security.DecryptMessage(content); err == nil {
			content = decrypted
		}
		messages = append(messages, MessageCtx{
			SenderName: firstContextValue(strings.TrimSpace(senderName), "Unknown"),
			Content:    strings.TrimSpace(content),
			Timestamp:  createdAt.UTC(),
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}

	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}

	return messages, nil
}

func (cb *ContextBuilder) loadRoomMessageSoftCutoff(ctx context.Context, roomID string) (time.Time, error) {
	query := fmt.Sprintf(
		`SELECT extended_expiry_time FROM %s WHERE room_id = ? LIMIT 1`,
		cb.scylla.Table(roomMessageSoftExpiryTable),
	)
	var cutoff *time.Time
	if err := cb.scylla.Session.Query(query, roomID).WithContext(ctx).Scan(&cutoff); err != nil {
		if errors.Is(err, gocql.ErrNotFound) || isMissingTableError(err) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	if cutoff == nil || cutoff.IsZero() {
		return time.Time{}, nil
	}
	return cutoff.UTC(), nil
}

func (cb *ContextBuilder) loadCanvasFiles(ctx context.Context, roomID string, excerptLines int) ([]CanvasFileCtx, error) {
	query := fmt.Sprintf(
		`SELECT path, language, content FROM %s WHERE room_id = ?`,
		cb.scylla.Table("canvas_files"),
	)
	iter := cb.scylla.Session.Query(query, roomID).WithContext(ctx).Iter()

	files := make([]CanvasFileCtx, 0, 16)
	var (
		path     string
		language string
		content  string
	)
	for iter.Scan(&path, &language, &content) {
		excerpt, lineCount := excerptFirstLines(content, excerptLines)
		files = append(files, CanvasFileCtx{
			Path:     strings.TrimSpace(path),
			Language: strings.TrimSpace(language),
			Lines:    lineCount,
			Excerpt:  excerpt,
		})
	}
	if err := iter.Close(); err != nil {
		if isMissingTableError(err) {
			return nil, nil
		}
		return nil, err
	}

	sort.SliceStable(files, func(i, j int) bool {
		return compareFold(files[i].Path, files[j].Path) < 0
	})
	return files, nil
}

func (cb *ContextBuilder) resolveRequestedBy(ctx context.Context, requestedByID string, members []UserCtx) UserCtx {
	requestedByID = strings.TrimSpace(requestedByID)
	if requestedByID == "" {
		return UserCtx{}
	}

	requestedByUsername := normalizeContextUsername(requestedByID)
	requestedByIDLower := strings.ToLower(requestedByID)
	for _, member := range members {
		if strings.EqualFold(strings.TrimSpace(member.ID), requestedByIDLower) || normalizeContextUsername(member.Username) == requestedByUsername {
			return member
		}
	}

	user, err := cb.loadUserByReference(ctx, requestedByID)
	if err == nil {
		for _, member := range members {
			if strings.EqualFold(strings.TrimSpace(member.ID), strings.TrimSpace(user.ID)) {
				user.IsOwner = member.IsOwner
				return user
			}
		}
		return user
	}

	return UserCtx{ID: requestedByID}
}

func (cb *ContextBuilder) loadUserByReference(ctx context.Context, reference string) (UserCtx, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return UserCtx{}, fmt.Errorf("user reference is required")
	}

	if parsed, err := parseFlexibleUUID(reference); err == nil {
		return cb.loadUserByUUID(ctx, parsed)
	}

	usernames := []string{reference}
	if normalized := normalizeContextUsername(reference); normalized != "" && !containsString(usernames, normalized) {
		usernames = append(usernames, normalized)
	}

	query := fmt.Sprintf(`SELECT user_id FROM %s WHERE username = ? LIMIT 1`, cb.scylla.Table("users_by_username"))
	for _, username := range usernames {
		var userID gocql.UUID
		if err := cb.scylla.Session.Query(query, username).WithContext(ctx).Scan(&userID); err != nil {
			if errors.Is(err, gocql.ErrNotFound) {
				continue
			}
			return UserCtx{}, err
		}
		return cb.loadUserByUUID(ctx, userID)
	}

	return UserCtx{}, fmt.Errorf("user not found")
}

func (cb *ContextBuilder) loadUserByUUID(ctx context.Context, userID gocql.UUID) (UserCtx, error) {
	query := fmt.Sprintf(`SELECT username, full_name FROM %s WHERE id = ? LIMIT 1`, cb.scylla.Table("users"))
	var username, fullName string
	if err := cb.scylla.Session.Query(query, userID).WithContext(ctx).Scan(&username, &fullName); err != nil {
		return UserCtx{}, err
	}

	return UserCtx{
		ID:       strings.TrimSpace(userID.String()),
		Username: strings.TrimSpace(username),
		FullName: strings.TrimSpace(fullName),
	}, nil
}

func (cb *ContextBuilder) loadUsersByIDs(ctx context.Context, userIDs []string) (map[string]UserCtx, error) {
	users := make(map[string]UserCtx, len(userIDs))
	for _, userID := range userIDs {
		parsed, err := parseFlexibleUUID(userID)
		if err != nil {
			continue
		}
		user, err := cb.loadUserByUUID(ctx, parsed)
		if err != nil {
			if errors.Is(err, gocql.ErrNotFound) {
				continue
			}
			return nil, err
		}
		users[user.ID] = user
	}
	return users, nil
}

func (cb *ContextBuilder) loadUserDisplayNames(ctx context.Context, userIDs []string) (map[string]string, error) {
	userMap, err := cb.loadUsersByIDs(ctx, userIDs)
	if err != nil {
		return nil, err
	}

	names := make(map[string]string, len(userMap))
	for userID, user := range userMap {
		names[userID] = firstContextValue(strings.TrimSpace(user.FullName), strings.TrimSpace(user.Username))
	}
	return names, nil
}

func deriveSprintContexts(tasks []TaskCtx) []SprintCtx {
	if len(tasks) == 0 {
		return nil
	}

	sprintMap := make(map[string]*SprintCtx)
	for _, task := range tasks {
		sprintName := strings.TrimSpace(task.SprintName)
		if sprintName == "" {
			sprintName = "(No Sprint)"
		}
		sprint := sprintMap[sprintName]
		if sprint == nil {
			sprint = &SprintCtx{Name: sprintName}
			sprintMap[sprintName] = sprint
		}
		sprint.TaskCount++
		switch task.Status {
		case "done":
			sprint.Done++
		case "in_progress":
			sprint.InProgress++
		default:
			sprint.Todo++
		}
	}

	sprints := make([]SprintCtx, 0, len(sprintMap))
	for _, sprint := range sprintMap {
		sprints = append(sprints, *sprint)
	}
	sort.SliceStable(sprints, func(i, j int) bool { return sprintCtxLess(sprints[i], sprints[j]) })
	return sprints
}

func sortWorkspaceContext(workspace *WorkspaceContext) {
	if workspace == nil {
		return
	}

	sort.SliceStable(workspace.Members, func(i, j int) bool { return userCtxLess(workspace.Members[i], workspace.Members[j]) })
	sort.SliceStable(workspace.Tasks, func(i, j int) bool { return taskCtxLess(workspace.Tasks[i], workspace.Tasks[j]) })
	sort.SliceStable(workspace.SupportTickets, func(i, j int) bool { return taskCtxLess(workspace.SupportTickets[i], workspace.SupportTickets[j]) })
	sort.SliceStable(workspace.Sprints, func(i, j int) bool { return sprintCtxLess(workspace.Sprints[i], workspace.Sprints[j]) })
	sort.SliceStable(workspace.CanvasFiles, func(i, j int) bool {
		return compareFold(workspace.CanvasFiles[i].Path, workspace.CanvasFiles[j].Path) < 0
	})
	sort.SliceStable(workspace.RecentMessages, func(i, j int) bool {
		if workspace.RecentMessages[i].Timestamp.Equal(workspace.RecentMessages[j].Timestamp) {
			return compareFold(workspace.RecentMessages[i].SenderName, workspace.RecentMessages[j].SenderName) < 0
		}
		return workspace.RecentMessages[i].Timestamp.Before(workspace.RecentMessages[j].Timestamp)
	})
}

func normalizeBuildOptions(opts BuildOptions) BuildOptions {
	if opts.ChatMessageLimit <= 0 {
		opts.ChatMessageLimit = defaultChatMessageLimit
	}
	if opts.TaskLimit < 0 {
		opts.TaskLimit = defaultTaskLimit
	}
	if opts.CanvasExcerptLines <= 0 {
		opts.CanvasExcerptLines = defaultCanvasExcerptLines
	}
	return opts
}

func normalizeContextRoomID(raw string) string {
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

func normalizeContextUsername(raw string) string {
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
	return strings.Trim(strings.ToLower(builder.String()), "_")
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
	formatted := fmt.Sprintf(
		"%s-%s-%s-%s-%s",
		compact[0:8],
		compact[8:12],
		compact[12:16],
		compact[16:20],
		compact[20:32],
	)
	return gocql.ParseUUID(formatted)
}

func resolveContextTaskRoomUUID(raw string) (gocql.UUID, string, error) {
	normalizedRoomID := normalizeContextRoomID(raw)
	if normalizedRoomID == "" {
		return gocql.UUID{}, "", fmt.Errorf("room id is required")
	}

	if parsed, err := parseFlexibleUUID(strings.TrimSpace(raw)); err == nil {
		return parsed, normalizedRoomID, nil
	}
	if parsed, err := parseFlexibleUUID(normalizedRoomID); err == nil {
		return parsed, normalizedRoomID, nil
	}
	return deterministicContextTaskRoomUUID(normalizedRoomID), normalizedRoomID, nil
}

func deterministicContextTaskRoomUUID(normalizedRoomID string) gocql.UUID {
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

func normalizeContextTaskStatus(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, " ", "_")
	if normalized == "" {
		return "todo"
	}
	return normalized
}

func normalizeContextTaskType(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "support" {
		return "support"
	}
	return "sprint"
}

func renderTaskLine(task TaskCtx) string {
	status := strings.ToUpper(firstContextValue(task.Status, "todo"))
	title := firstContextValue(strings.TrimSpace(task.Title), "(untitled task)")
	sprintName := strings.TrimSpace(task.SprintName)
	if sprintName == "" {
		sprintName = "(none)"
	}
	budgetText := "-"
	if task.Budget != nil {
		budgetText = "$" + formatBudget(*task.Budget)
	}
	dueText := "-"
	if task.DueDate != nil && !task.DueDate.IsZero() {
		dueText = task.DueDate.UTC().Format("2006-01-02")
	}
	startText := "-"
	if task.StartDate != nil && !task.StartDate.IsZero() {
		startText = task.StartDate.UTC().Format("2006-01-02")
	}
	spentText := "-"
	if task.ActualCost != nil {
		spentText = "$" + formatBudget(*task.ActualCost)
	}
	roleNames := make([]string, 0, len(task.Roles))
	for _, role := range task.Roles {
		if strings.TrimSpace(role.Role) != "" {
			roleNames = append(roleNames, strings.TrimSpace(role.Role))
		}
	}
	rolesText := "-"
	if len(roleNames) > 0 {
		rolesText = strings.Join(roleNames, ", ")
	}

	description := truncateWithEllipsis(collapseWhitespace(task.Description), 100)
	line := fmt.Sprintf(
		"[%s] %s  {id:%s}  sprint:%q  budget:%s  spent:%s  start:%s  due:%s  roles: %s",
		status,
		title,
		task.ID,
		sprintName,
		budgetText,
		spentText,
		startText,
		dueText,
		rolesText,
	)
	if assignee := firstContextValue(strings.TrimSpace(task.AssigneeName), strings.TrimSpace(task.AssigneeID)); assignee != "" {
		line += "  assignee:" + assignee
	}
	if len(task.BlockedBy) > 0 {
		line += fmt.Sprintf("  blocked_by:%d", len(task.BlockedBy))
	}
	if len(task.Blocks) > 0 {
		line += fmt.Sprintf("  blocks:%d", len(task.Blocks))
	}
	if len(task.Subtasks) > 0 {
		line += fmt.Sprintf("  subtasks:%d", len(task.Subtasks))
	}
	if keys := compactCustomFieldKeys(task.CustomFields); keys != "" {
		line += "  custom_fields:" + keys
	}
	if description != "" {
		line += " | " + description
	}
	return line
}

func formatUserLabel(user UserCtx) string {
	if strings.TrimSpace(user.ID) == "" && strings.TrimSpace(user.Username) == "" && strings.TrimSpace(user.FullName) == "" {
		return ""
	}

	name := firstContextValue(strings.TrimSpace(user.FullName), strings.TrimSpace(user.Username), strings.TrimSpace(user.ID))
	if strings.TrimSpace(user.Username) != "" && !strings.EqualFold(strings.TrimSpace(user.Username), strings.TrimSpace(name)) {
		name += " (@" + strings.TrimSpace(user.Username) + ")"
	}
	if strings.TrimSpace(user.ID) != "" {
		name += " {id:" + strings.TrimSpace(user.ID) + "}"
	}
	return name
}

func extractTaskBudget(description string, customFields map[string]any) *float64 {
	if budget := extractBudgetFromDescription(description); budget != nil {
		return budget
	}
	return parseNumericCustomField(customFields, "budget", "task_budget")
}

func extractTaskActualCost(description string, customFields map[string]any) *float64 {
	if actualCost := extractActualCostFromDescription(description); actualCost != nil {
		return actualCost
	}
	return parseNumericCustomField(customFields, "actual_cost", "actual cost", "spent", "spent_cost", "cost")
}

func extractBudgetFromDescription(description string) *float64 {
	_, entries := parseTaskMetadataEntries(description)
	for _, entry := range entries {
		if entry.Key != "budget" {
			continue
		}
		valuePortion := entry.Raw
		if index := strings.Index(valuePortion, ":"); index >= 0 {
			valuePortion = valuePortion[index+1:]
		}
		value, ok := parseBudgetValue(valuePortion)
		if !ok {
			continue
		}
		budget := value
		return &budget
	}
	return nil
}

func extractActualCostFromDescription(description string) *float64 {
	_, entries := parseTaskMetadataEntries(description)
	for _, entry := range entries {
		switch entry.Key {
		case "actual cost", "actual_cost", "spent", "cost":
		default:
			continue
		}
		valuePortion := entry.Raw
		if index := strings.Index(valuePortion, ":"); index >= 0 {
			valuePortion = valuePortion[index+1:]
		}
		value, ok := parseBudgetValue(valuePortion)
		if !ok {
			continue
		}
		actualCost := value
		return &actualCost
	}
	return nil
}

type taskMetadataEntry struct {
	Key string
	Raw string
}

func parseTaskMetadataEntries(description string) (string, []taskMetadataEntry) {
	trimmed := strings.TrimSpace(description)
	if trimmed == "" {
		return "", nil
	}

	lastOpen := strings.LastIndex(trimmed, "[")
	lastClose := strings.LastIndex(trimmed, "]")
	if lastOpen < 0 || lastClose < lastOpen || strings.TrimSpace(trimmed[lastClose+1:]) != "" {
		return trimmed, nil
	}

	base := strings.TrimSpace(trimmed[:lastOpen])
	metadataBody := strings.TrimSpace(trimmed[lastOpen+1 : lastClose])
	if metadataBody == "" || !strings.Contains(metadataBody, ":") {
		return trimmed, nil
	}

	sections := strings.Split(metadataBody, "|")
	entries := make([]taskMetadataEntry, 0, len(sections))
	for _, section := range sections {
		raw := strings.TrimSpace(section)
		if raw == "" {
			continue
		}
		key := raw
		if idx := strings.Index(raw, ":"); idx >= 0 {
			key = raw[:idx]
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		entries = append(entries, taskMetadataEntry{Key: key, Raw: raw})
	}
	return base, entries
}

func parseBudgetValue(raw string) (float64, bool) {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return 0, false
	}
	replacer := strings.NewReplacer(",", "", "$", "", "USD", "", "usd", "")
	normalized = strings.TrimSpace(replacer.Replace(normalized))
	parts := strings.Fields(normalized)
	if len(parts) > 0 {
		normalized = parts[0]
	}
	value, err := strconv.ParseFloat(normalized, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, false
	}
	return value, true
}

func parseNumericCustomField(fields map[string]any, keys ...string) *float64 {
	if len(fields) == 0 || len(keys) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		allowed[strings.ToLower(strings.TrimSpace(key))] = struct{}{}
	}
	for fieldKey, rawValue := range fields {
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(fieldKey))]; !ok {
			continue
		}
		switch value := rawValue.(type) {
		case float64:
			if value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
				return &value
			}
		case int:
			next := float64(value)
			return &next
		case int64:
			next := float64(value)
			return &next
		case json.Number:
			parsed, err := value.Float64()
			if err == nil {
				return &parsed
			}
		case string:
			parsed, ok := parseBudgetValue(value)
			if ok {
				return &parsed
			}
		}
	}
	return nil
}

func parseJSONMap(raw *string) map[string]any {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(*raw), &parsed); err != nil {
		return nil
	}
	return parsed
}

func parseRoleContexts(raw *string) []RoleCtx {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil
	}
	var parsed []RoleCtx
	if err := json.Unmarshal([]byte(*raw), &parsed); err != nil {
		return nil
	}
	for index := range parsed {
		parsed[index].Role = strings.TrimSpace(parsed[index].Role)
		parsed[index].Responsibilities = strings.TrimSpace(parsed[index].Responsibilities)
	}
	return parsed
}

func cloneSubtasks(source []SubtaskCtx) []SubtaskCtx {
	if len(source) == 0 {
		return nil
	}
	next := make([]SubtaskCtx, len(source))
	copy(next, source)
	return next
}

func cloneStringSlice(source []string) []string {
	if len(source) == 0 {
		return nil
	}
	next := make([]string, 0, len(source))
	for _, value := range source {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		next = append(next, trimmed)
	}
	if len(next) == 0 {
		return nil
	}
	return next
}

func uniqueSortedContextIDs(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		unique = append(unique, trimmed)
	}
	sort.SliceStable(unique, func(i, j int) bool {
		return compareFold(unique[i], unique[j]) < 0
	})
	if len(unique) == 0 {
		return nil
	}
	return unique
}

func compactCustomFieldKeys(fields map[string]any) string {
	if len(fields) == 0 {
		return ""
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		keys = append(keys, trimmed)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.SliceStable(keys, func(i, j int) bool {
		return compareFold(keys[i], keys[j]) < 0
	})
	const maxKeys = 3
	if len(keys) <= maxKeys {
		return strings.Join(keys, ",")
	}
	return strings.Join(keys[:maxKeys], ",") + fmt.Sprintf("+%d", len(keys)-maxKeys)
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil || value.IsZero() {
		return nil
	}
	copyValue := value.UTC()
	return &copyValue
}

func excerptFirstLines(content string, maxLines int) (string, int) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	if normalized == "" {
		return "", 0
	}

	lines := strings.Split(normalized, "\n")
	totalLines := len(lines)
	if maxLines <= 0 || maxLines >= len(lines) {
		return strings.Join(lines, "\n"), totalLines
	}
	return strings.Join(lines[:maxLines], "\n"), totalLines
}

func collapseWhitespace(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func truncateWithEllipsis(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 || value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	if maxRunes == 1 {
		return string(runes[:1])
	}
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "..."
}

func formatBudget(value float64) string {
	formatted := strconv.FormatFloat(value, 'f', 2, 64)
	formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	if formatted == "" {
		return "0"
	}
	return formatted
}

func mergeRole(roleByUserID map[string]string, userID string, role string) {
	if strings.TrimSpace(userID) == "" {
		return
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		role = "member"
	}
	if existing, ok := roleByUserID[userID]; ok && existing == "owner" {
		return
	}
	roleByUserID[userID] = role
}

func taskCtxLess(left, right TaskCtx) bool {
	leftSprint := firstContextValue(strings.TrimSpace(left.SprintName), "(No Sprint)")
	rightSprint := firstContextValue(strings.TrimSpace(right.SprintName), "(No Sprint)")
	if cmp := compareFold(leftSprint, rightSprint); cmp != 0 {
		return cmp < 0
	}
	if leftStatus, rightStatus := taskStatusSortKey(left.Status), taskStatusSortKey(right.Status); leftStatus != rightStatus {
		return leftStatus < rightStatus
	}
	if cmp := compareFold(left.Title, right.Title); cmp != 0 {
		return cmp < 0
	}
	return compareFold(left.ID, right.ID) < 0
}

func sprintCtxLess(left, right SprintCtx) bool {
	if cmp := compareFold(left.Name, right.Name); cmp != 0 {
		return cmp < 0
	}
	return left.TaskCount < right.TaskCount
}

func userCtxLess(left, right UserCtx) bool {
	if left.IsOwner != right.IsOwner {
		return left.IsOwner
	}
	if cmp := compareFold(firstContextValue(left.FullName, left.Username, left.ID), firstContextValue(right.FullName, right.Username, right.ID)); cmp != 0 {
		return cmp < 0
	}
	return compareFold(left.ID, right.ID) < 0
}

func taskStatusSortKey(status string) int {
	switch normalizeContextTaskStatus(status) {
	case "todo":
		return 0
	case "in_progress":
		return 1
	case "blocked":
		return 2
	case "done":
		return 3
	default:
		return 4
	}
}

func compareFold(left, right string) int {
	left = strings.ToLower(strings.TrimSpace(left))
	right = strings.ToLower(strings.TrimSpace(right))
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i, j int) bool { return compareFold(keys[i], keys[j]) < 0 })
	return keys
}

func sortedKeysFromSet(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i, j int) bool { return compareFold(keys[i], keys[j]) < 0 })
	return keys
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func isMissingTableError(err error) bool {
	if err == nil {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(normalized, "unconfigured table") ||
		strings.Contains(normalized, "undefined name") ||
		strings.Contains(normalized, "cannot be found") ||
		strings.Contains(normalized, "not found")
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func firstContextValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
