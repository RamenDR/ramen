// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	recipe "github.com/ramendr/recipe/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	"github.com/ramendr/ramen/e2e/argocd"
	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
	"github.com/ramendr/ramen/e2e/util"
)

const (
	deploymentName           = "deploy"
	deploymentAppName        = "busybox"
	deploymentPath           = "workloads/deployment/base"
	deploymentSelectResource = "deployment"

	//nolint:lll
	// deploymentMinimumReplicasAvailable is added in a deployment when it has its minimum replicas required available.
	// https://github.com/kubernetes/kubernetes/blob/95bff1b249e048b7e36ae857d7478dd2ac05346a/pkg/controller/deployment/util/deployment_util.go#L97
	deploymentMinimumReplicasAvailable = "MinimumReplicasAvailable"

	kustomizeBuildTimeout = 2 * time.Minute
)

type Deployment struct {
	Name     string
	Branch   string
	PVCSpec  config.PVCSpec
	PVCNames []string
}

func NewDeployment(repo config.Repo, pvcSpec config.PVCSpec) (types.Workload, error) {
	pvcNames, err := fetchDeploymentPVCNames(repo.URL, repo.Branch)
	if err != nil {
		return nil, err
	}

	return &Deployment{
		Name:     fmt.Sprintf("%s-%s", deploymentName, pvcSpec.Name),
		Branch:   repo.Branch,
		PVCSpec:  pvcSpec,
		PVCNames: pvcNames,
	}, nil
}

// fetchDeploymentPVCNames builds the deployment kustomization from the git repo and returns the
// PersistentVolumeClaim names in the rendered manifests.
func fetchDeploymentPVCNames(repoURL, branch string) ([]string, error) {
	url := repoURL + "/" + deploymentPath + "?ref=" + branch

	manifests, err := downloadManifests(url)
	if err != nil {
		return nil, err
	}

	pvcNames := make([]string, 0, len(manifests))

	for _, manifest := range manifests {
		pvc, ok, err := unmarshalPVC(manifest)
		if err != nil {
			return nil, fmt.Errorf("failed to parse manifest in kustomization %q: %w", url, err)
		}

		if ok {
			pvcNames = append(pvcNames, pvc.GetName())
		}
	}

	if len(pvcNames) == 0 {
		return nil, fmt.Errorf("no persistent volume claims found in kustomization %q", url)
	}

	return pvcNames, nil
}

func downloadManifests(url string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kustomizeBuildTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "kubectl", "kustomize", url).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to build kustomization %q: %w", url, err)
	}

	docs := strings.Split(string(out), "---\n")
	manifests := make([]string, 0, len(docs))

	for _, doc := range docs {
		if strings.TrimSpace(doc) == "" {
			continue
		}

		manifests = append(manifests, doc)
	}

	return manifests, nil
}

func unmarshalPVC(doc string) (*unstructured.Unstructured, bool, error) {
	obj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal([]byte(doc), obj); err != nil {
		return nil, false, err
	}

	if obj.GetAPIVersion() != corev1.SchemeGroupVersion.String() || obj.GetKind() != "PersistentVolumeClaim" {
		return nil, false, nil
	}

	return obj, true, nil
}

func (w Deployment) GetAppName() string {
	return deploymentAppName
}

func (w Deployment) GetName() string {
	return w.Name
}

func (w Deployment) GetPath() string {
	return deploymentPath
}

func (w Deployment) GetBranch() string {
	return w.Branch
}

func (w Deployment) GetSelectResource() string {
	return deploymentSelectResource
}

func (w Deployment) GetLabelSelector() *metav1.LabelSelector {
	return &metav1.LabelSelector{MatchLabels: map[string]string{"appname": deploymentAppName}}
}

func (w Deployment) GetChecks(namespace string) []*recipe.Check {
	return []*recipe.Check{
		{
			Name:      "check-replicas",
			Condition: "{$.spec.replicas} == {$.status.readyReplicas}",
		},
		{
			Name:      "check-available",
			Condition: "{$.spec.replicas} == {$.status.availableReplicas}",
		},
	}
}

func (w Deployment) GetOperations(namespace string) []*recipe.Operation {
	return []*recipe.Operation{
		{
			Name:    "ls",
			Command: "/bin/sh -c ls",
		},
		{
			Name:    "echo",
			Command: "/bin/sh -c echo 'Hello'",
		},
	}
}

