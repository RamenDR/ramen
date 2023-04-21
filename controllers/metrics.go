// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"github.com/prometheus/client_golang/prometheus"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
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

const (
	ObjType            = "obj_type"
	ObjName            = "obj_name"
	ObjNamespace       = "obj_namespace"
	Policyname         = "policyname"
	SchedulingInterval = "scheduling_interval"
)

var (
	syncMetricLabelNames = []string{
		ObjType,            // Name of the type of the resource [drpc|vrg]
		ObjName,            // Name of the resource [drpc-name|vrg-name]
		ObjNamespace,       // DRPC namespace name
		Policyname,         // DRPolicy name
		SchedulingInterval, // Value from DRPolicy
	}

	drpolicySyncIntervalMetricLabelNames = []string{
		Policyname, // DRPolicy name
	}
)

var (
	lastSyncTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:      LastSyncTimestampSeconds,
			Namespace: metricNamespace,
			Help:      "Duration of last sync time in seconds",
		},
		syncMetricLabelNames,
	)

	dRPolicySyncInterval = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:      DRPolicySyncIntervalSeconds,
			Namespace: metricNamespace,
			Help:      "Schedule interval for a policy in seconds",
		},
		drpolicySyncIntervalMetricLabelNames,
	)
)

func SyncPolicyMetricLables(drPolicy *rmn.DRPolicy, drpc *rmn.DRPlacementControl) prometheus.Labels {
	return prometheus.Labels{
		ObjType:            "DRPlacementControl",
		ObjName:            drpc.Name,
		ObjNamespace:       drpc.Namespace,
		Policyname:         drPolicy.Name,
		SchedulingInterval: drPolicy.Spec.SchedulingInterval,
	}
}

func NewSyncMetrics(labels prometheus.Labels) SyncMetrics {
	return SyncMetrics{
		LastSyncTime: lastSyncTime.With(labels),
	}
}

func DeleteSyncMetric(labels prometheus.Labels) bool {
	return lastSyncTime.Delete(labels)
}

func DRPolicySyncIntervalMetricLabels(drPolicy *rmn.DRPolicy) prometheus.Labels {
	return prometheus.Labels{Policyname: drPolicy.Name}
}

func NewDRPolicySyncIntervalMetrics(labels prometheus.Labels) DRPolicySyncMetrics {
	return DRPolicySyncMetrics{
		DRPolicySyncInterval: dRPolicySyncInterval.With(labels),
	}
}

func DeleteDRPolicySyncIntervalMetrics(labels prometheus.Labels) bool {
	return dRPolicySyncInterval.Delete(labels)
}

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(lastSyncTime)
	metrics.Registry.MustRegister(dRPolicySyncInterval)
}
