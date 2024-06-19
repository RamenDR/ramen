// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

// NOTE: Added to skip creating shadow manifests for localSecret struct
// +kubebuilder:skip
package util

import (
	"context"
	//nolint:gosec
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	errorswrapper "github.com/pkg/errors"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	cpcv1 "open-cluster-management.io/config-policy-controller/api/v1"
	gppv1 "open-cluster-management.io/governance-policy-propagator/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	//nolint:lll
	// Ref: https://access.redhat.com/documentation/en-us/red_hat_advanced_cluster_management_for_kubernetes/2.4/html/governance/governance#governance-architecture
	policyNameLengthLimit = 63

	secretPlRuleBaseName       = "plrule"
	secretPlBindingBaseName    = "plbinding"
	secretConfigPolicyBaseName = "cfg-policy"

	secretResourceNameFormat string = "%s-%s"

	//nolint:lll
	// See: https://github.com/stolostron/rhacm-docs/blob/2.4_stage/governance/custom_template.adoc#special-annotation-for-reprocessing
	PolicyTriggerAnnotation = "policy.open-cluster-management.io/trigger-update"

	// Finalizer on the secret
	SecretPolicyFinalizer string = "drpolicies.ramendr.openshift.io/policy-protection"

	VeleroSecretKeyNameDefault = "ramengenerated"
)

// TargetSecretFormat defines the secret format to deliver to the cluster
type TargetSecretFormat string

const (
	SecretFormatRamen  TargetSecretFormat = "ramen"
	SecretFormatVelero TargetSecretFormat = "velero"

	// This is a dev time assertion message to detect any new unhandled format in related functions
	unknownFormat = "detected unhandled target secret format"
)

// Prefix length for format, to distinguish policy names for the same secret in the same namespace
const formatPrefixLen = 1

const (
	ramenFormatPrefix  = "" // retain backward compatibility, no prefix
	veleroFormatPrefix = "v"
)

type SecretsUtil struct {
	client.Client
	APIReader client.Reader
	Ctx       context.Context
	Log       logr.Logger
}

// GeneratePolicyResourceNames returns names (in order) for policy resources that are created,
// policyName: Name of the policy
// plBindingName: Name of the PlacementBinding that ties a Policy to a PlacementRule
// plRuleName: Name of the PlacementRule
// configPolicyName: Name of the ConfigurationPolicy resource embedded within the Policy
func GeneratePolicyResourceNames(
	secret string,
	format TargetSecretFormat,
) (policyName, plBindingName, plRuleName, configPolicyName string) {
	switch format {
	case SecretFormatRamen:
		policyName = ramenFormatPrefix + secret
	case SecretFormatVelero:
		policyName = veleroFormatPrefix + secret
	default:
		panic(unknownFormat)
	}

	plBindingName = fmt.Sprintf(secretResourceNameFormat, secretPlBindingBaseName, policyName)
	plRuleName = fmt.Sprintf(secretResourceNameFormat, secretPlRuleBaseName, policyName)
	configPolicyName = fmt.Sprintf(secretResourceNameFormat, secretConfigPolicyBaseName, policyName)

	return
}

func generatePolicyPlacementName(secret string, format TargetSecretFormat) string {
	var policyName string

	switch format {
	case SecretFormatRamen:
		policyName = ramenFormatPrefix + secret
	case SecretFormatVelero:
		policyName = veleroFormatPrefix + secret
	default:
		panic(unknownFormat)
	}

	return fmt.Sprintf(secretResourceNameFormat, secretPlRuleBaseName, policyName)
}

func GenerateVeleroSecretName(sName string) string {
	// Disambiguate with a "v" in case fromNS and veleroNS are the same
	return veleroFormatPrefix + sName
}

