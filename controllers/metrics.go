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
	DRPolicySyncIntervalSeconds = "policy_schedule_interval_seconds"
)

const (
	LastSyncTimestampSeconds = "last_sync_timestamp_seconds"
	LastSyncDurationSeconds  = "last_sync_duration_seconds"
	LastSyncDataBytes        = "last_sync_data_bytes"
	WorkloadProtectionStatus = "workload_protection_status"
)

type SyncMetrics struct {
	LastSyncTime prometheus.Gauge
}

type DRPolicySyncMetrics struct {
	DRPolicySyncInterval prometheus.Gauge
}

type SyncDurationMetrics struct {
	LastSyncDuration prometheus.Gauge
}

type SyncDataBytesMetrics struct {
	LastSyncDataBytes prometheus.Gauge
}

type WorkloadProtectionMetrics struct {
	WorkloadProtectionStatus prometheus.Gauge
}

type DRPCMetrics struct {
	SyncMetrics
	SyncDurationMetrics
	SyncDataBytesMetrics
	WorkloadProtectionMetrics
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

	syncDurationMetricLabelNames = []string{
		ObjType,            // Name of the type of the resource [drpc]
		ObjName,            // Name of the resoure [drpc-name]
		ObjNamespace,       // DRPC namespace name
		SchedulingInterval, // Value from DRPolicy
	}

	syncDataBytesMetricLabels = []string{
		ObjType,            // Name of the type of the resource [drpc]
		ObjName,            // Name of the resoure [drpc-name]
		ObjNamespace,       // DRPC namespace name
		SchedulingInterval, // Value from DRPolicy
	}

	workloadProtectionStatusLabels = []string{
		ObjType,      // Name of the type of the resource [drpc]
		ObjName,      // Name of the resoure [drpc-name]
		ObjNamespace, // DRPC namespace
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

	lastSyncDuration = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:      LastSyncDurationSeconds,
			Namespace: metricNamespace,
			Help:      "Duration of max sync time in seconds",
		},
		syncDurationMetricLabelNames,
	)

	lastSyncDataBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:      LastSyncDataBytes,
			Namespace: metricNamespace,
			Help:      "Total data synced in bytes since last sync",
		},
		syncDataBytesMetricLabels,
	)

	workloadProtectionStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:      WorkloadProtectionStatus,
			Namespace: metricNamespace,
			Help:      "Status regarding workload protection health",
		},
		workloadProtectionStatusLabels,
	)
)

// lastSyncTime metrics reports value from lastGrpupSyncTime taken from DRPC status
func SyncMetricLabels(drPolicy *rmn.DRPolicy, drpc *rmn.DRPlacementControl) prometheus.Labels {
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

// dRPolicySyncInterval Metrics reports the value from schedulingInterval from DRPolicy
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

// lastSyncDuration Metrics reports value from lastGroupSyncDuration from DRPC status
func SyncDurationMetricLabels(drPolicy *rmn.DRPolicy, drpc *rmn.DRPlacementControl) prometheus.Labels {
	return prometheus.Labels{
		ObjType:            "DRPlacementControl",
		ObjName:            drpc.Name,
		ObjNamespace:       drpc.Namespace,
		SchedulingInterval: drPolicy.Spec.SchedulingInterval,
	}
}

func NewSyncDurationMetric(labels prometheus.Labels) SyncDurationMetrics {
	return SyncDurationMetrics{
		LastSyncDuration: lastSyncDuration.With(labels),
	}
}

func DeleteSyncDurationMetric(labels prometheus.Labels) bool {
	return lastSyncDuration.Delete(labels)
}

// lastSyncDataBytes Metric reports value from lastGroupSyncBytes taken from DRPC status
func SyncDataBytesMetricLabels(drPolicy *rmn.DRPolicy, drpc *rmn.DRPlacementControl) prometheus.Labels {
	return prometheus.Labels{
		ObjType:            "DRPlacementControl",
		ObjName:            drpc.Name,
		ObjNamespace:       drpc.Namespace,
		SchedulingInterval: drPolicy.Spec.SchedulingInterval,
	}
}

func NewSyncDataBytesMetric(labels prometheus.Labels) SyncDataBytesMetrics {
	return SyncDataBytesMetrics{
		LastSyncDataBytes: lastSyncDataBytes.With(labels),
	}
}

func DeleteSyncDataBytesMetric(labels prometheus.Labels) bool {
	return lastSyncDataBytes.Delete(labels)
}

// workloadProtectionStatus Metric reports information regarding workload protection condition from DRPC
func WorkloadProtectionStatusLabels(drpc *rmn.DRPlacementControl) prometheus.Labels {
	return prometheus.Labels{
		ObjType:      "DRPlacementControl",
		ObjName:      drpc.Name,
		ObjNamespace: drpc.Namespace,
	}
}

func NewWorkloadProtectionStatusMetric(labels prometheus.Labels) WorkloadProtectionMetrics {
	return WorkloadProtectionMetrics{
		WorkloadProtectionStatus: workloadProtectionStatus.With(labels),
	}
}

func DeleteWorkloadProtectionStatusMetric(labels prometheus.Labels) bool {
	return workloadProtectionStatus.Delete(labels)
}

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(dRPolicySyncInterval)
	metrics.Registry.MustRegister(lastSyncTime)
	metrics.Registry.MustRegister(lastSyncDuration)
	metrics.Registry.MustRegister(lastSyncDataBytes)
	metrics.Registry.MustRegister(workloadProtectionStatus)
}
