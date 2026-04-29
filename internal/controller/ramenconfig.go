// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	configv1alpha1 "k8s.io/component-base/config/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/yaml"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	rameninternalconfig "github.com/ramendr/ramen/internal/config"
)

const (
	hubName                                           = "hub"
	drClusterName                                     = "dr-cluster"
	operatorNamePrefix                                = "ramen-"
	operatorNameSuffix                                = "-operator"
	hubOperatorNameDefault                            = operatorNamePrefix + hubName + operatorNameSuffix
	drClusterOperatorNameDefault                      = operatorNamePrefix + drClusterName + operatorNameSuffix
	configMapNameSuffix                               = "-config"
	HubOperatorConfigMapName                          = hubOperatorNameDefault + configMapNameSuffix
	DrClusterOperatorConfigMapName                    = drClusterOperatorNameDefault + configMapNameSuffix
	leaderElectionResourceNameSuffix                  = ".ramendr.openshift.io"
	HubLeaderElectionResourceName                     = hubName + leaderElectionResourceNameSuffix
	drClusterLeaderElectionResourceName               = drClusterName + leaderElectionResourceNameSuffix
	ConfigMapRamenConfigKeyName                       = "ramen_manager_config.yaml"
	drClusterOperatorPackageNameDefault               = drClusterOperatorNameDefault
	drClusterOperatorChannelNameDefault               = "alpha"
	drClusterOperatorCatalogSourceNameDefault         = "ramen-catalog"
	drClusterOperatorClusterServiceVersionNameDefault = drClusterOperatorPackageNameDefault + ".v0.0.1"
	DefaultCephFSCSIDriverName                        = "openshift-storage.cephfs.csi.ceph.com"
	VeleroNamespaceNameDefault                        = "velero"
	DefaultVolSyncCopyMethod                          = "Snapshot"
	defaultMaxConcurrentReconciles                    = 50
)

// FIXME
const NoS3StoreAvailable = "NoS3"

var ControllerType ramendrv1alpha1.ControllerType

func DefaultRamenConfig(controllerType ramendrv1alpha1.ControllerType) *ramendrv1alpha1.RamenConfig {
	var leaderElectionResourceName string

	switch controllerType {
	case ramendrv1alpha1.DRHubType:
		leaderElectionResourceName = HubLeaderElectionResourceName
	case ramendrv1alpha1.DRClusterType:
		leaderElectionResourceName = drClusterLeaderElectionResourceName
	default:
		panic(fmt.Sprintf("unknown controller type %q", controllerType))
	}

	leaderElect := true

	cfg := &ramendrv1alpha1.RamenConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: ramendrv1alpha1.GroupVersion.String(),
			Kind:       "RamenConfig",
		},
		MaxConcurrentReconciles: defaultMaxConcurrentReconciles,
		Health: ramendrv1alpha1.ControllerHealth{
			HealthProbeBindAddress: ":8081",
		},
		Metrics: ramendrv1alpha1.ControllerMetrics{
			BindAddress: "127.0.0.1:9289",
		},
		LeaderElection: &configv1alpha1.LeaderElectionConfiguration{
			LeaderElect:  &leaderElect,
			ResourceName: leaderElectionResourceName,
		},
		RamenOpsNamespace:         "ramen-ops",
		VolumeUnprotectionEnabled: true,
	}

	cfg.DrClusterOperator.ChannelName = drClusterOperatorChannelNameDefault
	cfg.DrClusterOperator.PackageName = drClusterOperatorPackageNameDefault
	cfg.DrClusterOperator.CatalogSourceName = drClusterOperatorCatalogSourceNameDefault
	cfg.DrClusterOperator.DeploymentAutomationEnabled = true
	cfg.DrClusterOperator.S3SecretDistributionEnabled = true

	cfg.KubeObjectProtection.VeleroNamespaceName = VeleroNamespaceNameDefault
	cfg.VolSync.DestinationCopyMethod = "Direct"
	cfg.VolSync.Disabled = false

	cfg.MultiNamespace.FeatureEnabled = true
	cfg.MultiNamespace.VolsyncSupported = true

	return cfg
}

