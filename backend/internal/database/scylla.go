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
		log.Printf("☁️  Astra: Connecting via API (ID: %s)...", cfg.AstraDatabaseID)

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
