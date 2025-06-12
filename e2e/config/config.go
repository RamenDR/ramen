// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/validation"
)

const (
	// Kubernetes distributions
	DistroK8s = "k8s"
	DistroOcp = "ocp"

	// ClusterSet
	DefaultClusterSetName = "default"

	// Channel
	defaultChannelNamespace = "e2e-gitops"

	// Git repository
	defaultGitURL    = "https://github.com/RamenDR/ocm-ramen-samples.git"
	defaultGitBranch = "main"

	// DRPolicy
	defaultDRPolicyName = "dr-policy"
)

// Channel defines the name and namespace for the channel CR.
// This is not user-configurable and always uses default values.
type Channel struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// Namespaces are determined by distro and are not user-configurable.
type Namespaces struct {
	RamenHubNamespace       string `json:"ramenHubNamespace"`
	RamenDRClusterNamespace string `json:"ramenDRClusterNamespace"`
	RamenOpsNamespace       string `json:"ramenOpsNamespace"`
	ArgocdNamespace         string `json:"argocdNamespace"`
}

// Repo represents the user-configurable git repository settings.
// It includes the repository url and branch to be used for deploying workload.
type Repo struct {
	URL    string `json:"url"`
	Branch string `json:"branch"`
}

type PVCSpec struct {
	Name             string `json:"name"`
	StorageClassName string `json:"storageClassName"`
	AccessModes      string `json:"accessModes"`
}

type Cluster struct {
	Kubeconfig string `json:"kubeconfig"`
}

type Test struct {
	Workload string `json:"workload"`
	Deployer string `json:"deployer"`
	PVCSpec  string `json:"pvcSpec"`
}

// Config keeps configuration for e2e tests.
type Config struct {
	// User configurable values.
	Clusters   map[string]Cluster `json:"clusters"`
	ClusterSet string             `json:"clusterSet"`
	Distro     string             `json:"distro"`
	Repo       Repo               `json:"repo"`
	DRPolicy   string             `json:"drPolicy"`
	PVCSpecs   []PVCSpec          `json:"pvcSpecs"`
	Tests      []Test             `json:"tests"`

	// Generated values
	Channel    Channel    `json:"channel"`
	Namespaces Namespaces `json:"namespaces"`
}

// Options that can be used in a configuration file.
type Options struct {
	Workloads []string
	Deployers []string
}

// Default namespace mappings for Kubernetes (k8s) clusters.
var K8sNamespaces = Namespaces{
	RamenHubNamespace:       "ramen-system",
	RamenDRClusterNamespace: "ramen-system",
	RamenOpsNamespace:       "ramen-ops",
	ArgocdNamespace:         "argocd",
}

// Default namespace mappings for OpenShift (ocp) clusters.
var OcpNamespaces = Namespaces{
	RamenHubNamespace:       "openshift-operators",
	RamenDRClusterNamespace: "openshift-dr-system",
	RamenOpsNamespace:       "openshift-dr-ops",
	ArgocdNamespace:         "openshift-gitops",
}

var resourceNameForbiddenCharacters *regexp.Regexp

func ReadConfig(configFile string, options Options) (*Config, error) {
	config := &Config{}

	if err := readConfig(configFile, config); err != nil {
		return nil, err
	}

	if err := validateDistro(config); err != nil {
		return nil, err
	}

	if err := validateClusters(config); err != nil {
		return nil, err
	}

	if err := validatePVCSpecs(config); err != nil {
		return nil, err
	}

	if err := validateTests(config, &options); err != nil {
		return nil, err
	}

	config.Channel.Name = resourceName(config.Repo.URL)
	config.Channel.Namespace = defaultChannelNamespace

	return config, nil
}

func readConfig(configFile string, config *Config) error {
	viper.SetDefault("Repo.URL", defaultGitURL)
	viper.SetDefault("Repo.Branch", defaultGitBranch)
	viper.SetDefault("DRPolicy", defaultDRPolicyName)
	viper.SetDefault("ClusterSet", DefaultClusterSetName)

	viper.SetConfigFile(configFile)

	if err := viper.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read config: %v", err)
	}

	if err := viper.Unmarshal(config); err != nil {
		return fmt.Errorf("failed to unmarshal config: %v", err)
	}

	return nil
}

func validateDistro(config *Config) error {
	// Discover distro during validation if the distro is not configured
	if config.Distro == "" {
		return nil
	}

	switch config.Distro {
	case DistroK8s:
		config.Namespaces = K8sNamespaces
	case DistroOcp:
		config.Namespaces = OcpNamespaces
	default:
		return fmt.Errorf("invalid distro %q: (choose one of %q, %q)",
			config.Distro, DistroK8s, DistroOcp)
	}

	return nil
}

func validateClusters(config *Config) error {
	if config.Clusters["hub"].Kubeconfig == "" {
		return fmt.Errorf("failed to find hub cluster in configuration")
	}

	if config.Clusters["c1"].Kubeconfig == "" {
		return fmt.Errorf("failed to find c1 cluster in configuration")
	}

	if config.Clusters["c2"].Kubeconfig == "" {
		return fmt.Errorf("failed to find c2 cluster in configuration")
	}

	return nil
}

func validatePVCSpecs(config *Config) error {
	if len(config.PVCSpecs) == 0 {
		return fmt.Errorf("failed to find pvcs in configuration")
	}

	if err := validateDuplicatePVCSpecsNames(config.PVCSpecs); err != nil {
		return err
	}

	if err := validateDuplicatePVCSpecsContent(config.PVCSpecs); err != nil {
		return err
	}

	if err := validateStorageClassNameFormat(config.PVCSpecs); err != nil {
		return err
	}

	if err := validateAccessModes(config.PVCSpecs); err != nil {
		return err
	}

	return nil
}