func SecretFinalizer(format TargetSecretFormat) string {
	switch format {
	case SecretFormatRamen:
		return SecretPolicyFinalizer
	case SecretFormatVelero:
		return SecretPolicyFinalizer + "-" + string(SecretFormatVelero)
	default:
		panic(unknownFormat)
	}
}

// GeneratePolicyName generates a policy name by combining the word "vs-secret-" with the name.
// However, if the length of the passed-in name is less than or equal to the 'maxLen',
// the passed-in name is returned as-is.
//
// If the passed-in name and the namespace length exceeds 'maxLen', a unique hash of the
// passed-in name is computed using MD5 prepended to it "vs-secret-". If this combined name
// still exceeds 'maxLen', it is trimmed to fit within the limit by removing characters from
// the end of the hash up to maxLen.
//
// Parameters:
//
//	potentialPolicyName: The preferred name of the policy.
//	namespace: The namespace associated with the policy.
//	maxLen: The maximum length of the generated name
//
// Returns:
//
//	"vs-secret" + the generated name, which is either the passed-in name or a modified version that fits
//	  within the allowed length.
//
//nolint:gosec
func GeneratePolicyName(name string, maxLen int) string {
	const prefix = "vs-secret-"
	// Use 3 hex characters as a buffer.
	// From 000 to FFF possible workloads -- disregarding collisions
	const buffer = 3

	// maxLen can't be less than length of "vs-secret"
	if maxLen <= (len(prefix) + buffer) {
		return name
	}

	// If the name is already less than the max, then return the original name
	if len(name) <= maxLen {
		return name
	}

	// Otherwise, generate a name up to 32 characters
	hash := md5.Sum([]byte(name))

	// prefix it and trim if necessary
	policyName := prefix + hex.EncodeToString(hash[:])
	if len(policyName) > maxLen {
		return policyName[:maxLen]
	}

	return policyName
}

func newPlacementRuleBinding(
	name, namespace, placementRuleName string,
	subjects []gppv1.Subject,
) *gppv1.PlacementBinding {
	return &gppv1.PlacementBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PlacementBinding",
			APIVersion: "policy.open-cluster-management.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		PlacementRef: gppv1.PlacementSubject{
			APIGroup: plrv1.Resource("PlacementRule").Group,
			Kind:     plrv1.Resource("PlacementRule").Resource,
			Name:     placementRuleName,
		},
		Subjects: subjects,
	}
}

func newPlacementRule(name string, namespace string,
	clusters []string,
) *plrv1.PlacementRule {
	plRuleClusters := []plrv1.GenericClusterReference{}
	for _, clusterRef := range clusters {
		plRuleClusters = append(plRuleClusters, plrv1.GenericClusterReference{
			Name: clusterRef,
		})
	}

	return &plrv1.PlacementRule{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PlacementRule",
			APIVersion: "apps.open-cluster-management.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: plrv1.PlacementRuleSpec{
			GenericPlacementFields: plrv1.GenericPlacementFields{
				Clusters: plRuleClusters,
			},
		},
	}
}

func newS3ConfigurationSecret(s3SecretRef corev1.SecretReference, targetns string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]interface{}{
			"name":      s3SecretRef.Name,
			"namespace": targetns,
		},
		"type": "Opaque",
		"data": map[string]string{
			"AWS_ACCESS_KEY_ID": "{{hub fromSecret " +
				"\"" + s3SecretRef.Namespace + "\"" + " " +
				"\"" + s3SecretRef.Name + "\"" + " " +
				"\"AWS_ACCESS_KEY_ID\" hub}}",
			"AWS_SECRET_ACCESS_KEY": "{{hub fromSecret " +
				"\"" + s3SecretRef.Namespace + "\"" + " " +
				"\"" + s3SecretRef.Name + "\"" + " " +
				"\"AWS_SECRET_ACCESS_KEY\" hub}}",
		},
	}
}

