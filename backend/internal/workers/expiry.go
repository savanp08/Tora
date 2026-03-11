package workers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hibiken/asynq"
	"github.com/minio/minio-go/v7"
	"github.com/savanp08/converse/internal/database"
	"github.com/savanp08/converse/internal/storage"
)

const (
	TaskTypeRoomExpiryEmail    = "room:expiry_email"
	QueueRoomExpiryEmail       = "room_expiry_email"
	roomExpiryLeadTime         = time.Hour
	roomArchivePresignedTTL    = 24 * time.Hour
	defaultWorkerConcurrency   = 4
	defaultSMTPPort            = 587
	defaultExportQueryTimeout  = 30 * time.Second
	defaultExportUploadTimeout = 45 * time.Second
)

type ExpiryEmailTaskPayload struct {
	RoomID    string `json:"roomId"`
	Email     string `json:"email"`
	ExpiresAt int64  `json:"expiresAt"`
}

type SMTPSettings struct {
	Host        string
	Port        int
	Username    string
	Password    string
	FromAddress string
	FromName    string
}

type ExpiryEmailQueue struct {
	client  *asynq.Client
	server  *asynq.Server
	mux     *asynq.ServeMux
	handler *ExpiryEmailTaskHandler
}

type ExpiryEmailTaskHandler struct {
	Scylla *database.ScyllaStore
	R2     *storage.R2Client
	SMTP   SMTPSettings
}

type RoomStateArchive struct {
	RoomID             string                   `json:"roomId"`
	ExpiresAt          int64                    `json:"expiresAt"`
	ExportedAt         time.Time                `json:"exportedAt"`
	Room               map[string]interface{}   `json:"room,omitempty"`
	Messages           []map[string]interface{} `json:"messages"`
	BoardElements      []map[string]interface{} `json:"boardElements"`
	RoomPins           []map[string]interface{} `json:"roomPins"`
	DiscussionComments []map[string]interface{} `json:"discussionComments"`
	RoomSoftExpiry     []map[string]interface{} `json:"roomSoftExpiry"`
	CanvasSnapshots    []map[string]interface{} `json:"canvasSnapshots"`
}

func NewExpiryEmailQueue(
	redisAddr string,
	redisPassword string,
	scyllaStore *database.ScyllaStore,
	r2Client *storage.R2Client,
) (*ExpiryEmailQueue, error) {
	normalizedAddr := strings.TrimSpace(redisAddr)
	if normalizedAddr == "" {
		return nil, fmt.Errorf("redis address is required")
	}

	redisOpt := asynq.RedisClientOpt{
		Addr:     normalizedAddr,
		Password: strings.TrimSpace(redisPassword),
		DB:       0,
	}

	handler := &ExpiryEmailTaskHandler{
		Scylla: scyllaStore,
		R2:     r2Client,
		SMTP:   LoadSMTPSettingsFromEnv(),
	}

	mux := asynq.NewServeMux()
	mux.HandleFunc(TaskTypeRoomExpiryEmail, handler.ProcessTask)

	queue := &ExpiryEmailQueue{
		client: asynq.NewClient(redisOpt),
		server: asynq.NewServer(redisOpt, asynq.Config{
			Concurrency: defaultWorkerConcurrency,
			Queues: map[string]int{
				QueueRoomExpiryEmail: 8,
				"default":            1,
			},
		}),
		mux:     mux,
		handler: handler,
	}

	return queue, nil
}

func (q *ExpiryEmailQueue) Run() error {
	if q == nil || q.server == nil || q.mux == nil {
		return fmt.Errorf("expiry email queue is not configured")
	}
	return q.server.Run(q.mux)
}

func (q *ExpiryEmailQueue) Shutdown() {
	if q == nil {
		return
	}
	if q.server != nil {
		q.server.Shutdown()
	}
	if q.client != nil {
		_ = q.client.Close()
	}
}

func (q *ExpiryEmailQueue) Client() *asynq.Client {
	if q == nil {
		return nil
	}
	return q.client
}

