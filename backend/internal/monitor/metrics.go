package monitor

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	ActiveConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "converse",
		Subsystem: "ws",
		Name:      "active_connections",
		Help:      "Current number of active WebSocket connections.",
	})

	ActiveRooms = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "converse",
		Subsystem: "ws",
		Name:      "active_rooms",
		Help:      "Current number of active rooms loaded in memory.",
	})

	TotalUploads = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "converse",
		Subsystem: "upload",
		Name:      "requests_total",
		Help:      "Total number of upload requests grouped by status.",
	}, []string{"status"})

	UploadBytes = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "converse",
		Subsystem: "upload",
		Name:      "bytes",
		Help:      "Distribution of uploaded file sizes in bytes.",
		Buckets: []float64{
			8 * 1024,
			32 * 1024,
			128 * 1024,
			512 * 1024,
			1 * 1024 * 1024,
			4 * 1024 * 1024,
			12 * 1024 * 1024,
			25 * 1024 * 1024,
			50 * 1024 * 1024,
		},
	})

	AIRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "converse",
		Subsystem: "ai",
		Name:      "requests_total",
		Help:      "Total AI requests grouped by provider and status.",
	}, []string{"provider", "status"})

	AILimitChecksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "converse",
		Subsystem: "ai",
		Name:      "limit_checks_total",
		Help:      "Total AI limiter checks grouped by scope and decision.",
	}, []string{"scope", "decision"})

	CodeExecutionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "converse",
		Subsystem: "execution",
		Name:      "requests_total",
		Help:      "Total code execution requests grouped by language and status.",
	}, []string{"language", "status"})

	SecurityBlocksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "converse",
		Subsystem: "security",
		Name:      "blocks_total",
		Help:      "Total blocked requests grouped by reason.",
	}, []string{"reason"})
)