// validateDuplicatePVCSpecsNames ensures no PVCSpec has a duplicate name
func validateDuplicatePVCSpecsNames(pvcSpecs []PVCSpec) error {
	seen := make(map[string]PVCSpec)
	for _, spec := range pvcSpecs {
		if existing, exists := seen[spec.Name]; exists {
			return fmt.Errorf("duplicate pvcSpec name %q found:\n	%+v\n	%+v", spec.Name, existing, spec)
		}

		seen[spec.Name] = spec
	}

	return nil
}

// validateDuplicatePVCSpecsContent ensures no two PVCSpecs have the same storageClassName and accessModes
func validateDuplicatePVCSpecsContent(pvcSpecs []PVCSpec) error {
	seen := make(map[PVCSpec]PVCSpec)

	for _, spec := range pvcSpecs {
		key := PVCSpec{
			StorageClassName: spec.StorageClassName,
			AccessModes:      spec.AccessModes,
		}

		if duplicate, exists := seen[key]; exists {
			return fmt.Errorf("duplicate pvcSpec content found:\n	%+v\n	%+v", duplicate, spec)
		}

		seen[key] = spec
	}

	return nil
}

// validateStorageClassNameFormat checks that each StorageClassName in the given PVC specs
// conforms to Kubernetes DNS subdomain naming rules (RFC 1123).
// https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#dns-subdomain-names
// Returns an error if any StorageClassName in pvcSpec is invalid.
func validateStorageClassNameFormat(pvcSpecs []PVCSpec) error {
	for _, spec := range pvcSpecs {
		errs := validation.NameIsDNSSubdomain(spec.StorageClassName, false)
		if len(errs) > 0 {
			return fmt.Errorf("invalid storageClassName %q in pvcSpec %q: %v",
				spec.StorageClassName, spec.Name, errs)
		}
	}

	return nil
}

// validateAccessModes validates that accessModes is one of the supported values
func validateAccessModes(pvcSpecs []PVCSpec) error {
	validModes := map[corev1.PersistentVolumeAccessMode]struct{}{
		corev1.ReadWriteOnce:    {},
		corev1.ReadOnlyMany:     {},
		corev1.ReadWriteMany:    {},
		corev1.ReadWriteOncePod: {},
	}

	for _, spec := range pvcSpecs {
		mode := corev1.PersistentVolumeAccessMode(spec.AccessModes)
		if _, valid := validModes[mode]; !valid {
			return fmt.Errorf("invalid accessMode %q in pvcSpec %q", mode, spec.Name)
		}
	}

	return nil
}

func validateTests(config *Config, options *Options) error {
	// We allow an empty test list so one can run the validation tests or unit tests without a fully configured file.
	if len(config.Tests) == 0 {
		return nil
	}

	pvcSpecNames := make([]string, 0, len(config.PVCSpecs))
	for _, spec := range config.PVCSpecs {
		pvcSpecNames = append(pvcSpecNames, spec.Name)
	}

	testsSeen := map[Test]struct{}{}

	for _, t := range config.Tests {
		if _, ok := testsSeen[t]; ok {
			return fmt.Errorf("duplicate test (deployer: %q, workload: %q, pvcSpec: %q)",
				t.Deployer, t.Workload, t.PVCSpec)
		}

		if !slices.Contains(options.Deployers, t.Deployer) {
			return fmt.Errorf("invalid test deployer: %q (available %q)", t.Deployer, options.Deployers)
		}

		if !slices.Contains(options.Workloads, t.Workload) {
			return fmt.Errorf("invalid test workload: %q (available %q)", t.Workload, options.Workloads)
		}

		if !slices.Contains(pvcSpecNames, t.PVCSpec) {
			return fmt.Errorf("invalid test pvcSpec: %q (available %q)", t.PVCSpec, pvcSpecNames)
		}

		testsSeen[t] = struct{}{}
	}

	return nil
}

// PVCSpecMap returns a mapping from PVCSpec.Name to PVCSpec.
func PVCSpecsMap(config *Config) map[string]PVCSpec {
	res := map[string]PVCSpec{}
	for _, spec := range config.PVCSpecs {
		res[spec.Name] = spec
	}

	return res
}

// Equal return true if config is equal to other config.
//
//nolint:cyclop
func (c *Config) Equal(o *Config) bool {
	if c == o {
		return true
	}

	if c.Distro != o.Distro {
		return false
	}

	if c.Repo != o.Repo {
		return false
	}

	if c.DRPolicy != o.DRPolicy {
		return false
	}

	if c.ClusterSet != o.ClusterSet {
		return false
	}

	if !maps.Equal(c.Clusters, o.Clusters) {
		return false
	}

	if !slices.Equal(c.PVCSpecs, o.PVCSpecs) {
		return false
	}

	if !slices.Equal(c.Tests, o.Tests) {
		return false
	}

	if c.Channel != o.Channel {
		return false
	}

	if c.Namespaces != o.Namespaces {
		return false
	}

	return true
}

// resourceName convert a URL to conventional k8s resource name:
// "https://github.com/foo/bar.git" -> "https-github-com-foo-bar-git"
// https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#dns-subdomain-names
func resourceName(url string) string {
	return strings.ToLower(resourceNameForbiddenCharacters.ReplaceAllString(url, "-"))
}

func init() {
	// Matches one of more forbidden characters, so we can replace them with single replacement character.
	resourceNameForbiddenCharacters = regexp.MustCompile(`[^\w]+`)
}