func (q *ExpiryEmailQueue) EnqueueRoomExpiryEmailTask(
	roomID string,
	email string,
	expirationTimestamp int64,
) (*asynq.TaskInfo, error) {
	return EnqueueRoomExpiryEmailTask(q.Client(), roomID, email, expirationTimestamp)
}

func EnqueueRoomExpiryEmailTask(
	client *asynq.Client,
	roomID string,
	email string,
	expirationTimestamp int64,
) (*asynq.TaskInfo, error) {
	if expirationTimestamp <= 0 {
		return nil, fmt.Errorf("expiration timestamp is required")
	}
	expirationAt := time.Unix(expirationTimestamp, 0).UTC()
	return enqueueRoomExpiryEmailTask(client, roomID, email, expirationAt)
}

func EnqueueRoomExpiryEmailTaskAt(
	client *asynq.Client,
	roomID string,
	email string,
	expirationAt time.Time,
) (*asynq.TaskInfo, error) {
	return enqueueRoomExpiryEmailTask(client, roomID, email, expirationAt)
}

func enqueueRoomExpiryEmailTask(
	client *asynq.Client,
	roomID string,
	email string,
	expirationAt time.Time,
) (*asynq.TaskInfo, error) {
	if client == nil {
		return nil, fmt.Errorf("asynq client is not configured")
	}
	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return nil, fmt.Errorf("room id is required")
	}
	normalizedEmail := normalizeEmail(email)
	if normalizedEmail == "" {
		return nil, fmt.Errorf("valid email is required")
	}
	if expirationAt.IsZero() {
		return nil, fmt.Errorf("expiration timestamp is required")
	}

	runAt := expirationAt.UTC().Add(-roomExpiryLeadTime)
	payloadBytes, err := json.Marshal(ExpiryEmailTaskPayload{
		RoomID:    normalizedRoomID,
		Email:     normalizedEmail,
		ExpiresAt: expirationAt.UTC().Unix(),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal expiry email task payload: %w", err)
	}

	task := asynq.NewTask(TaskTypeRoomExpiryEmail, payloadBytes)
	info, err := client.Enqueue(
		task,
		asynq.Queue(QueueRoomExpiryEmail),
		asynq.ProcessAt(runAt),
		asynq.MaxRetry(5),
	)
	if err != nil {
		return nil, fmt.Errorf("enqueue expiry email task: %w", err)
	}
	return info, nil
}

func (h *ExpiryEmailTaskHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if task == nil {
		return fmt.Errorf("%w: task is nil", asynq.SkipRetry)
	}
	if task.Type() != TaskTypeRoomExpiryEmail {
		return fmt.Errorf("%w: unsupported task type: %s", asynq.SkipRetry, task.Type())
	}

	payload, err := decodeExpiryEmailTaskPayload(task.Payload())
	if err != nil {
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}

	archiveCtx, cancelArchive := context.WithTimeout(ctx, defaultExportQueryTimeout)
	defer cancelArchive()
	archive, err := h.fetchRoomStateArchive(archiveCtx, payload.RoomID, payload.ExpiresAt)
	if err != nil {
		return fmt.Errorf("fetch room state archive: %w", err)
	}

	archiveJSON, err := json.MarshalIndent(archive, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal room state archive json: %w", err)
	}

	uploadCtx, cancelUpload := context.WithTimeout(ctx, defaultExportUploadTimeout)
	defer cancelUpload()
	downloadURL, objectKey, err := h.uploadRoomStateArchive(uploadCtx, payload.RoomID, archiveJSON)
	if err != nil {
		return fmt.Errorf("upload room archive to r2: %w", err)
	}

	if err := h.sendRoomArchiveEmail(payload.Email, payload.RoomID, payload.ExpiresAt, downloadURL, objectKey); err != nil {
		return err
	}

	return nil
}

