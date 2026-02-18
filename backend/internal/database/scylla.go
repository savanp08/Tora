package database

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

var keyspaceNamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)

func NewScyllaStore(cfg config.Config) (store *ScyllaStore, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("scylla init panic: %v", recovered)
		}
	}()

	var (
		cluster *gocql.ClusterConfig
		mode    string
	)

	astraBundlePath := resolveAstraBundlePath(cfg.AstraBundlePath)
	astraAPIURL := normalizeAstraControlPlaneURL(cfg.AstraAPIURL)
	astraToken := strings.TrimSpace(cfg.AstraToken)
	astraDatabaseID := strings.TrimSpace(cfg.AstraDatabaseID)

	if astraBundlePath != "" {
		if astraToken == "" {
			return nil, fmt.Errorf("astra bundle mode requires ASTRA_TOKEN")
		}

		cluster, err = gocqlastra.NewClusterFromBundle(astraBundlePath, "token", astraToken, 30*time.Second)
		if err != nil {
			return nil, fmt.Errorf("create astra cluster from bundle: %w", err)
		}
		mode = "astra_bundle"
	} else if astraToken != "" && astraDatabaseID != "" {
		if astraAPIURL == "" {
			astraAPIURL = "https://api.astra.datastax.com"
		}
		cluster, err = gocqlastra.NewClusterFromURL(astraAPIURL, astraDatabaseID, astraToken, 30*time.Second)
		if err != nil {
			return nil, fmt.Errorf("create astra cluster from url: %w", err)
		}
		mode = "astra_url"
	} else {
		cluster = gocql.NewCluster(cfg.ScyllaHosts...)
		cluster.Consistency = gocql.Quorum
		mode = "local_scylla"
	}

	keyspace := strings.TrimSpace(cfg.ScyllaKeyspace)
	if keyspace == "" {
		keyspace = "converse"
	}

	cluster.ConnectTimeout = 30 * time.Second
	cluster.Timeout = 30 * time.Second
	cluster.ReconnectionPolicy = &gocql.ExponentialReconnectionPolicy{
		InitialInterval: 1 * time.Second,
		MaxInterval:     10 * time.Minute,
	}

	if strings.HasPrefix(mode, "astra_") {
		// Astra path: avoid setting keyspace during session init to bypass gocql UseKeyspace panics.
		cluster.Keyspace = ""
		session, err := cluster.CreateSession()
		if err != nil {
			return nil, fmt.Errorf("create scylla session (%s): %w", mode, err)
		}
		return &ScyllaStore{Session: session, Keyspace: keyspace}, nil
	}

	cluster.Keyspace = ""
	bootstrapSession, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("create bootstrap scylla session (%s): %w", mode, err)
	}
	defer bootstrapSession.Close()

	if err := ensureKeyspaceExists(bootstrapSession, keyspace); err != nil {
		return nil, fmt.Errorf("ensure keyspace %q: %w", keyspace, err)
	}

	cluster.Keyspace = keyspace
	session, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("create scylla session (%s): %w", mode, err)
	}

	return &ScyllaStore{Session: session, Keyspace: keyspace}, nil
}

func (s *ScyllaStore) Close() {
	if s == nil || s.Session == nil {
		return
	}
	s.Session.Close()
}

func (s *ScyllaStore) Table(name string) string {
	trimmedName := strings.TrimSpace(name)
	if s == nil {
		return trimmedName
	}

	trimmedKeyspace := strings.TrimSpace(s.Keyspace)
	if trimmedName == "" || trimmedKeyspace == "" {
		return trimmedName
	}
	if strings.Contains(trimmedName, ".") {
		return trimmedName
	}
	if !keyspaceNamePattern.MatchString(trimmedName) || !keyspaceNamePattern.MatchString(trimmedKeyspace) {
		return trimmedName
	}

	return trimmedKeyspace + "." + trimmedName
}

func resolveAstraBundlePath(raw string) string {
	cleaned := filepath.Clean(strings.TrimSpace(raw))
	if cleaned == "" || cleaned == "." {
		return ""
	}

	candidates := []string{
		cleaned,
		filepath.Join("backend", cleaned),
		filepath.Join("..", cleaned),
		filepath.Join("..", "backend", cleaned),
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}

	return ""
}

func normalizeAstraControlPlaneURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}

	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "apps.astra.datastax.com") {
		return "https://api.astra.datastax.com"
	}

	return trimmed
}

func ensureKeyspaceExists(session *gocql.Session, keyspace string) error {
	if session == nil {
		return fmt.Errorf("scylla session is not configured")
	}

	trimmed := strings.TrimSpace(keyspace)
	if !keyspaceNamePattern.MatchString(trimmed) {
		return fmt.Errorf("invalid keyspace name: %q", keyspace)
	}

	queries := []string{
		fmt.Sprintf(
			"CREATE KEYSPACE IF NOT EXISTS %s WITH replication = {'class': 'NetworkTopologyStrategy', 'datacenter1': '1'}",
			trimmed,
		),
		fmt.Sprintf(
			"CREATE KEYSPACE IF NOT EXISTS %s WITH replication = {'class': 'SimpleStrategy', 'replication_factor': '1'}",
			trimmed,
		),
	}

	var lastErr error
	for _, query := range queries {
		if err := safeQueryExec(session, query); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no keyspace creation query was attempted")
	}
	return lastErr
}

func safeQueryExec(session *gocql.Session, query string) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("query panic: %v", recovered)
		}
	}()
	return session.Query(query).Exec()
}