func LoadControllerConfig(configFile string,
	log logr.Logger,
) (ramenConfig *ramendrv1alpha1.RamenConfig) {
	controllerType := os.Getenv("RAMEN_CONTROLLER_TYPE")
	if controllerType == "" {
		panic(fmt.Errorf("RAMEN_CONTROLLER_TYPE environment variable must be set"))
	}

	ct := ramendrv1alpha1.ControllerType(controllerType)
	if ct != ramendrv1alpha1.DRHubType && ct != ramendrv1alpha1.DRClusterType {
		panic(fmt.Errorf("invalid controller type specified (%s), should be one of [%s|%s]",
			ct, ramendrv1alpha1.DRHubType, ramendrv1alpha1.DRClusterType))
	}

	ControllerType = ct

	log.Info("loading Ramen configuration from defaults", "controllerType", ct)

	return DefaultRamenConfig(ct)
}

func LoadControllerOptions(options *ctrl.Options, ramenConfig *ramendrv1alpha1.RamenConfig) {
	if ramenConfig == nil {
		return
	}

	options.HealthProbeBindAddress = ramenConfig.Health.HealthProbeBindAddress

	// Use controller-runtime built-in auth for metrics
	if ramenConfig.Metrics.BindAddress == "0" {
		options.Metrics = metricsserver.Options{BindAddress: "0"}
	} else {
		options.Metrics = metricsserver.Options{
			BindAddress:    ramenConfig.Metrics.BindAddress,
			SecureServing:  true,
			FilterProvider: filters.WithAuthenticationAndAuthorization,
		}
	}

	if ramenConfig.LeaderElection != nil {
		if ramenConfig.LeaderElection.LeaderElect != nil {
			options.LeaderElection = *ramenConfig.LeaderElection.LeaderElect
		}

		if ramenConfig.LeaderElection.ResourceName != "" {
			options.LeaderElectionID = ramenConfig.LeaderElection.ResourceName
		}
	}
}

func GetRamenConfigS3StoreProfile(ctx context.Context, apiReader client.Reader, profileName string) (
	s3StoreProfile ramendrv1alpha1.S3StoreProfile, err error,
) {
	_, ramenConfig, err := ConfigMapGet(ctx, apiReader)
	if err != nil {
		return s3StoreProfile, err
	}

	s3StoreProfilePointer := RamenConfigS3StoreProfilePointerGet(ramenConfig, profileName)

	if s3StoreProfilePointer == nil {
		err = fmt.Errorf("s3 profile %s not found in RamenConfig", profileName)

		return s3StoreProfile, err
	}

	s3StoreProfile = *s3StoreProfilePointer

	err = s3StoreProfileFormatCheck(&s3StoreProfile)

	return
}

func RamenConfigS3StoreProfilePointerGet(ramenConfig *ramendrv1alpha1.RamenConfig, profileName string,
) *ramendrv1alpha1.S3StoreProfile {
	for i := range ramenConfig.S3StoreProfiles {
		s3Profile := &ramenConfig.S3StoreProfiles[i]
		if s3Profile.S3ProfileName == profileName {
			return s3Profile
		}
	}

	return nil
}

func s3StoreProfileFormatCheck(s3StoreProfile *ramendrv1alpha1.S3StoreProfile) (err error) {
	s3Endpoint := s3StoreProfile.S3CompatibleEndpoint
	if s3Endpoint == "" {
		err = fmt.Errorf("s3 endpoint has not been configured in s3 profile %s",
			s3StoreProfile.S3ProfileName)

		return err
	}

	_, err = url.ParseRequestURI(s3Endpoint)
	if err != nil {
		err = fmt.Errorf("invalid s3 endpoint <%s> in "+
			"profile %s, reason: %w", s3Endpoint, s3StoreProfile.S3ProfileName, err)

		return err
	}

	s3Bucket := s3StoreProfile.S3Bucket
	if s3Bucket == "" {
		err = fmt.Errorf("s3 bucket has not been configured in s3 profile %s",
			s3StoreProfile.S3ProfileName)

		return err
	}

	return nil
}

func getMaxConcurrentReconciles(ramenConfig *ramendrv1alpha1.RamenConfig) int {
	const defaultMaxConcurrentReconciles = 1

	if ramenConfig == nil {
		return defaultMaxConcurrentReconciles
	}

	if ramenConfig.MaxConcurrentReconciles == 0 {
		return defaultMaxConcurrentReconciles
	}

	return ramenConfig.MaxConcurrentReconciles
}

func ramenOperatorConfigMapName() string {
	switch ControllerType {
	case ramendrv1alpha1.DRHubType:
		return HubOperatorConfigMapName
	case ramendrv1alpha1.DRClusterType:
		return DrClusterOperatorConfigMapName
	default:
		panic(fmt.Errorf("invalid controller type specified (%s), should be one of [%s|%s]",
			ControllerType, ramendrv1alpha1.DRHubType, ramendrv1alpha1.DRClusterType))
	}
}