func newVeleroSecret(s3SecretRef corev1.SecretReference, fromNS, veleroNS, keyName string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]interface{}{
			"name":      GenerateVeleroSecretName(s3SecretRef.Name),
			"namespace": veleroNS,
		},
		"type": "Opaque",
		"data": map[string]string{
			keyName: "{{ (printf \"[default]\\n  aws_access_key_id = %s\\n  aws_secret_access_key = %s\\n\" " +
				"((lookup \"v1\" \"Secret\" \"" + fromNS +
				"\" \"" + s3SecretRef.Name + "\").data.AWS_ACCESS_KEY_ID | base64dec) " +
				"((lookup \"v1\" \"Secret\" \"" + fromNS +
				"\" \"" + s3SecretRef.Name + "\").data.AWS_SECRET_ACCESS_KEY | base64dec)" +
				") | base64enc }}",
		},
	}
}

func newConfigurationPolicy(name string, object *runtime.RawExtension) *cpcv1.ConfigurationPolicy {
	return &cpcv1.ConfigurationPolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigurationPolicy",
			APIVersion: "policy.open-cluster-management.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: &cpcv1.ConfigurationPolicySpec{
			RemediationAction: cpcv1.Enforce,
			Severity:          "high",
			ObjectTemplates: []*cpcv1.ObjectTemplate{
				{
					ComplianceType:   cpcv1.MustHave,
					ObjectDefinition: *object,
				},
			},
		},
	}
}

func newPolicy(name, namespace, triggerValue string, object runtime.RawExtension) *gppv1.Policy {
	return &gppv1.Policy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Policy",
			APIVersion: "policy.open-cluster-management.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				PolicyTriggerAnnotation: triggerValue,
			},
		},
		Spec: gppv1.PolicySpec{
			RemediationAction: gppv1.Enforce,
			Disabled:          false,
			PolicyTemplates: []*gppv1.PolicyTemplate{
				{
					ObjectDefinition: object,
				},
			},
		},
	}
}

func (sutil *SecretsUtil) createPolicyResources(
	secret *corev1.Secret,
	cluster, namespace, targetNS string,
	format TargetSecretFormat,
	veleroNS string,
) error {
	policyName, plBindingName, plRuleName, configPolicyName := GeneratePolicyResourceNames(secret.Name, format)

	sutil.Log.Info("Creating secret policy", "secret", secret.Name, "cluster", cluster, "namespace", namespace)

	if AddFinalizer(secret, SecretFinalizer(format)) {
		if err := sutil.Client.Update(sutil.Ctx, secret); err != nil {
			sutil.Log.Error(err, "unable to add finalizer to secret", "secret", secret.Name, "cluster", cluster)

			return errorswrapper.Wrap(err, fmt.Sprintf("unable to add finalizer to secret (secret: %s, cluster: %s)",
				secret.Name, cluster))
		}
	}

	// Create a PlacementBinding for the Policy object and the placement rule
	subjects := []gppv1.Subject{
		{
			Name:     policyName,
			APIGroup: gppv1.SchemeGroupVersion.WithResource("Policy").GroupResource().Group,
			Kind:     gppv1.SchemeGroupVersion.WithResource("Policy").GroupResource().Resource,
		},
	}

	plRuleBindingObject := newPlacementRuleBinding(plBindingName, namespace, plRuleName, subjects)
	if err := sutil.Client.Create(sutil.Ctx, plRuleBindingObject); err != nil && !errors.IsAlreadyExists(err) {
		sutil.Log.Error(err, "unable to create placement binding", "secret", secret.Name, "cluster", cluster)

		return errorswrapper.Wrap(err, fmt.Sprintf("unable to create placement binding (secret: %s, cluster: %s)",
			secret.Name, cluster))
	}

	// Create a Policy object for the secret
	configObject := newConfigurationPolicy(configPolicyName,
		sutil.policyObject(secret.Name, namespace, targetNS, format, veleroNS))

	sutil.Log.Info("Initializing secret policy trigger", "secret", secret.Name, "trigger", secret.ResourceVersion)

	policyObject := newPolicy(policyName, namespace,
		secret.ResourceVersion, runtime.RawExtension{Object: configObject})
	if err := sutil.Client.Create(sutil.Ctx, policyObject); err != nil && !errors.IsAlreadyExists(err) {
		sutil.Log.Error(err, "unable to create policy", "secret", secret.Name, "cluster", cluster)

		return errorswrapper.Wrap(err, fmt.Sprintf("unable to create policy (secret: %s, cluster: %s)",
			secret.Name, cluster))
	}

	// Create a PlacementRule, including cluster
	plRuleObject := newPlacementRule(plRuleName, namespace, []string{cluster})
	if err := sutil.Client.Create(sutil.Ctx, plRuleObject); err != nil && !errors.IsAlreadyExists(err) {
		sutil.Log.Error(err, "unable to create placement rule", "secret", secret.Name, "cluster", cluster)

		return errorswrapper.Wrap(err, fmt.Sprintf("unable to create placement rule (secret: %s, cluster: %s)",
			secret.Name, cluster))
	}

	return nil
}

