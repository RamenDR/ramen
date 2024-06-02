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
	"fmt"

	"github.com/go-logr/logr"
	errorswrapper "github.com/pkg/errors"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	corev1 "k8s.io/api/core/v1"
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

func GeneratePolicyResourceNames(
	secret string,
	format TargetSecretFormat,
) (plName, plBindingName, plRuleName, configPolicyName string) {
	var policyName string

	switch format {
	case SecretFormatRamen:
		policyName = ramenFormatPrefix + secret
	case SecretFormatVelero:
		policyName = veleroFormatPrefix + secret
	}

	return policyName,
		fmt.Sprintf(secretResourceNameFormat, secretPlBindingBaseName, policyName),
		fmt.Sprintf(secretResourceNameFormat, secretPlRuleBaseName, policyName),
		fmt.Sprintf(secretResourceNameFormat, secretConfigPolicyBaseName, policyName)
}

func generatePolicyPlacementName(secret string, format TargetSecretFormat) string {
	var policyName string

	switch format {
	case SecretFormatRamen:
		policyName = ramenFormatPrefix + secret
	case SecretFormatVelero:
		policyName = veleroFormatPrefix + secret
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
	}

	return SecretPolicyFinalizer
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

// localSecret is added to provide for an interface that can convert the "template" value in secret.Data
// and store it as a policy object. Currently the actual secret.Data is a map of []byte, which hence garbles
// the value of the template secret value in the policy. Using stringData which is a map of string does not
// work with the configuration controllers, as values from actual secret's data is encoded in base64 twice.
// This needs to be tracked with OCM and fixed, at which point we can remove the local copy and adapt to
// the fix.
type localSecret struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Data              map[string]string `json:"data,omitempty"`
}

// DeepCopyObject interfaces required to use localSecret as a runtime.Object
// Lifted from generated deep copy file for other resources
func (in *localSecret) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopy is for copying the receiver, creating a new ClusterStatus.
func (in *localSecret) DeepCopy() *localSecret {
	if in == nil {
		return nil
	}

	out := new(localSecret)

	in.DeepCopyInto(out)

	return out
}

// DeepCopyInto is for copying the receiver, writing into out. in must be non-nil.
func (in *localSecret) DeepCopyInto(out *localSecret) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)

	if in.Data != nil {
		in, out := &in.Data, &out.Data
		*out = make(map[string]string, len(*in))

		for key, val := range *in {
			(*out)[key] = val
		}
	}
}

func newS3ConfigurationSecret(s3SecretRef corev1.SecretReference, targetns string) *localSecret {
	return &localSecret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s3SecretRef.Name,
			Namespace: targetns,
		},
		Data: map[string]string{
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

func newVeleroSecret(s3SecretRef corev1.SecretReference, fromNS, veleroNS, keyName string) *localSecret {
	return &localSecret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      GenerateVeleroSecretName(s3SecretRef.Name),
			Namespace: veleroNS,
		},
		/*
			keyName contains, base 64 encoded data as follows:
			[default]
			  aws_access_key_id = <key-id>
			  aws_secret_access_key = <key>

			Where, <key-id> and <key> are base 64 decoded values from looked up secret (s3SecretRef.Name)
			in namespace (fromNS)
		*/
		Data: map[string]string{
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
	object := &runtime.RawExtension{}

	switch format {
	case SecretFormatRamen:
		object = &runtime.RawExtension{Object: newS3ConfigurationSecret(s3SecretRef, targetNS)}
	case SecretFormatVelero:
		object = &runtime.RawExtension{
			Object: newVeleroSecret(s3SecretRef, targetNS, veleroNS, VeleroSecretKeyNameDefault),
		}
	}

	return object
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
