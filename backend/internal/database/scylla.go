package database

import (
	"context"
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
	gocql.Logger = log.New(os.Stdout, "[gocql-debug] ", log.LstdFlags)
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
			sysSession.Query(q).Exec()
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

func ensureBaseSchema(session *gocql.Session, keyspace string) error {
	if session == nil {
		return fmt.Errorf("scylla session is not configured")
	}
	normalizedKeyspace := strings.TrimSpace(keyspace)
	if normalizedKeyspace == "" {
		normalizedKeyspace = "converse"
	}
	boardElementsTable := normalizedKeyspace + ".board_elements"
	roomsTable := normalizedKeyspace + ".rooms"

	roomsQuery := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (
			room_id text PRIMARY KEY,
			name text,
			type text,
			parent_room_id text,
			origin_message_id text,
			admin_code text,
			created_at timestamp,
			updated_at timestamp
		)`,
		roomsTable,
	)
	if err := session.Query(roomsQuery).Exec(); err != nil {
		return err
	}

	roomAlterQueries := []string{
		fmt.Sprintf(`ALTER TABLE %s ADD parent_room_id text`, roomsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD admin_code text`, roomsTable),
	}
	for _, alterQuery := range roomAlterQueries {
		if err := session.Query(alterQuery).Exec(); err != nil {
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
	if err := session.Query(query).Exec(); err != nil {
		return err
	}
	alterQueries := []string{
		fmt.Sprintf(`ALTER TABLE %s ADD created_by_user_id text`, boardElementsTable),
		fmt.Sprintf(`ALTER TABLE %s ADD created_by_name text`, boardElementsTable),
	}
	for _, alterQuery := range alterQueries {
		if err := session.Query(alterQuery).Exec(); err != nil {
			lowered := strings.ToLower(strings.TrimSpace(err.Error()))
			if strings.Contains(lowered, "duplicate") || strings.Contains(lowered, "already exists") {
				continue
			}
			return err
		}
	}
	return nil
}
