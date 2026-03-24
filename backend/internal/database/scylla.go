package database

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	gocqlastra "github.com/datastax/gocql-astra"
	"github.com/gocql/gocql"
	"github.com/savanp08/converse/internal/config"
)

type ScyllaStore struct {
	Session  *gocql.Session
	Keyspace string
}

func NewScyllaStore(cfg config.Config) (*ScyllaStore, error) {
	var (
		cluster *gocql.ClusterConfig
		session *gocql.Session
		err     error
	)

	keyspace := strings.TrimSpace(cfg.ScyllaKeyspace)
	if keyspace == "" {
		keyspace = "converse"
	}

	// --- MODE 1: ASTRA CLOUD (via ID & Token) ---
	if cfg.AstraDatabaseID != "" && cfg.AstraToken != "" {
		log.Printf("☁️  Astra: Connecting via API ...")

		// This uses the official NewClusterFromURL method you requested
		cluster, err = gocqlastra.NewClusterFromURL(
			"https://api.astra.datastax.com",
			cfg.AstraDatabaseID,
			cfg.AstraToken,
			30*time.Second,
		)
		if err != nil {
			return nil, fmt.Errorf("astra config failed: %w", err)
		}

		cluster.Keyspace = keyspace
		cluster.Consistency = gocql.LocalQuorum

		// --- MODE 2: LOCAL SCYLLA/DOCKER ---
	} else {
		log.Println("🏠 Scylla: Connecting to Local Cluster...")
		if len(cfg.ScyllaHosts) == 0 {
			cfg.ScyllaHosts = []string{"localhost"}
		}
		cluster = gocql.NewCluster(cfg.ScyllaHosts...)
		cluster.Keyspace = keyspace
		cluster.Consistency = gocql.Quorum
	}

	// Global Settings
	cluster.ConnectTimeout = 30 * time.Second
	cluster.Timeout = 30 * time.Second

	// --- CREATE SESSION ---
	session, err = cluster.CreateSession()

	// --- LOCAL RECOVERY (Create Keyspace) ---
	// If local connection fails because keyspace is missing, create it
	if err != nil && cfg.AstraDatabaseID == "" && (strings.Contains(err.Error(), "keyspace") || strings.Contains(err.Error(), "does not exist")) {
		log.Println("⚠️  Local Keyspace missing. Creating it...")

		cluster.Keyspace = "" // Connect to system
		sysSession, sysErr := cluster.CreateSession()
		if sysErr == nil {
			q := fmt.Sprintf("CREATE KEYSPACE IF NOT EXISTS %s WITH replication = {'class': 'SimpleStrategy', 'replication_factor': '1'}", keyspace)
			_ = sysSession.Query(q).Exec()
			sysSession.Close()

			// Retry original connection
			cluster.Keyspace = keyspace
			session, err = cluster.CreateSession()
		}
	}

	if err != nil {
		return nil, fmt.Errorf("session creation failed: %w", err)
	}

	if session == nil {
		return nil, fmt.Errorf("CRITICAL: session is nil")
	}

	if err := ensureBaseSchema(session, keyspace); err != nil {
		log.Printf("⚠️  Warning: Could not ensure base schema: %v", err)
	}
	if err := ensurePersistenceSchema(session, keyspace); err != nil {
		log.Printf("⚠️  Warning: Could not ensure persistence schema: %v", err)
	}

	return &ScyllaStore{Session: session, Keyspace: keyspace}, nil
}

func (s *ScyllaStore) Close() {
	if s != nil && s.Session != nil {
		s.Session.Close()
	}
}

func (s *ScyllaStore) Table(name string) string {
	if s == nil || s.Keyspace == "" {
		return name
	}
	if strings.Contains(name, ".") {
		return name
	}
	return s.Keyspace + "." + name
}

func (s *ScyllaStore) Ping(ctx context.Context) error {
	if s == nil || s.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	var releaseVersion string
	query := `SELECT release_version FROM system.local LIMIT 1`
	return s.Session.Query(query).WithContext(ctx).Scan(&releaseVersion)
}

