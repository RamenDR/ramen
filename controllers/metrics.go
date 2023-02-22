package controllers

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	metricNamespace = "ramen"
)

const (
	LastSyncTimestampSeconds    = "last_sync_timestamp_seconds"
	DRPolicySyncIntervalSeconds = "policy_schedule_interval_seconds"
)

type SyncMetrics struct {
	LastSyncTime prometheus.Gauge
}

type DRPolicySyncMetrics struct {
	DRPolicySyncInterval prometheus.Gauge
}

var (
	syncMetricLabels = []string{
		"resource_type",       // Name of the type of the resource [drpc|vrg]
		"name",                // Name of the resource [drpc-name|vrg-name]
		"namespace",           // DRPC namespace name
		"policyname",          // DRPolicy name
		"scheduling_interval", // Value from DRPolicy
	}

	drpolicySyncIntervalMetricLabels = []string{
		"policyname", // DRPolicy name
	}
)

var (
	lastSyncTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:      LastSyncTimestampSeconds,
			Namespace: metricNamespace,
			Help:      "Duration of last sync time in seconds",
		},
		syncMetricLabels,
	)

	dRPolicySyncInterval = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:      DRPolicySyncIntervalSeconds,
			Namespace: metricNamespace,
			Help:      "Schedule interval for a policy in seconds",
		},
		drpolicySyncIntervalMetricLabels,
	)
)

func NewSyncMetrics(labels prometheus.Labels) SyncMetrics {
	return SyncMetrics{
		LastSyncTime: lastSyncTime.With(labels),
	}
}

func NewDRPolicySyncIntervalMetrics(labels prometheus.Labels) DRPolicySyncMetrics {
	return DRPolicySyncMetrics{
		DRPolicySyncInterval: dRPolicySyncInterval.With(labels),
	}
}

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(lastSyncTime)
	metrics.Registry.MustRegister(dRPolicySyncInterval)
}
