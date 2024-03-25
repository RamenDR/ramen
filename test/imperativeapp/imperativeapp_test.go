package imperativeapp_test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	volsyncv1alpha1 "github.com/backube/volsync/api/v1alpha1"
	volrep "github.com/csi-addons/kubernetes-csi-addons/apis/replication.storage/v1alpha1"
	snapv1 "github.com/kubernetes-csi/external-snapshotter/client/v4/apis/volumesnapshot/v1"
	ocmclv1 "github.com/open-cluster-management/api/cluster/v1"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	viewv1beta1 "github.com/stolostron/multicloud-operators-foundation/pkg/apis/view/v1beta1"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	cpcv1 "open-cluster-management.io/config-policy-controller/api/v1"
	gppv1 "open-cluster-management.io/governance-policy-propagator/api/v1"

	clrapiv1beta1 "github.com/open-cluster-management-io/api/cluster/v1beta1"
	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
	argocdv1alpha1hack "github.com/ramendr/ramen/controllers/argocd"
	Recipe "github.com/ramendr/recipe/api/v1alpha1"
	velero "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"

	utiljson "k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/yaml"
)

const (
	ramenOperatorNamespace = "ramen-system"
	ramenOperandsNamespace = "ramen-ops"
)

var (
	kubeconfigHub string
	kubeconfigDR1 string
	kubeconfigDR2 string
)