func (h *ExpiryEmailTaskHandler) fetchRoomStateArchive(
	ctx context.Context,
	roomID string,
	expiresAt int64,
) (RoomStateArchive, error) {
	archive := RoomStateArchive{
		RoomID:             roomID,
		ExpiresAt:          expiresAt,
		ExportedAt:         time.Now().UTC(),
		Messages:           []map[string]interface{}{},
		BoardElements:      []map[string]interface{}{},
		RoomPins:           []map[string]interface{}{},
		DiscussionComments: []map[string]interface{}{},
		RoomSoftExpiry:     []map[string]interface{}{},
		CanvasSnapshots:    []map[string]interface{}{},
	}

	if h == nil || h.Scylla == nil || h.Scylla.Session == nil {
		return archive, fmt.Errorf("scylla session is not configured")
	}

	roomsTable := h.Scylla.Table("rooms")
	messagesTable := h.Scylla.Table("messages")
	boardElementsTable := h.Scylla.Table("board_elements")
	roomPinsTable := h.Scylla.Table("room_pins")
	commentsTable := h.Scylla.Table("pin_discussion_comments")
	softExpiryTable := h.Scylla.Table("room_message_soft_expiry")
	canvasSnapshotsTable := h.Scylla.Table("canvas_snapshots")

	roomRows, err := h.queryRowsAsMaps(
		ctx,
		fmt.Sprintf(`SELECT room_id, name, type, parent_room_id, origin_message_id, admin_code, canvas_has_data, rolling_summary, created_at, updated_at FROM %s WHERE room_id = ? LIMIT 1`, roomsTable),
		roomID,
	)
	if err != nil {
		return archive, fmt.Errorf("query room metadata: %w", err)
	}
	if len(roomRows) > 0 {
		archive.Room = roomRows[0]
	}

	archive.Messages, err = h.queryRowsAsMaps(
		ctx,
		fmt.Sprintf(`SELECT room_id, created_at, message_id, sender_id, sender_name, content, type, media_url, media_type, file_name, is_edited, edited_at, has_break_room, break_room_id, break_join_count, reply_to_message_id, reply_to_snippet FROM %s WHERE room_id = ?`, messagesTable),
		roomID,
	)
	if err != nil {
		return archive, fmt.Errorf("query room messages: %w", err)
	}

	archive.BoardElements, err = h.queryRowsAsMaps(
		ctx,
		fmt.Sprintf(`SELECT room_id, element_id, type, x, y, width, height, content, z_index, created_by_user_id, created_by_name, created_at FROM %s WHERE room_id = ?`, boardElementsTable),
		roomID,
	)
	if err != nil {
		return archive, fmt.Errorf("query board elements: %w", err)
	}

	archive.RoomPins, err = h.queryRowsAsMaps(
		ctx,
		fmt.Sprintf(`SELECT room_id, created_at, message_id, type FROM %s WHERE room_id = ?`, roomPinsTable),
		roomID,
	)
	if err != nil {
		return archive, fmt.Errorf("query room pins: %w", err)
	}

	for _, pin := range archive.RoomPins {
		pinMessageID := normalizeString(pin["message_id"])
		if pinMessageID == "" {
			continue
		}
		commentRows, commentsErr := h.queryRowsAsMaps(
			ctx,
			fmt.Sprintf(`SELECT room_id, pin_message_id, created_at, comment_id, parent_comment_id, sender_id, sender_name, content, is_edited, edited_at, is_deleted, is_pinned, pinned_by, pinned_by_name, pinned_at FROM %s WHERE room_id = ? AND pin_message_id = ?`, commentsTable),
			roomID,
			pinMessageID,
		)
		if commentsErr != nil {
			return archive, fmt.Errorf("query discussion comments for pin %s: %w", pinMessageID, commentsErr)
		}
		archive.DiscussionComments = append(archive.DiscussionComments, commentRows...)
	}

	archive.RoomSoftExpiry, err = h.queryRowsAsMaps(
		ctx,
		fmt.Sprintf(`SELECT room_id, extended_expiry_time, updated_at FROM %s WHERE room_id = ? LIMIT 1`, softExpiryTable),
		roomID,
	)
	if err != nil {
		return archive, fmt.Errorf("query room soft expiry: %w", err)
	}

	archive.CanvasSnapshots, err = h.queryRowsAsMaps(
		ctx,
		fmt.Sprintf(`SELECT room_id, snapshot FROM %s WHERE room_id = ? LIMIT 1`, canvasSnapshotsTable),
		roomID,
	)
	if err != nil {
		return archive, fmt.Errorf("query canvas snapshot: %w", err)
	}

	return archive, nil
}

