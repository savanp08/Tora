package database

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	gocqlastra "github.com/datastax/gocql-astra"
	"github.com/savanp08/converse/internal/config"

	"github.com/gocql/gocql"
)

type ScyllaStore struct {
	Session *gocql.Session
}

func NewScyllaStore(cfg config.Config) (*ScyllaStore, error) {
	var (
		cluster *gocql.ClusterConfig
		err     error
		mode    string
	)

	astraBundle := strings.TrimSpace(cfg.AstraBundlePath)
	astraClientID := strings.TrimSpace(cfg.AstraClientID)
	astraClientSecret := strings.TrimSpace(cfg.AstraClientSecret)
	astraAPIURL := strings.TrimSpace(cfg.AstraAPIURL)
	astraDatabaseID := strings.TrimSpace(cfg.AstraDatabaseID)
	astraToken := strings.TrimSpace(cfg.AstraToken)
	if astraBundle != "" {
		bundlePath := resolveAstraBundlePath(astraBundle)
		cluster, err = gocqlastra.NewClusterFromBundle(bundlePath, astraClientID, astraClientSecret, 10*time.Second)
		if err != nil {
			return nil, fmt.Errorf("create astra cluster from bundle: %w", err)
		}
		mode = "astra_bundle"
	} else if astraAPIURL != "" && astraDatabaseID != "" && astraToken != "" {
		cluster, err = gocqlastra.NewClusterFromURL(astraAPIURL, astraDatabaseID, astraToken, 10*time.Second)
		if err != nil {
			return nil, fmt.Errorf("create astra cluster from url: %w", err)
		}
		mode = "astra_url"
	} else {
		cluster = gocql.NewCluster(cfg.ScyllaHosts...)
		cluster.Consistency = gocql.Quorum
		mode = "local_scylla"
	}

	cluster.Keyspace = cfg.ScyllaKeyspace
	cluster.ConnectTimeout = 10 * time.Second
	cluster.Timeout = 10 * time.Second

	session, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("create scylla session (%s): %w", mode, err)
	}

	return &ScyllaStore{Session: session}, nil
}

func (s *ScyllaStore) Close() {
	s.Session.Close()
}

func resolveAstraBundlePath(raw string) string {
	cleaned := filepath.Clean(strings.TrimSpace(raw))
	if cleaned == "" {
		return ""
	}

	candidates := []string{
		cleaned,
		filepath.Join("backend", cleaned),
		filepath.Join("..", cleaned),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	return cleaned
}