func init() {
	flag.StringVar(&kubeconfigHub, "kubeconfig-hub", "", "Path to the kubeconfig file for the hub cluster")
	flag.StringVar(&kubeconfigDR1, "kubeconfig-dr1", "", "Path to the kubeconfig file for the dr1 cluster")
	flag.StringVar(&kubeconfigDR2, "kubeconfig-dr2", "", "Path to the kubeconfig file for the dr2 cluster")
	utilruntime.Must(ocmworkv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(ocmclv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(plrv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(viewv1beta1.AddToScheme(scheme.Scheme))
	utilruntime.Must(cpcv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(gppv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(ramendrv1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(Recipe.AddToScheme(scheme.Scheme))
	utilruntime.Must(volrep.AddToScheme(scheme.Scheme))
	utilruntime.Must(volsyncv1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(snapv1.AddToScheme(scheme.Scheme))
	utilruntime.Must(velero.AddToScheme(scheme.Scheme))
	utilruntime.Must(clrapiv1beta1.AddToScheme(scheme.Scheme))
	utilruntime.Must(argocdv1alpha1hack.AddToScheme(scheme.Scheme))
}

type Cluster struct {
	k8sclient client.Client
}
type Clusters map[string]*Cluster

var clusters Clusters

func setupClient(kubeconfigPath string) (client.Client, error) {
	var cfg *rest.Config

	var err error

	if kubeconfigPath == "" {
		return nil, fmt.Errorf("failed to create client: kubeconfigPath is empty")
	}

	kubeconfigPath, err = filepath.Abs(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path of kubeconfig: %w", err)
	}

	cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build config from kubeconfig path (%s): %w", kubeconfigPath, err)
	}

	c, err := client.New(cfg, client.Options{})
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	return c, nil
}

func setupClusters() Clusters {
	hubClient, err := setupClient(kubeconfigHub)
	if err != nil {
		panic(fmt.Sprintf("failed to create client for hub cluster: %v", err))
	}

	dr1Client, err := setupClient(kubeconfigDR1)
	if err != nil {
		panic(fmt.Sprintf("failed to create client for dr1 cluster: %v", err))
	}

	dr2Client, err := setupClient(kubeconfigDR2)
	if err != nil {
		panic(fmt.Sprintf("failed to create client for dr2 cluster: %v", err))
	}

	hubCluster := &Cluster{k8sclient: hubClient}
	dr1Cluster := &Cluster{k8sclient: dr1Client}
	dr2Cluster := &Cluster{k8sclient: dr2Client}

	clusters := Clusters{
		"hub": hubCluster,
		"dr1": dr1Cluster,
		"dr2": dr2Cluster,
	}

	return clusters
}

func GetHubCluster(allClusters Clusters) *Cluster {
	for clusterName, cluster := range allClusters {
		if strings.Contains(clusterName, "hub") {
			return cluster
		}
	}

	return nil
}

func GetManagedClusters(allClusters Clusters) Clusters {
	managedClusters := make(Clusters)

	for clusterName, cluster := range allClusters {
		if !strings.Contains(clusterName, "hub") {
			managedClusters[clusterName] = cluster
		}
	}

	return managedClusters
}

func getNamespace(k8sclient client.Client, namespace string) (*corev1.Namespace, error) {
	ns := &corev1.Namespace{}

	err := k8sclient.Get(context.TODO(), client.ObjectKey{Name: namespace}, ns)
	if err != nil {
		return nil, err
	}

	return ns, nil
}

func doesNamespaceExist(k8sclient client.Client, namespace string) (bool, error) {
	ns, err := getNamespace(k8sclient, namespace)
	if err != nil && !errors.IsNotFound(err) {
		return false, err
	}

	return ns != nil, nil
}

// runOnClusters is a util function that runs the given function on all clusters
func runOnClusters(clusters Clusters, f func(cluster *Cluster) (bool, error)) (map[string]bool, map[string]error) {
	results := make(map[string]bool, len(clusters))
	errs := make(map[string]error, len(clusters))

	for clusterName, cluster := range clusters {
		result, err := f(cluster)
		results[clusterName] = result
		errs[clusterName] = err
	}

	return results, errs
}

func checkRamenOperatorNamespaceExists(clusters Clusters) (bool, error) {
	results, errs := runOnClusters(clusters, func(cluster *Cluster) (bool, error) {
		return doesNamespaceExist(cluster.k8sclient, ramenOperatorNamespace)
	})

	for clusterName, result := range results {
		if !result {
			return false, fmt.Errorf("namespace %s does not exist in %s cluster: err: %v", ramenOperatorNamespace,
				clusterName, errs[clusterName])
		}
	}

	return true, nil
}

func checkRamenOperandsNamespaceExists(clusters Clusters) (bool, error) {
	results, errs := runOnClusters(clusters, func(cluster *Cluster) (bool, error) {
		return doesNamespaceExist(cluster.k8sclient, ramenOperandsNamespace)
	})

	for clusterName, result := range results {
		if !result {
			return false, fmt.Errorf("namespace %s does not exist in %s cluster: err: %v", ramenOperandsNamespace,
				clusterName, errs[clusterName])
		}
	}

	return true, nil
}

func checkPrerequisites() {
	if _, err := checkRamenOperatorNamespaceExists(clusters); err != nil {
		panic(fmt.Sprintf("failed to check prerequisites: %v", err))
	}

	if _, err := checkRamenOperandsNamespaceExists(clusters); err != nil {
		panic(fmt.Sprintf("failed to check prerequisites: %v", err))
	}
}

func waitForDeploymentReady(k8sclient client.Client, deployment *appsv1.Deployment) error {
	deploymentKey := client.ObjectKey{Name: deployment.Name, Namespace: deployment.Namespace}

	err := k8sclient.Get(context.TODO(), deploymentKey, deployment)
	if err != nil {
		return fmt.Errorf("failed to get deployment %s: %v", deployment.Name, err)
	}

	if deployment.Status.ReadyReplicas != *deployment.Spec.Replicas {
		return fmt.Errorf("deployment %s is not ready", deployment.Name)
	}

	return nil
}

func createYAMLForClientObjects(objects ...client.Object) string {
	yamlout := ""

	for _, obj := range objects {

		rb, err := utiljson.Marshal(obj)
		if err != nil {
			panic(err)
		}

		rbyaml, err := yaml.JSONToYAML(rb)
		if err != nil {
			panic(err)
		}

		yamlout += "---\n" + string(rbyaml) + "\n"
	}

	return yamlout
}

func TestMain(m *testing.M) {
	flag.Parse()

	clusters = setupClusters()

	checkPrerequisites()

	os.Exit(m.Run())
}

func TestDeploymentForImperativeSingleNamespaceWithNoRecipe(t *testing.T) {
	workload := NewWorkload("apple").AddBusyBoxDeploymentWithPVC(true, "rook-ceph-block").AddExtraConfigMaps(2, true).
		AddExtraPVCs(2, true, "rook-ceph-block")

	err := workload.Create(clusters["dr1"].k8sclient)
	if err != nil {
		t.Fatalf("failed to create workload: %v", err)
	}

	err = enableDR(clusters["hub"].k8sclient, workload)
	if err != nil {
		t.Fatalf("failed to enable DR: %v", err)
	}

	// err = workload.Delete(clusters["dr1"].k8sclient)
	// if err != nil {
	// 	t.Fatalf("failed to delete workload: %v", err)
	// }
}
