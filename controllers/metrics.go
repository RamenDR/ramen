package controllers

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	metricNamespace = "ramen"
)

const (
	LastSyncTimestampSeconds = "last_sync_timestamp_seconds"
)

type SyncMetrics struct {
	LastSyncTime prometheus.Gauge
}

var (
	metricLabels = []string{
		"resource_type",       // Name of the type of the resource [drpc|vrg]
		"name",                // Name of the resource [drpc-name|vrg-name]
		"namespace",           // DRPC namespace name
		"policyname",          // DRPolicy name
		"scheduling_interval", // Value from DRPolicy
	}

	lastSyncTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:      LastSyncTimestampSeconds,
			Namespace: metricNamespace,
			Help:      "Duration of last sync time in seconds",
		},
		metricLabels,
	)
)

func NewSyncMetrics(lables prometheus.Labels) SyncMetrics {
	return SyncMetrics{
		LastSyncTime: lastSyncTime.With(lables),
	}
}

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(lastSyncTime)
}