func (sutil *SecretsUtil) policyObject(
	secretName, secretNS, targetNS string,
	format TargetSecretFormat,
	veleroNS string,
) *runtime.RawExtension {
	s3SecretRef := corev1.SecretReference{Name: secretName, Namespace: secretNS}

	var object []interface{}

	switch format {
	case SecretFormatRamen:
		object = append(object, newS3ConfigurationSecret(s3SecretRef, targetNS))
	case SecretFormatVelero:
		object = append(object, newVeleroSecret(s3SecretRef, targetNS, veleroNS, VeleroSecretKeyNameDefault))
	default:
		panic(unknownFormat)
	}

	object = append(object, olmClusterRole,
		OlmRoleBinding,
		vrgClusterRole,
		vrgClusterRoleBinding,
		mModeClusterRole,
		mModeClusterRoleBinding)

	object2, err := json.Marshal(object)
	if err != nil {
		return nil
	}

	return &runtime.RawExtension{Raw: object2}
}

func (sutil *SecretsUtil) deletePolicyResources(
	secret *corev1.Secret,
	namespace string,
	format TargetSecretFormat,
) error {
	policyName, plBindingName, plRuleName, _ := GeneratePolicyResourceNames(secret.Name, format)

	sutil.Log.Info("Deleting secret policy", "secret", secret.Name)

	plRuleBindingObject := &gppv1.PlacementBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      plBindingName,
			Namespace: namespace,
		},
	}
	if err := sutil.Client.Delete(sutil.Ctx, plRuleBindingObject); err != nil && !errors.IsNotFound(err) {
		sutil.Log.Error(err, "unable to delete placement binding", "secret", secret.Name)

		return errorswrapper.Wrap(err, fmt.Sprintf("unable to delete placement binding (secret: %s)", secret.Name))
	}

	policyObject := &gppv1.Policy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      policyName,
			Namespace: namespace,
		},
	}
	if err := sutil.Client.Delete(sutil.Ctx, policyObject); err != nil && !errors.IsNotFound(err) {
		sutil.Log.Error(err, "unable to delete policy", "secret", secret.Name)

		return errorswrapper.Wrap(err, fmt.Sprintf("unable to delete policy (secret: %s)", secret.Name))
	}

	plRuleObject := &plrv1.PlacementRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      plRuleName,
			Namespace: namespace,
		},
	}
	if err := sutil.Client.Delete(sutil.Ctx, plRuleObject); err != nil && !errors.IsNotFound(err) {
		sutil.Log.Error(err, "unable to delete placement rule", "secret", secret.Name)

		return errorswrapper.Wrap(err, fmt.Sprintf("unable to delete placement rule (secret: %s)",
			secret.Name))
	}

	// Remove finalizer from secret. Allow secret deletion and recreation ordering for policy tickle
	if controllerutil.ContainsFinalizer(secret, SecretFinalizer(format)) {
		controllerutil.RemoveFinalizer(secret, SecretFinalizer(format))

		if err := sutil.Client.Update(sutil.Ctx, secret); err != nil {
			sutil.Log.Error(err, "unable to remove finalizer from secret", "secret", secret.Name)

			return errorswrapper.Wrap(err, fmt.Sprintf("unable to remove finalizer from secret (secret: %s)",
				secret.Name))
		}
	}

	return nil
}