type jsonPatch struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

func (w Deployment) Kustomize() (string, error) {
	if w.PVCSpec.StorageClassName == "" && w.PVCSpec.AccessModes == "" {
		return "", nil
	}

	scName := "rook-ceph-block"
	if w.PVCSpec.StorageClassName != "" {
		scName = w.PVCSpec.StorageClassName
	}

	accessMode := string(corev1.ReadWriteOnce)
	if w.PVCSpec.AccessModes != "" {
		accessMode = w.PVCSpec.AccessModes
	}

	ops, err := json.Marshal([]jsonPatch{
		{Op: "replace", Path: "/spec/storageClassName", Value: scName},
		{Op: "add", Path: "/spec/accessModes", Value: []string{accessMode}},
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal PVC patch: %w", err)
	}

	patches := make(argocd.KustomizePatches, 0, len(w.PVCNames))

	for _, pvcName := range w.PVCNames {
		patches = append(patches, argocd.KustomizePatch{
			Target: &argocd.KustomizeSelector{
				KustomizeResId: argocd.KustomizeResId{
					KustomizeGvk: argocd.KustomizeGvk{Kind: "PersistentVolumeClaim"},
					Name:         pvcName,
				},
			},
			Patch: string(ops),
		})
	}

	kustomize := &argocd.ApplicationSourceKustomize{Patches: patches}

	out, err := json.Marshal(kustomize)
	if err != nil {
		return "", fmt.Errorf("failed to marshal kustomize: %w", err)
	}

	return string(out), nil
}

func (w Deployment) GetResources() error {
	// this would be a common function given the vars? But we need the resources Kustomized
	return nil
}

// Check the workload health deployed in a cluster
func (w Deployment) Health(ctx types.TestContext, cluster *types.Cluster) error {
	deploy, err := getDeployment(ctx, cluster, ctx.AppNamespace(), w.GetAppName())
	if err != nil {
		return err
	}

	if deploy.GetGeneration() != deploy.Status.ObservedGeneration {
		return fmt.Errorf("deployment \"%s/%s\" status has stale generation in cluster %q (expected: %d, observed: %d)",
			ctx.AppNamespace(), w.GetAppName(), cluster.Name, deploy.GetGeneration(), deploy.Status.ObservedGeneration)
	}

	condition := findDeploymentCondition(deploy.Status.Conditions, appsv1.DeploymentAvailable)
	if condition == nil {
		return fmt.Errorf("deployment \"%s/%s\" missing %q condition in cluster %q",
			ctx.AppNamespace(), w.GetAppName(), appsv1.DeploymentAvailable, cluster.Name)
	}

	if condition.Status != corev1.ConditionTrue {
		return fmt.Errorf("deployment \"%s/%s\" condition %q is %q in cluster %q: %s",
			ctx.AppNamespace(), w.GetAppName(), appsv1.DeploymentAvailable,
			condition.Status, cluster.Name, condition.Message)
	}

	if condition.Reason != deploymentMinimumReplicasAvailable {
		return fmt.Errorf("deployment \"%s/%s\" condition %q has reason %q but expected %q in cluster %q",
			ctx.AppNamespace(), w.GetAppName(), appsv1.DeploymentAvailable,
			condition.Reason, deploymentMinimumReplicasAvailable, cluster.Name)
	}

	for _, pvcName := range w.PVCNames {
		if err := checkPVCHealth(ctx, cluster, ctx.AppNamespace(), pvcName); err != nil {
			return err
		}
	}

	return nil
}

// Status returns the deployment status across managed clusters.
func (w Deployment) Status(ctx types.TestContext) ([]types.WorkloadStatus, error) {
	var statuses []types.WorkloadStatus

	for _, cluster := range ctx.Env().ManagedClusters() {
		status, err := w.statusForCluster(ctx, cluster)
		if err != nil {
			return nil, fmt.Errorf("error checking application \"%s/%s\" on cluster %q: %w",
				ctx.AppNamespace(), w.GetAppName(), cluster.Name, err)
		}

		if status.Status != types.ApplicationNotFound {
			statuses = append(statuses, status)
		}
	}

	return statuses, nil
}

func (w Deployment) statusForCluster(ctx types.TestContext, cluster *types.Cluster) (types.WorkloadStatus, error) {
	deploymentExist, err := findDeployment(ctx, cluster, ctx.AppNamespace(), w.GetAppName())
	if err != nil {
		return types.WorkloadStatus{}, err
	}

	pvcsFound := 0

	for _, pvcName := range w.PVCNames {
		pvcExist, err := findPVC(ctx, cluster, ctx.AppNamespace(), pvcName)
		if err != nil {
			return types.WorkloadStatus{}, err
		}

		if pvcExist {
			pvcsFound++
		}
	}

	var status types.ApplicationStatus

	switch {
	case deploymentExist && pvcsFound == len(w.PVCNames):
		status = types.ApplicationFound
	case deploymentExist || pvcsFound > 0:
		status = types.ApplicationPartial
	default:
		status = types.ApplicationNotFound
	}

	return types.WorkloadStatus{ClusterName: cluster.Name, Status: status}, nil
}

func getDeployment(ctx types.TestContext, cluster *types.Cluster, namespace, name string) (*appsv1.Deployment, error) {
	deploy := &appsv1.Deployment{}
	key := k8stypes.NamespacedName{Name: name, Namespace: namespace}

	err := cluster.Client.Get(ctx.Context(), key, deploy)
	if err != nil {
		return nil, err
	}

	return deploy, nil
}

func findDeployment(ctx types.TestContext, cluster *types.Cluster, namespace, name string) (bool, error) {
	_, err := getDeployment(ctx, cluster, namespace, name)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return false, err
		}

		return false, nil
	}

	return true, nil
}

