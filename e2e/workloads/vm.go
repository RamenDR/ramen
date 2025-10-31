// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

import (
	"fmt"

	recipe "github.com/ramendr/recipe/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	virtv1 "kubevirt.io/api/core/v1"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
)

const (
	vmName    = "vm-pvc"
	vmAppName = "vm"
	vmPath    = "workloads/kubevirt/vm-pvc/base"
	vmPVCName = "root-disk"
)

type VM struct {
	Name    string
	Branch  string
	PVCSpec config.PVCSpec
}

func NewVM(branch string, pvcSpec config.PVCSpec) types.Workload {
	return &VM{
		Name:    fmt.Sprintf("%s-%s", vmName, pvcSpec.Name),
		Branch:  branch,
		PVCSpec: pvcSpec,
	}
}

func (w *VM) GetAppName() string {
	return vmAppName
}

func (w *VM) GetName() string {
	return w.Name
}

func (w *VM) GetPath() string {
	return vmPath
}

func (w *VM) GetBranch() string {
	return w.Branch
}

func (w *VM) GetSelectResource() string {
	return "kubevirt.io/v1/virtualmachines"
}

func (w *VM) GetLabelSelector() *metav1.LabelSelector {
	return &metav1.LabelSelector{MatchLabels: map[string]string{"appname": vmAppName}}
}

// TODO: To be implemented according to the VM workload
func (w *VM) GetChecks(namespace string) []*recipe.Check {
	return nil
}

// TODO: To be implemented according to the VM workload
func (w *VM) GetOperations(namespace string) []*recipe.Operation {
	return nil
}

func (w *VM) Kustomize() string {
	patch := `{
				"patches": [{
					"target": {
						"kind": "VirtualMachine",
						"name": "vm"
					},
					"patch": "- op: add\n  path: /spec/template/spec/domain/devices/interfaces/0/bridge\n  value: {}"
				},
				{
					"target": {
						"kind": "PersistentVolumeClaim",
						"name": "root-disk"
					},
					"patch": "- op: replace\n  path: /spec/storageClassName\n  value: ` + w.PVCSpec.StorageClassName +
		`\n- op: add\n  path: /spec/accessModes\n  value: [` + w.PVCSpec.AccessModes + `]"
				}]
			}`

	return patch
}

func (w *VM) GetResources() error {
	// this would be a common function given the vars? But we need the resources Kustomized
	return nil
}

// Health method checks if the VirtualMachine status updates are safe and are as desired,
// failing which will report an appropriate error message
func (w *VM) Health(ctx types.TestContext, cluster *types.Cluster) error {
	vm, err := getVM(ctx, cluster, ctx.AppNamespace(), w.GetAppName())
	if err != nil {
		return err
	}

	if vm.GetGeneration() != vm.Status.ObservedGeneration {
		return fmt.Errorf("vm \"%s/%s\" status has stale generation in cluster %q (expected: %d, observed: %d)",
			ctx.AppNamespace(), w.GetAppName(), cluster.Name, vm.GetGeneration(), vm.Status.ObservedGeneration)
	}

	condition := findVMCondition(vm.Status.Conditions, virtv1.VirtualMachineReady)
	if condition == nil {
		return fmt.Errorf("vm \"%s/%s\" missing %q condition in cluster %q",
			ctx.AppNamespace(), w.GetAppName(), virtv1.VirtualMachineReady, cluster.Name)
	}

	if condition.Status != corev1.ConditionTrue {
		return fmt.Errorf("vm \"%s/%s\" condition %q is %q in cluster %q: %s",
			ctx.AppNamespace(), w.GetAppName(), virtv1.VirtualMachineReady,
			condition.Status, cluster.Name, condition.Message)
	}

	return nil
}

// Status returns the VM workload deployment status across managed clusters.
func (w *VM) Status(ctx types.TestContext) ([]types.WorkloadStatus, error) {
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

func (w *VM) statusForCluster(ctx types.TestContext, cluster *types.Cluster) (types.WorkloadStatus, error) {
	vmExist, err := findVM(ctx, cluster, ctx.AppNamespace(), w.GetAppName())
	if err != nil {
		return types.WorkloadStatus{}, err
	}

	pvcExist, err := findPVC(ctx, cluster, ctx.AppNamespace(), vmPVCName)
	if err != nil {
		return types.WorkloadStatus{}, err
	}

	var status types.ApplicationStatus

	switch {
	case vmExist && pvcExist:
		status = types.ApplicationFound
	case vmExist || pvcExist:
		status = types.ApplicationPartial
	default:
		status = types.ApplicationNotFound
	}

	return types.WorkloadStatus{ClusterName: cluster.Name, Status: status}, nil
}

func getVM(ctx types.TestContext, cluster *types.Cluster,
	namespace, name string,
) (*virtv1.VirtualMachine, error) {
	vm := &virtv1.VirtualMachine{}
	key := k8stypes.NamespacedName{Name: name, Namespace: namespace}

	err := cluster.Client.Get(ctx.Context(), key, vm)
	if err != nil {
		return nil, err
	}

	return vm, nil
}

func findVM(ctx types.TestContext, cluster *types.Cluster, namespace, name string) (bool, error) {
	_, err := getVM(ctx, cluster, namespace, name)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return false, err
		}

		return false, nil
	}

	return true, nil
}

func findVMCondition(
	conditions []virtv1.VirtualMachineCondition,
	conditionType virtv1.VirtualMachineConditionType,
) *virtv1.VirtualMachineCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}

	return nil
}

func init() {
	register(vmName, NewVM)
}