func CreateOrUpdateConfigMap(
	ctx context.Context,
	c client.Client,
	r client.Reader,
	defaultRamenConfig *ramendrv1alpha1.RamenConfig,
	log logr.Logger,
) (*ramendrv1alpha1.RamenConfig, error) {
	if defaultRamenConfig == nil {
		return nil, fmt.Errorf("defaultRamenConfig must not be nil")
	}

	configMapName := ramenOperatorConfigMapName()

	configMap := &corev1.ConfigMap{}
	key := types.NamespacedName{
		Namespace: RamenOperatorNamespace(),
		Name:      configMapName,
	}

	if err := r.Get(ctx, key, configMap); err != nil {
		if !k8serrors.IsNotFound(err) {
			return nil, err
		}

		return configMapCreate(ctx, c, key.Name, defaultRamenConfig, log)
	}

	defaultYAML, err := yaml.Marshal(defaultRamenConfig)
	if err != nil {
		return nil, err
	}

	userYAML := []byte(configMap.Data[ConfigMapRamenConfigKeyName])

	merged, err := rameninternalconfig.Merge(defaultYAML, userYAML)
	if err != nil {
		return nil, err
	}

	return configMapUpdate(ctx, c, configMap, &merged, log)
}

func configMapCreate(
	ctx context.Context,
	c client.Client,
	configMapName string,
	desiredRamenConfig *ramendrv1alpha1.RamenConfig,
	log logr.Logger,
) (*ramendrv1alpha1.RamenConfig, error) {
	userKey := types.NamespacedName{
		Namespace: RamenOperatorNamespace(),
		Name:      configMapName,
	}

	newConfigMap, err := ConfigMapNew(userKey.Namespace, userKey.Name, desiredRamenConfig)
	if err != nil {
		return nil, err
	}

	if err = c.Create(ctx, newConfigMap); err != nil {
		return nil, err
	}

	log.Info("created configmap", "namespace", newConfigMap.Namespace, "name", newConfigMap.Name)

	return desiredRamenConfig, nil
}

func disownOLMManagedConfigMap(cm *corev1.ConfigMap, log logr.Logger) {
	// Older OLM installs created this ConfigMap from the CSV bundle.
	// When upgrading to a non-OLM-owned ConfigMap, remove ownership metadata so
	// the ConfigMap is not garbage collected when the old CSV is deleted.
	if len(cm.OwnerReferences) > 0 {
		log.Info("Removing ramen configmap owner references",
			"name", cm.Name,
			"namespace", cm.Namespace,
			"ownerReferences", cm.OwnerReferences)
		cm.OwnerReferences = nil
	}

	for _, key := range []string{
		"olm.managed",
		"operators.coreos.com/odr-hub-operator.openshift-operators",
	} {
		if v, ok := cm.Labels[key]; ok {
			log.Info("Remove ramen config map label",
				"key", key,
				"value", v,
				"name", cm.Name,
				"namespace", cm.Namespace)
			delete(cm.Labels, key)
		}
	}
}

func configMapUpdate(
	ctx context.Context,
	c client.Client,
	userConfigMap *corev1.ConfigMap,
	desiredRamenConfig *ramendrv1alpha1.RamenConfig,
	log logr.Logger,
) (*ramendrv1alpha1.RamenConfig, error) {
	disownOLMManagedConfigMap(userConfigMap, log)

	desiredBytes, err := yaml.Marshal(desiredRamenConfig)
	if err != nil {
		return nil, err
	}

	if userConfigMap.Data == nil {
		userConfigMap.Data = map[string]string{}
	}

	userConfigMap.Data[ConfigMapRamenConfigKeyName] = string(desiredBytes)
	if err := c.Update(ctx, userConfigMap); err != nil {
		return nil, err
	}

	log.Info("updated configmap (merged onto defaults)",
		"namespace", userConfigMap.Namespace, "name", userConfigMap.Name)

	return desiredRamenConfig, nil
}

func ConfigMapNew(
	namespaceName string,
	name string,
	ramenConfig *ramendrv1alpha1.RamenConfig,
) (*corev1.ConfigMap, error) {
	ramenConfigYaml, err := yaml.Marshal(ramenConfig)
	if err != nil {
		return nil, fmt.Errorf("config map yaml marshal %w", err)
	}

	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespaceName,
		},
		Data: map[string]string{
			ConfigMapRamenConfigKeyName: string(ramenConfigYaml),
		},
	}, nil
}