func getPVC(
	ctx types.TestContext,
	cluster *types.Cluster,
	namespace, name string,
) (*corev1.PersistentVolumeClaim, error) {
	pvc := &corev1.PersistentVolumeClaim{}
	key := k8stypes.NamespacedName{Name: name, Namespace: namespace}

	err := cluster.Client.Get(ctx.Context(), key, pvc)
	if err != nil {
		return nil, err
	}

	return pvc, nil
}

// checkPVCHealth verifies the application PVC is Bound and not being deleted.
// Bound returns nil. Pending is retryable. NotFound, Lost, or a set
// deletionTimestamp are unrecoverable so wait loops fail immediately.
func checkPVCHealth(
	ctx types.TestContext,
	cluster *types.Cluster,
	namespace, pvcName string,
) error {
	pvc, err := getPVC(ctx, cluster, namespace, pvcName)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}

		return fmt.Errorf("pvc \"%s/%s\" not found in cluster %q: %w",
			namespace, pvcName, cluster.Name, util.ErrUnrecoverable)
	}

	if !pvc.GetDeletionTimestamp().IsZero() {
		return fmt.Errorf("pvc \"%s/%s\" is being deleted in cluster %q: %w",
			namespace, pvcName, cluster.Name, util.ErrUnrecoverable)
	}

	switch pvc.Status.Phase {
	case corev1.ClaimBound:
		return nil
	case corev1.ClaimPending:
		return fmt.Errorf("pvc \"%s/%s\" phase is %q, expected %q in cluster %q",
			namespace, pvcName, pvc.Status.Phase, corev1.ClaimBound, cluster.Name)
	case corev1.ClaimLost:
		return fmt.Errorf("pvc \"%s/%s\" phase is %q in cluster %q: %w",
			namespace, pvcName, pvc.Status.Phase, cluster.Name, util.ErrUnrecoverable)
	default:
		return fmt.Errorf("pvc \"%s/%s\" phase is %q, expected %q in cluster %q",
			namespace, pvcName, pvc.Status.Phase, corev1.ClaimBound, cluster.Name)
	}
}

func findPVC(ctx types.TestContext, cluster *types.Cluster, namespace, name string) (bool, error) {
	_, err := getPVC(ctx, cluster, namespace, name)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return false, err
		}

		return false, nil
	}

	return true, nil
}

func findDeploymentCondition(
	conditions []appsv1.DeploymentCondition,
	conditionType appsv1.DeploymentConditionType,
) *appsv1.DeploymentCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}

	return nil
}

func init() {
	register(deploymentName, NewDeployment)
}