func (s *ScyllaStore) UpdateRoomSummary(ctx context.Context, roomID string, summary string) error {
	if s == nil || s.Session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	normalizedRoomID := strings.TrimSpace(roomID)
	if normalizedRoomID == "" {
		return fmt.Errorf("room id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	query := fmt.Sprintf(`UPDATE %s SET rolling_summary = ? WHERE room_id = ?`, s.Table("rooms"))
	return s.Session.Query(
		query,
		strings.TrimSpace(summary),
		normalizedRoomID,
	).WithContext(ctx).Exec()
}

func (s *ScyllaStore) GetRoomSummary(ctx context.Context, roomID string) (string, error) {
	if s == nil || s.Session == nil {
		return "", fmt.Errorf("scylla session is not configured")
	}
	normalizedRoomID := strings.TrimSpace(roomID)
	if normalizedRoomID == "" {
		return "", nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	query := fmt.Sprintf(`SELECT rolling_summary FROM %s WHERE room_id = ? LIMIT 1`, s.Table("rooms"))
	var summary *string
	err := s.Session.Query(query, normalizedRoomID).WithContext(ctx).Scan(&summary)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	if summary == nil {
		return "", nil
	}
	return strings.TrimSpace(*summary), nil
}

func ensureBaseSchema(session *gocql.Session, keyspace string) error {
	if session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	normalizedKeyspace := strings.TrimSpace(keyspace)
	if normalizedKeyspace == "" {
		normalizedKeyspace = "converse"
	}
	boardElementsTable := normalizedKeyspace + ".board_elements"
	canvasSnapshotsTable := normalizedKeyspace + ".canvas_snapshots"
	roomsTable := normalizedKeyspace + ".rooms"

	roomsQuery := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (
			room_id text PRIMARY KEY,
			name text,
			type text,
			parent_room_id text,
			origin_message_id text,
			admin_code text,
			canvas_has_data boolean,
			rolling_summary text,
			created_at timestamp,
			updated_at timestamp
		)`,
		roomsTable,
	)
	err := session.Query(roomsQuery).Exec()
	if err != nil {
		return err
	}

	roomAlterQueries := []string{
		fmt.Sprintf(`ALTER TABLE %s ADD parent_room_id text`, roomsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD admin_code text`, roomsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD canvas_has_data boolean`, roomsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD rolling_summary text`, roomsTable),
	}
	for _, alterQuery := range roomAlterQueries {
		err := session.Query(alterQuery).Exec()
		if err != nil {
			lowered := strings.ToLower(strings.TrimSpace(err.Error()))
			if strings.Contains(lowered, "duplicate") || strings.Contains(lowered, "already exists") {
				continue
			}
			return err
		}
	}

	query := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (
			room_id text,
			element_id text,
			type text,
			x float,
			y float,
			width float,
			height float,
			content text,
			z_index int,
			created_by_user_id text,
			created_by_name text,
			created_at timestamp,
			PRIMARY KEY (room_id, element_id)
		)`,
		boardElementsTable,
	)
	err = session.Query(query).Exec()
	if err != nil {
		return err
	}
	alterQueries := []string{
		fmt.Sprintf(`ALTER TABLE %s ADD created_by_user_id text`, boardElementsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD created_by_name text`, boardElementsTable),
	}
	for _, alterQuery := range alterQueries {
		err := session.Query(alterQuery).Exec()
		if err != nil {
			lowered := strings.ToLower(strings.TrimSpace(err.Error()))
			if strings.Contains(lowered, "duplicate") || strings.Contains(lowered, "already exists") {
				continue
			}
			return err
		}
	}
	canvasSnapshotsQuery := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (
			room_id text PRIMARY KEY,
			snapshot blob
		)`,
		canvasSnapshotsTable,
	)
	err = session.Query(canvasSnapshotsQuery).Exec()
	if err != nil {
		return err
	}
	return nil
}

func ensurePersistenceSchema(session *gocql.Session, keyspace string) error {
	if session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	normalizedKeyspace := strings.TrimSpace(keyspace)
	if normalizedKeyspace == "" {
		normalizedKeyspace = "converse"
	}

	userRoomsTable := normalizedKeyspace + ".user_rooms"
	userRoomsTextTable := normalizedKeyspace + ".user_rooms_text"
	userConnectionsTable := normalizedKeyspace + ".user_connections"
	personalItemsTable := normalizedKeyspace + ".personal_items"
	tasksTable := normalizedKeyspace + ".tasks"
	roomFieldSchemasTable := normalizedKeyspace + ".room_field_schemas"
	taskRelationsTable := normalizedKeyspace + ".task_relations"
	timeEntriesTable := normalizedKeyspace + ".time_entries"
	intakeFormsTable := normalizedKeyspace + ".intake_forms"
	formSubmissionsTable := normalizedKeyspace + ".form_submissions"
	roomsTable := normalizedKeyspace + ".rooms"

	persistenceQueries := []string{
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				user_id uuid,
				room_id uuid,
				room_name text,
				role text,
				joined_at timestamp,
				last_accessed timestamp,
				PRIMARY KEY ((user_id), room_id)
			) WITH CLUSTERING ORDER BY (room_id ASC)`,
			userRoomsTable,
		),
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				user_id uuid,
				room_id text,
				room_name text,
				role text,
				room_type text,
				joined_at timestamp,
				last_accessed timestamp,
				expires_at timestamp,
				PRIMARY KEY ((user_id), room_id)
			) WITH CLUSTERING ORDER BY (room_id ASC)`,
			userRoomsTextTable,
		),
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				user_id uuid,
				target_id uuid,
				status text,
				created_at timestamp,
				PRIMARY KEY (user_id, target_id)
			)`,
			userConnectionsTable,
		),
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				user_id uuid,
				item_id uuid,
				type text,
				title text,
				content text,
				description text,
				status text,
				due_at timestamp,
				start_at timestamp,
				end_at timestamp,
				remind_at timestamp,
				repeat_rule text,
				created_at timestamp,
				PRIMARY KEY ((user_id), item_id)
			) WITH CLUSTERING ORDER BY (item_id DESC)`,
			personalItemsTable,
		),
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
					room_id uuid,
					id uuid,
					title text,
					description text,
					status text,
					sprint_name text,
					assignee_id uuid,
					custom_fields text,
					status_actor_id text,
					status_actor_name text,
					status_changed_at timestamp,
					created_at timestamp,
					updated_at timestamp,
					PRIMARY KEY ((room_id), id)
				) WITH CLUSTERING ORDER BY (id ASC)`,
			tasksTable,
		),
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				room_id text,
				field_id text,
				name text,
				field_type text,
				options text,
				position int,
				created_at timestamp,
				PRIMARY KEY (room_id, field_id)
			) WITH CLUSTERING ORDER BY (field_id ASC)`,
			roomFieldSchemasTable,
		),
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				room_id text,
				from_task_id text,
				to_task_id text,
				relation_type text,
				position int,
				content text,
				completed boolean,
				created_at timestamp,
				PRIMARY KEY (room_id, from_task_id, to_task_id)
			) WITH CLUSTERING ORDER BY (from_task_id ASC, to_task_id ASC)`,
			taskRelationsTable,
		),
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				room_id text,
				task_id text,
				entry_id text,
				user_id text,
				username text,
				start_time timestamp,
				end_time timestamp,
				duration_seconds int,
				note text,
				created_at timestamp,
				PRIMARY KEY ((room_id, task_id), entry_id)
			) WITH CLUSTERING ORDER BY (entry_id DESC)`,
			timeEntriesTable,
		),
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				room_id text,
				form_id text,
				title text,
				description text,
				fields text,
				target_status text,
				target_sprint text,
				enabled boolean,
				created_at timestamp,
				PRIMARY KEY (room_id, form_id)
			) WITH CLUSTERING ORDER BY (form_id ASC)`,
			intakeFormsTable,
		),
		fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (
				form_id text,
				submission_id text,
				room_id text,
				task_id text,
				data text,
				submitter_email text,
				submitted_at timestamp,
				PRIMARY KEY (form_id, submission_id)
			) WITH CLUSTERING ORDER BY (submission_id DESC)`,
			formSubmissionsTable,
		),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS ON %s (assignee_id)`, tasksTable),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS ON %s (to_task_id)`, taskRelationsTable),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS ON %s (user_id)`, timeEntriesTable),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS ON %s (form_id)`, intakeFormsTable),
	}

	for _, query := range persistenceQueries {
		if err := session.Query(query).Exec(); err != nil {
			return err
		}
	}

	alterRoomsQueries := []string{
		fmt.Sprintf(`ALTER TABLE %s ADD id uuid`, roomsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD owner_id uuid`, roomsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD is_ephemeral boolean`, roomsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD is_direct boolean`, roomsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD expires_at timestamp`, roomsTable),
	}
	for _, alterQuery := range alterRoomsQueries {
		err := session.Query(alterQuery).Exec()
		if err != nil {
			lowered := strings.ToLower(strings.TrimSpace(err.Error()))
			if strings.Contains(lowered, "duplicate") || strings.Contains(lowered, "already exists") {
				continue
			}
			return err
		}
	}

	alterPersonalItemsQueries := []string{
		fmt.Sprintf(`ALTER TABLE %s ADD title text`, personalItemsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD description text`, personalItemsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD start_at timestamp`, personalItemsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD end_at timestamp`, personalItemsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD remind_at timestamp`, personalItemsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD repeat_rule text`, personalItemsTable),
	}
	for _, alterQuery := range alterPersonalItemsQueries {
		err := session.Query(alterQuery).Exec()
		if err != nil {
			lowered := strings.ToLower(strings.TrimSpace(err.Error()))
			if strings.Contains(lowered, "duplicate") || strings.Contains(lowered, "already exists") {
				continue
			}
			return err
		}
	}

	alterTasksQueries := []string{
		fmt.Sprintf(`ALTER TABLE %s ADD sprint_name text`, tasksTable),
		fmt.Sprintf(`ALTER TABLE %s ADD status_actor_id text`, tasksTable),
		fmt.Sprintf(`ALTER TABLE %s ADD status_actor_name text`, tasksTable),
		fmt.Sprintf(`ALTER TABLE %s ADD status_changed_at timestamp`, tasksTable),
		fmt.Sprintf(`ALTER TABLE %s ADD custom_fields text`, tasksTable),
	}
	for _, alterQuery := range alterTasksQueries {
		err := session.Query(alterQuery).Exec()
		if err != nil {
			lowered := strings.ToLower(strings.TrimSpace(err.Error()))
			if strings.Contains(lowered, "duplicate") || strings.Contains(lowered, "already exists") {
				continue
			}
			return err
		}
	}

	return nil
}

func UpdateCanvasHasData(ctx context.Context, session *gocql.Session, roomID string) error {
	if session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	normalizedRoomID := strings.TrimSpace(roomID)
	if normalizedRoomID == "" {
		return fmt.Errorf("room id is required")
	}
	query := `UPDATE rooms SET canvas_has_data = true WHERE room_id = ?`
	return session.Query(query, normalizedRoomID).WithContext(ctx).Exec()
}

func CheckCanvasHasData(ctx context.Context, session *gocql.Session, roomID string) (bool, error) {
	if session == nil {
		return false, fmt.Errorf("scylla session is not configured")
	}
	normalizedRoomID := strings.TrimSpace(roomID)
	if normalizedRoomID == "" {
		return false, nil
	}

	query := `SELECT canvas_has_data FROM rooms WHERE room_id = ? LIMIT 1`
	var hasData *bool
	err := session.Query(query, normalizedRoomID).WithContext(ctx).Scan(&hasData)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if hasData == nil {
		return false, nil
	}
	return *hasData, nil
}