func inspectClusters(
	clusters []plrv1.GenericClusterReference,
	cluster string,
	add bool,
) (bool, []plrv1.GenericClusterReference) {
	found := false
	survivors := []plrv1.GenericClusterReference{}

	// Check if cluster is already part of placement rule
	for _, plCluster := range clusters {
		if plCluster.Name == cluster {
			if add {
				return true, clusters
			}

			found = true

			continue
		}
		// Build a potential surviving cluster list
		survivors = append(survivors, plCluster)
	}

	return found, survivors
}

func (sutil *SecretsUtil) updatePlacementRule(
	plRule *plrv1.PlacementRule,
	secret *corev1.Secret,
	cluster, namespace string,
	format TargetSecretFormat,
	add bool,
) (bool, error) {
	deleted := true
	found, survivors := inspectClusters(plRule.Spec.Clusters, cluster, add)

	switch add {
	case true:
		if found {
			return !deleted, nil
		}

		plRule.Spec.Clusters = append(plRule.Spec.Clusters, plrv1.GenericClusterReference{Name: cluster})
	case false:
		if len(survivors) == 0 {
			sutil.Log.Info("Deleting empty secret policy", "secret", secret.Name)

			return deleted, sutil.deletePolicyResources(secret, namespace, format)
		}

		if !found {
			return !deleted, nil
		}

		plRule.Spec.Clusters = survivors
	}

	sutil.Log.Info("Updating placement rule for secret policy", "secret", secret.Name, "clusters", plRule.Spec.Clusters)

	err := sutil.Client.Update(sutil.Ctx, plRule)
	if err != nil {
		sutil.Log.Error(err, "unable to update placement rule", "placementRule", plRule.Name, "cluster", cluster)

		return !deleted, errorswrapper.Wrap(err,
			fmt.Sprintf("unable to update placement rule (placementRule: %s, cluster: %s)", plRule.Name, cluster))
	}

	return !deleted, nil
}

// ticklePolicy updates the Policy PolicyTriggerAnnotation with the secret resourceVersion, to deliver refreshed
// secrets based on the Policy to the managed cluster. This is specifically useful where policy contains templated
// secret propagation from the hub.
// (see: https://github.com/open-cluster-management-io/open-cluster-management-io.github.io/blob/448ad30cf9b13a30a82a8f0ed63bb28e1090b132/content/zh/concepts/policy.md?plain=1#L256-L259)
// The resource version of the Secret is used as a secret does not carry a generation number.
func (sutil *SecretsUtil) ticklePolicy(secret *corev1.Secret, namespace string) error {
	policyName := secret.Name
	policyObject := gppv1.Policy{}

	// TODO: Read directly from the API server? May read a cached older trigger and update it to the same value?
	if err := sutil.Client.Get(sutil.Ctx,
		types.NamespacedName{Namespace: namespace, Name: policyName},
		&policyObject); err != nil {
		sutil.Log.Error(err, "unable to get policy", "secret", secret.Name)

		return errorswrapper.Wrap(err, fmt.Sprintf("unable to get policy (secret: %s)", secret.Name))
	}

	for annotation, value := range policyObject.GetAnnotations() {
		if annotation == PolicyTriggerAnnotation && value == secret.ResourceVersion {
			return nil
		}
	}

	sutil.Log.Info("Updating secret policy trigger", "secret", secret.Name, "trigger", secret.ResourceVersion)

	policyObject.Annotations[PolicyTriggerAnnotation] = secret.ResourceVersion
	if err := sutil.Client.Update(sutil.Ctx, &policyObject); err != nil {
		sutil.Log.Error(err, "unable to trigger policy update", "secret", secret.Name)

		return errorswrapper.Wrap(err, fmt.Sprintf("unable to trigger policy update (secret: %s)", secret.Name))
	}

	return nil
}

