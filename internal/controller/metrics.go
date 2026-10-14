// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
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
	CGEnabled                = "unsupported_consistency_grouping_enabled"
	GlobalActionStatus       = "global_action_consensus_status"
	// Added for drpc progression state
	DRProgressionState = "progression_state"
)

const (
	InvalidCIDRsDetected = "invalid_cidrs_detected"
	UndetectedCIDRsFound = "undetected_cidrs_found"
)

// DR telemetry metrics, forwarded to Red Hat Telemetry via the
// cluster-monitoring-operator allowlist. Label cardinality is bounded
// (3 + 2 + 2 = 7 series) as required by telemetry.
const (
	DRPolicyTypeName    = "dr_policy_type"
	DRProtectedAppsName = "dr_protected_apps"
	DRActionsName       = "dr_actions"
)

const (
	DRTypeLabel           = "dr_type"
	ManagementMethodLabel = "management_method"
	ActionLabel           = "action"
)

const (
	DRTypeMetro    = "metro"
	DRTypeRegional = "regional"
	DRTypeUnknown  = "unknown"

	ManagementMethodDiscovered = "discovered"
	ManagementMethodManaged    = "managed"

	ActionFailover = "failover"
	ActionRelocate = "relocate"
)

type SyncTimeMetrics struct {
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
type CGEnabledMetrics struct {
	CGEnabled prometheus.Gauge
}

type GlobalActionMetrics struct {
	GlobalActionStatus prometheus.Gauge
}

type InvalidCIDRsDetectedMetrics struct {
	InvalidCIDRsDetected prometheus.Gauge
}

type UndetectedCIDRsFoundMetrics struct {
	UndetectedCIDRsFound prometheus.Gauge
}

type DRProgressionStateMetrics struct {
	DRProgressionState prometheus.Gauge
}

type SyncMetrics struct {
	SyncTimeMetrics
	SyncDurationMetrics
	SyncDataBytesMetrics
}

const (
	ObjType               = "obj_type"
	ObjName               = "obj_name"
	ObjNamespace          = "obj_namespace"
	Policyname            = "policyname"
	SchedulingInterval    = "scheduling_interval"
	ProgressionStateLabel = "state"
)

var (
	syncTimeMetricLabelNames = []string{
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

	cgEnabledMetricLabels = []string{
		ObjType,      // Name of the type of the resource [drpc]
		ObjName,      // Name of the resoure [drpc-name]
		ObjNamespace, // DRPC namespace
	}

	globalActionLabels = []string{
		ObjType,      // Name of the type of the resource [drpc]
		ObjName,      // Name of the resoure [drpc-name]
		ObjNamespace, // DRPC namespace
	}

	cidrMetricLabels = []string{
		ObjType, // Name of the type of the resource [DRCluster]
		ObjName, // Name of the resoure [DRCluster-name]
	}

	drProgressionStateMetricsLabels = []string{
		ObjType,      // Name of the type of the resource [drpc]
		ObjName,      // Name of the protected application [drpc-name]
		ObjNamespace, // Protected namespace
		ProgressionStateLabel,
	}
)

var (
	lastSyncTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:      LastSyncTimestampSeconds,
			Namespace: metricNamespace,
			Help:      "Duration of last sync time in seconds",
		},
		syncTimeMetricLabelNames,
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

	cgEnabled = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:      CGEnabled,
			Namespace: metricNamespace,
			Help:      "Unsupported consistency grouping enabled status",
		},
		cgEnabledMetricLabels,
	)

	globalAction = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:      GlobalActionStatus,
			Namespace: metricNamespace,
			Help:      "Status regarding global action consensus",
		},
		globalActionLabels,
	)

	invalidCIDRsDetected = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:      InvalidCIDRsDetected,
			Namespace: metricNamespace,
			Help:      "Invalid CIDRs Detected status",
		},
		cidrMetricLabels,
	)

	undetectedCIDRsFound = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:      UndetectedCIDRsFound,
			Namespace: metricNamespace,
			Help:      "CIDRs configured in DRCluster not detected by the storage provisioner",
		},
		cidrMetricLabels,
	)

	drpcProgressionState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:      DRProgressionState,
			Namespace: metricNamespace,
			Help:      "DRPC progression state indicator; emitted only when WaitOnUserToCleanUp is active",
		},
		drProgressionStateMetricsLabels,
	)

	drPolicyType = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:      DRPolicyTypeName,
			Namespace: metricNamespace,
			Help:      "Count of DRPolicy resources by DR type",
		},
		[]string{DRTypeLabel},
	)

	drProtectedApps = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:      DRProtectedAppsName,
			Namespace: metricNamespace,
			Help:      "Count of DR protected applications (DRPC resources) by management method",
		},
		[]string{ManagementMethodLabel},
	)

	drActions = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:      DRActionsName,
			Namespace: metricNamespace,
			Help:      "Cumulative count of DR actions initiated by existing DRPC resources",
		},
		[]string{ActionLabel},
	)
)

// lastSyncTime metrics reports value from lastGrpupSyncTime taken from DRPC status
func SyncTimeMetricLabels(drPolicy *rmn.DRPolicy, drpc *rmn.DRPlacementControl) prometheus.Labels {
	return prometheus.Labels{
		ObjType:            "DRPlacementControl",
		ObjName:            drpc.Name,
		ObjNamespace:       drpc.Namespace,
		Policyname:         drPolicy.Name,
		SchedulingInterval: drPolicy.Spec.SchedulingInterval,
	}
}

func NewSyncTimeMetric(labels prometheus.Labels) SyncTimeMetrics {
	return SyncTimeMetrics{
		LastSyncTime: lastSyncTime.With(labels),
	}
}