func (h *ExpiryEmailTaskHandler) queryRowsAsMaps(
	ctx context.Context,
	query string,
	args ...interface{},
) ([]map[string]interface{}, error) {
	if h == nil || h.Scylla == nil || h.Scylla.Session == nil {
		return nil, fmt.Errorf("scylla session is not configured")
	}

	iter := h.Scylla.Session.Query(query, args...).WithContext(ctx).Iter()
	rows := make([]map[string]interface{}, 0, 64)
	row := map[string]interface{}{}
	for iter.MapScan(row) {
		normalized := make(map[string]interface{}, len(row))
		for key, value := range row {
			normalized[key] = normalizeScyllaValue(value)
		}
		rows = append(rows, normalized)
		row = map[string]interface{}{}
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return rows, nil
}

func (h *ExpiryEmailTaskHandler) uploadRoomStateArchive(
	ctx context.Context,
	roomID string,
	archiveJSON []byte,
) (downloadURL string, objectKey string, err error) {
	if h == nil || h.R2 == nil || h.R2.Client == nil {
		return "", "", fmt.Errorf("r2 client is not configured")
	}

	normalizedRoomID := normalizeRoomID(roomID)
	if normalizedRoomID == "" {
		return "", "", fmt.Errorf("room id is required")
	}
	if len(archiveJSON) == 0 {
		return "", "", fmt.Errorf("room archive json is empty")
	}

	objectKey = fmt.Sprintf(
		"exports/rooms/%s/state_%d.json",
		normalizedRoomID,
		time.Now().UTC().Unix(),
	)
	_, putErr := h.R2.Client.PutObject(
		ctx,
		h.R2.Bucket,
		objectKey,
		bytes.NewReader(archiveJSON),
		int64(len(archiveJSON)),
		minio.PutObjectOptions{
			ContentType: "application/json",
		},
	)
	if putErr != nil {
		return "", "", fmt.Errorf("put archive object: %w", putErr)
	}

	presignedURL, signErr := h.R2.Client.PresignedGetObject(
		ctx,
		h.R2.Bucket,
		objectKey,
		roomArchivePresignedTTL,
		url.Values{},
	)
	if signErr != nil {
		return "", "", fmt.Errorf("presign archive object: %w", signErr)
	}

	return presignedURL.String(), objectKey, nil
}

func (h *ExpiryEmailTaskHandler) sendRoomArchiveEmail(
	targetEmail string,
	roomID string,
	expiresAt int64,
	downloadURL string,
	objectKey string,
) error {
	settings := h.SMTP
	if err := settings.Validate(); err != nil {
		return fmt.Errorf("%w: smtp settings are invalid: %v", asynq.SkipRetry, err)
	}

	normalizedEmail := normalizeEmail(targetEmail)
	if normalizedEmail == "" {
		return fmt.Errorf("%w: invalid recipient email", asynq.SkipRetry)
	}

	expirationTime := time.Unix(expiresAt, 0).UTC()
	downloadExpiresAt := time.Now().UTC().Add(roomArchivePresignedTTL)

	subject := fmt.Sprintf("Room archive for %s", roomID)
	body := fmt.Sprintf(
		"Your room archive is ready.\n\nRoom ID: %s\nRoom expiration: %s UTC\nDownload link (valid 24 hours):\n%s\n\nLink expires at: %s UTC\n\nObject key: %s",
		roomID,
		expirationTime.Format("2006-01-02 15:04:05"),
		downloadURL,
		downloadExpiresAt.Format("2006-01-02 15:04:05"),
		objectKey,
	)

	headers := []string{
		fmt.Sprintf("From: %s", settings.FromHeader()),
		fmt.Sprintf("To: %s", normalizedEmail),
		fmt.Sprintf("Subject: %s", subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}
	message := strings.Join(headers, "\r\n")

	smtpAddress := fmt.Sprintf("%s:%d", settings.Host, settings.Port)
	var auth smtp.Auth
	if settings.Username != "" || settings.Password != "" {
		auth = smtp.PlainAuth("", settings.Username, settings.Password, settings.Host)
	}

	if err := smtp.SendMail(smtpAddress, auth, settings.FromAddress, []string{normalizedEmail}, []byte(message)); err != nil {
		return fmt.Errorf("send archive email: %w", err)
	}

	log.Printf("[expiry-email-worker] archive email sent")
	return nil
}

func LoadSMTPSettingsFromEnv() SMTPSettings {
	settings := SMTPSettings{
		Host:        strings.TrimSpace(os.Getenv("SMTP_HOST")),
		Port:        parseSMTPPort(strings.TrimSpace(os.Getenv("SMTP_PORT"))),
		Username:    strings.TrimSpace(os.Getenv("SMTP_USERNAME")),
		Password:    strings.TrimSpace(os.Getenv("SMTP_PASSWORD")),
		FromAddress: strings.TrimSpace(os.Getenv("SMTP_FROM")),
		FromName:    strings.TrimSpace(os.Getenv("SMTP_FROM_NAME")),
	}
	if settings.Port <= 0 {
		settings.Port = defaultSMTPPort
	}
	return settings
}

func (s SMTPSettings) Validate() error {
	if strings.TrimSpace(s.Host) == "" {
		return fmt.Errorf("SMTP_HOST is required")
	}
	if s.Port <= 0 {
		return fmt.Errorf("SMTP_PORT must be positive")
	}
	if normalizeEmail(s.FromAddress) == "" {
		return fmt.Errorf("SMTP_FROM must be a valid email address")
	}
	return nil
}

func (s SMTPSettings) FromHeader() string {
	fromAddress := normalizeEmail(s.FromAddress)
	if fromAddress == "" {
		return s.FromAddress
	}
	name := strings.TrimSpace(s.FromName)
	if name == "" {
		return fromAddress
	}
	return fmt.Sprintf("%s <%s>", name, fromAddress)
}

func decodeExpiryEmailTaskPayload(payloadBytes []byte) (ExpiryEmailTaskPayload, error) {
	var payload ExpiryEmailTaskPayload
	if len(payloadBytes) == 0 {
		return payload, fmt.Errorf("task payload is empty")
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return payload, fmt.Errorf("invalid task payload json: %w", err)
	}
	payload.RoomID = normalizeRoomID(payload.RoomID)
	payload.Email = normalizeEmail(payload.Email)
	if payload.RoomID == "" {
		return payload, fmt.Errorf("payload room id is required")
	}
	if payload.Email == "" {
		return payload, fmt.Errorf("payload email is required")
	}
	if payload.ExpiresAt <= 0 {
		return payload, fmt.Errorf("payload expiration timestamp is required")
	}
	return payload, nil
}

func parseSMTPPort(raw string) int {
	if strings.TrimSpace(raw) == "" {
		return defaultSMTPPort
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return defaultSMTPPort
	}
	return parsed
}

func normalizeRoomID(raw string) string {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return ""
	}
	var builder strings.Builder
	for _, character := range trimmed {
		switch {
		case character >= 'a' && character <= 'z':
			builder.WriteRune(character)
		case character >= '0' && character <= '9':
			builder.WriteRune(character)
		case character == '-' || character == '_':
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

func normalizeEmail(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := mail.ParseAddress(trimmed)
	if err != nil || parsed == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parsed.Address))
}

func normalizeString(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []byte:
		return strings.TrimSpace(string(typed))
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func normalizeScyllaValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case nil:
		return nil
	case []byte:
		if utf8.Valid(typed) {
			return string(typed)
		}
		return base64.StdEncoding.EncodeToString(typed)
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	case *time.Time:
		if typed == nil {
			return nil
		}
		return typed.UTC().Format(time.RFC3339Nano)
	case map[string]interface{}:
		next := make(map[string]interface{}, len(typed))
		for key, nested := range typed {
			next[key] = normalizeScyllaValue(nested)
		}
		return next
	case []interface{}:
		next := make([]interface{}, len(typed))
		for index, nested := range typed {
			next[index] = normalizeScyllaValue(nested)
		}
		return next
	default:
		return typed
	}
}