func (sutil *SecretsUtil) updatePolicyResources(
	plRule *plrv1.PlacementRule,
	secret *corev1.Secret,
	cluster, namespace string,
	format TargetSecretFormat,
	add bool,
) error {
	deleted, err := sutil.updatePlacementRule(plRule, secret, cluster, namespace, format, add)
	if err != nil {
		return err
	}

	if !deleted {
		return sutil.ticklePolicy(secret, namespace)
	}

	return nil
}

func (sutil *SecretsUtil) ensureS3SecretResources(
	secretName, namespace string,
	format TargetSecretFormat,
) (*corev1.Secret, error) {
	secret := corev1.Secret{}
	if err := sutil.Client.Get(sutil.Ctx,
		types.NamespacedName{Namespace: namespace, Name: secretName},
		&secret); err != nil {
		if !errors.IsNotFound(err) {
			return nil, errorswrapper.Wrap(err, "failed to get secret object")
		}

		// Cleanup policy for missing secret
		sutil.Log.Info("Cleaning up secret policy", "secret", secretName)

		secret.Name = secretName

		return nil, sutil.deletePolicyResources(&secret, namespace, format)
	}

	if !ResourceIsDeleted(&secret) {
		return &secret, nil
	}

	// Cleanup policy if secret is deleted
	sutil.Log.Info("Cleaning up secret policy", "secret", secretName)

	return nil, sutil.deletePolicyResources(&secret, namespace, format)
}

// AddSecretToCluster takes in a secret (secretName) in the Ramen S3 secret format in a namespace and uses OCM Policy
// to deliver it to the desired cluster (clusterName), in the desired namespace (targetNS). It accepts a format that
// can help convert the secret in the hub cluster to a desired format on the target cluster.
// The format SecretFormatVelero needs an additional argument veleroNS which is the namespace for the velero
// formatted secret, to be delivered from the targetNS (which requires that the secret first be delivered to
// the targetNS)
func (sutil *SecretsUtil) AddSecretToCluster(
	secretName, clusterName, namespace, targetNS string,
	format TargetSecretFormat,
	veleroNS string,
) error {
	sutil.Log.Info("Add Secret", "cluster", clusterName, "secret", secretName, "format", format)

	if len(secretName)+len(namespace)+len(".")+formatPrefixLen > policyNameLengthLimit {
		return fmt.Errorf("secret namespace.name (%s.%s) length exceeds maximum character limit (%d)",
			secretName, namespace, policyNameLengthLimit)
	}

	if format == SecretFormatVelero && veleroNS == "" {
		return fmt.Errorf("requested format (%s) requires a target namespace", SecretFormatVelero)
	}

	secret, err := sutil.ensureS3SecretResources(secretName, namespace, format)
	if err != nil {
		return err
	}

	if secret == nil {
		return fmt.Errorf("failed to find secret (secret: %s, cluster: %s)", secretName, clusterName)
	}

	plRule := &plrv1.PlacementRule{}
	plRuleName := types.NamespacedName{
		Namespace: namespace,
		Name:      generatePolicyPlacementName(secretName, format),
	}

	// Fetch secret placement rule, create secret resources if not found
	err = sutil.APIReader.Get(sutil.Ctx, plRuleName, plRule)
	if err != nil {
		if !errors.IsNotFound(err) {
			return errorswrapper.Wrap(err, "failed to get placementRule object")
		}

		return sutil.createPolicyResources(secret, clusterName, namespace, targetNS, format, veleroNS)
	}

	return sutil.updatePolicyResources(plRule, secret, clusterName, namespace, format, true)
}