func DeleteSyncTimeMetric(labels prometheus.Labels) bool {
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

// CGEnabled Metric reports information if consistency grouping is enabled for a DRPC
func CGEnabledMetricLabels(drpc *rmn.DRPlacementControl) prometheus.Labels {
	return prometheus.Labels{
		ObjType:      "DRPlacementControl",
		ObjName:      drpc.Name,
		ObjNamespace: drpc.Namespace,
	}
}

func NewCGEnabledMetric(labels prometheus.Labels) CGEnabledMetrics {
	return CGEnabledMetrics{
		CGEnabled: cgEnabled.With(labels),
	}
}

func DeleteCGEnabledMetric(labels prometheus.Labels) bool {
	return cgEnabled.Delete(labels)
}

func GlobalActionLabels(drpc *rmn.DRPlacementControl) prometheus.Labels {
	return prometheus.Labels{
		ObjType:      "DRPlacementControl",
		ObjName:      drpc.Name,
		ObjNamespace: drpc.Namespace,
	}
}

func NewGlobalActionMetric(labels prometheus.Labels) GlobalActionMetrics {
	return GlobalActionMetrics{
		GlobalActionStatus: globalAction.With(labels),
	}
}

func DeleteGlobalActionMetric(labels prometheus.Labels) bool {
	return globalAction.Delete(labels)
}

// InvalidCIDRsDetected Metric reports if CIDRs configured are valid for fencing
func DRClusterCIDRsMetricLabels(drc *rmn.DRCluster) prometheus.Labels {
	return prometheus.Labels{
		ObjType: "DRCluster",
		ObjName: drc.Name,
	}
}

func NewInvalidCIDRsDetectedMetric(labels prometheus.Labels) InvalidCIDRsDetectedMetrics {
	return InvalidCIDRsDetectedMetrics{
		InvalidCIDRsDetected: invalidCIDRsDetected.With(labels),
	}
}

func DeleteInvalidCIDRsDetectedMetric(labels prometheus.Labels) bool {
	return invalidCIDRsDetected.Delete(labels)
}

// UndetectedCIDRsFound Metric reports if CIDRs configured in DRCluster are not detected by the storage provisioner
func NewUndetectedCIDRsFoundMetric(labels prometheus.Labels) UndetectedCIDRsFoundMetrics {
	return UndetectedCIDRsFoundMetrics{
		UndetectedCIDRsFound: undetectedCIDRsFound.With(labels),
	}
}

func DeleteUndetectedCIDRsFoundMetric(labels prometheus.Labels) bool {
	return undetectedCIDRsFound.Delete(labels)
}

func DRProgressionStateMetricLabels(drpc *rmn.DRPlacementControl,
	state string,
) prometheus.Labels {
	return prometheus.Labels{
		ObjType:               "DRPlacementControl",
		ObjName:               drpc.Name,
		ObjNamespace:          drpc.Namespace,
		ProgressionStateLabel: state,
	}
}

func NewDRPCProgressionStateMetric(labels prometheus.Labels) DRProgressionStateMetrics {
	return DRProgressionStateMetrics{
		DRProgressionState: drpcProgressionState.With(labels),
	}
}

func DeleteDRPCProgressionStateMetric(labels prometheus.Labels) bool {
	return drpcProgressionState.Delete(labels)
}

// SetDRPolicyTypeMetric sets the count of DRPolicy resources for a DR type
// (DRTypeMetro, DRTypeRegional or DRTypeUnknown)
func SetDRPolicyTypeMetric(drType string, value float64) {
	drPolicyType.WithLabelValues(drType).Set(value)
}

// SetDRProtectedAppsMetric sets the count of DRPC resources for a management
// method (ManagementMethodDiscovered or ManagementMethodManaged)
func SetDRProtectedAppsMetric(method string, value float64) {
	drProtectedApps.WithLabelValues(method).Set(value)
}

// SetDRActionsMetric sets the cumulative count of DR actions for an action
// (ActionFailover or ActionRelocate)
func SetDRActionsMetric(action string, value float64) {
	drActions.WithLabelValues(action).Set(value)
}

// InitDRTelemetryMetrics initializes all DR telemetry metric label
// combinations to 0, so that a series being present with value 0 indicates
// Ramen is installed with no DR resources, as opposed to an absent series
func InitDRTelemetryMetrics() {
	for _, drType := range []string{DRTypeMetro, DRTypeRegional, DRTypeUnknown} {
		SetDRPolicyTypeMetric(drType, 0)
	}

	for _, method := range []string{ManagementMethodDiscovered, ManagementMethodManaged} {
		SetDRProtectedAppsMetric(method, 0)
	}

	for _, action := range []string{ActionFailover, ActionRelocate} {
		SetDRActionsMetric(action, 0)
	}
}

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(dRPolicySyncInterval)
	metrics.Registry.MustRegister(lastSyncTime)
	metrics.Registry.MustRegister(lastSyncDuration)
	metrics.Registry.MustRegister(lastSyncDataBytes)
	metrics.Registry.MustRegister(workloadProtectionStatus)
	metrics.Registry.MustRegister(cgEnabled)
	metrics.Registry.MustRegister(globalAction)
	metrics.Registry.MustRegister(invalidCIDRsDetected)
	metrics.Registry.MustRegister(undetectedCIDRsFound)
	metrics.Registry.MustRegister(drpcProgressionState)
	metrics.Registry.MustRegister(drPolicyType)
	metrics.Registry.MustRegister(drProtectedApps)
	metrics.Registry.MustRegister(drActions)
}