func ConfigMapGet(
	ctx context.Context,
	apiReader client.Reader,
) (configMap *corev1.ConfigMap, ramenConfig *ramendrv1alpha1.RamenConfig, err error) {
	configMapName := ramenOperatorConfigMapName()

	configMap = &corev1.ConfigMap{}
	if err = apiReader.Get(
		ctx,
		types.NamespacedName{
			Namespace: RamenOperatorNamespace(),
			Name:      configMapName,
		},
		configMap,
	); err != nil {
		return
	}

	ramenConfig = &ramendrv1alpha1.RamenConfig{}
	err = yaml.Unmarshal([]byte(configMap.Data[ConfigMapRamenConfigKeyName]), ramenConfig)

	return
}

func RamenOperatorNamespace() string {
	return os.Getenv("POD_NAMESPACE")
}

func RamenOperandsNamespace(config ramendrv1alpha1.RamenConfig) string {
	return config.RamenOpsNamespace
}

// vrgAdminNamespaceNames returns the namespace names where the vrg objects can
// be created for multi namespace protection.  The list includes the namespace
// where the ramen operator pod is running.  This is to keep backward
// compatibility with existing multi namespace protection.
func vrgAdminNamespaceNames(config ramendrv1alpha1.RamenConfig) []string {
	return []string{RamenOperandsNamespace(config), RamenOperatorNamespace()}
}

// drpcAdminNamespaceName returns the namespace name where the drpc objects can
// be created for multi namespace protection. The DRPC must be created only in
// RamenOperandsNamespace for multi namespace protection.
func drpcAdminNamespaceName(config ramendrv1alpha1.RamenConfig) string {
	return RamenOperandsNamespace(config)
}

func drClusterOperatorChannelNameOrDefault(ramenConfig *ramendrv1alpha1.RamenConfig) string {
	if ramenConfig.DrClusterOperator.ChannelName == "" {
		return drClusterOperatorChannelNameDefault
	}

	return ramenConfig.DrClusterOperator.ChannelName
}

func drClusterOperatorPackageNameOrDefault(ramenConfig *ramendrv1alpha1.RamenConfig) string {
	if ramenConfig.DrClusterOperator.PackageName == "" {
		return drClusterOperatorPackageNameDefault
	}

	return ramenConfig.DrClusterOperator.PackageName
}

func drClusterOperatorNamespaceNameOrDefault(ramenConfig *ramendrv1alpha1.RamenConfig) string {
	if ramenConfig.DrClusterOperator.NamespaceName == "" {
		return RamenOperatorNamespace()
	}

	return ramenConfig.DrClusterOperator.NamespaceName
}

func drClusterOperatorCatalogSourceNameOrDefault(ramenConfig *ramendrv1alpha1.RamenConfig) string {
	if ramenConfig.DrClusterOperator.CatalogSourceName == "" {
		return drClusterOperatorCatalogSourceNameDefault
	}

	return ramenConfig.DrClusterOperator.CatalogSourceName
}

func drClusterOperatorCatalogSourceNamespaceNameOrDefault(ramenConfig *ramendrv1alpha1.RamenConfig) string {
	if ramenConfig.DrClusterOperator.CatalogSourceNamespaceName == "" {
		return RamenOperatorNamespace()
	}

	return ramenConfig.DrClusterOperator.CatalogSourceNamespaceName
}

func drClusterOperatorClusterServiceVersionNameOrDefault(ramenConfig *ramendrv1alpha1.RamenConfig) string {
	if ramenConfig.DrClusterOperator.ClusterServiceVersionName == "" {
		return drClusterOperatorClusterServiceVersionNameDefault
	}

	return ramenConfig.DrClusterOperator.ClusterServiceVersionName
}

func cephFSCSIDriverNameOrDefault(ramenConfig *ramendrv1alpha1.RamenConfig) string {
	if ramenConfig.VolSync.CephFSCSIDriverName == "" {
		return DefaultCephFSCSIDriverName
	}

	return ramenConfig.VolSync.CephFSCSIDriverName
}

func volSyncDestinationCopyMethodOrDefault(ramenConfig *ramendrv1alpha1.RamenConfig) string {
	if ramenConfig.VolSync.DestinationCopyMethod == "" {
		return DefaultVolSyncCopyMethod
	}

	return ramenConfig.VolSync.DestinationCopyMethod
}