// RemoveSecretFromCluster removes the secret (secretName) in namespace, from clusterName in the format requested.
// If this was the last cluster that required the secret to be delivered in the requested format, then the related
// policy resources are also deleted as part of the removal.
func (sutil *SecretsUtil) RemoveSecretFromCluster(
	secretName, clusterName, namespace string,
	format TargetSecretFormat,
) error {
	sutil.Log.Info("Remove Secret", "cluster", clusterName, "secret", secretName)

	secret, err := sutil.ensureS3SecretResources(secretName, namespace, format)
	if err != nil {
		return err
	}

	if secret == nil {
		return nil
	}

	plRule := &plrv1.PlacementRule{}
	plRuleName := types.NamespacedName{
		Namespace: namespace,
		Name:      generatePolicyPlacementName(secretName, format),
	}

	// Fetch secret placement rule, success if not found
	err = sutil.APIReader.Get(sutil.Ctx, plRuleName, plRule)
	if err != nil {
		if !errors.IsNotFound(err) {
			return errorswrapper.Wrap(err, "failed to get placementRule object")
		}

		// Ensure all related resources and finalizers are deleted
		return sutil.deletePolicyResources(secret, namespace, format)
	}

	return sutil.updatePolicyResources(plRule, secret, clusterName, namespace, format, false)
}

var (
	olmClusterRole = &rbacv1.ClusterRole{
		TypeMeta:   metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "open-cluster-management:klusterlet-work-sa:agent:olm-edit"},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"operators.coreos.com"},
				Resources: []string{"operatorgroups"},
				Verbs:     []string{"create", "get", "list", "update", "delete"},
			},
		},
	}

	OlmRoleBinding = &rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{Kind: "RoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "open-cluster-management:klusterlet-work-sa:agent:olm-edit",
			Namespace: "",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "klusterlet-work-sa",
				Namespace: "open-cluster-management-agent",
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "open-cluster-management:klusterlet-work-sa:agent:olm-edit",
		},
	}

	vrgClusterRole = &rbacv1.ClusterRole{
		TypeMeta:   metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "open-cluster-management:klusterlet-work-sa:agent:volrepgroup-edit"},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"ramendr.openshift.io"},
				Resources: []string{"volumereplicationgroups"},
				Verbs:     []string{"create", "get", "list", "update", "delete"},
			},
		},
	}

	vrgClusterRoleBinding = &rbacv1.ClusterRoleBinding{
		TypeMeta:   metav1.TypeMeta{Kind: "ClusterRoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "open-cluster-management:klusterlet-work-sa:agent:volrepgroup-edit"},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "klusterlet-work-sa",
				Namespace: "open-cluster-management-agent",
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "open-cluster-management:klusterlet-work-sa:agent:volrepgroup-edit",
		},
	}

	mModeClusterRole = &rbacv1.ClusterRole{
		TypeMeta:   metav1.TypeMeta{Kind: "ClusterRole", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "open-cluster-management:klusterlet-work-sa:agent:mmode-edit"},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"ramendr.openshift.io"},
				Resources: []string{"maintenancemodes"},
				Verbs:     []string{"create", "get", "list", "update", "delete"},
			},
		},
	}

	mModeClusterRoleBinding = &rbacv1.ClusterRoleBinding{
		TypeMeta:   metav1.TypeMeta{Kind: "ClusterRoleBinding", APIVersion: "rbac.authorization.k8s.io/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "open-cluster-management:klusterlet-work-sa:agent:mmode-edit"},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "klusterlet-work-sa",
				Namespace: "open-cluster-management-agent",
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "open-cluster-management:klusterlet-work-sa:agent:mmode-edit",
		},
	}
)
