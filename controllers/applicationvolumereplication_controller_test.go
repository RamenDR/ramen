/*
Copyright 2021 The RamenDR authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers_test

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ghodss/yaml"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spokeClusterV1 "github.com/open-cluster-management/api/cluster/v1"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	plrv1 "github.com/open-cluster-management/multicloud-operators-placementrule/pkg/apis/apps/v1"
	subv1 "github.com/open-cluster-management/multicloud-operators-subscription/pkg/apis/apps/v1"
	rmn "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers"
	fndv2 "github.com/tjanssen3/multicloud-operators-foundation/v2/pkg/apis/view/v1beta1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

const (
	ApplicationVolumeReplicationName          = "app-volume-replication-test"
	ApplicationVolumeReplicationNamespaceName = "app-namespace"
	EastManagedCluster                        = "east-cluster"
	WestManagedCluster                        = "west-cluster"

	timeout  = time.Second * 10
	interval = time.Millisecond * 250
)

var (
	westCluster = &spokeClusterV1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: WestManagedCluster,
			Labels: map[string]string{
				"name": WestManagedCluster,
				"key1": "value1",
			},
		},
	}
	eastCluster = &spokeClusterV1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: EastManagedCluster,
			Labels: map[string]string{
				"name": EastManagedCluster,
				"key1": "value1",
			},
		},
	}

	clusters = []*spokeClusterV1.ManagedCluster{westCluster, eastCluster}

	eastManagedClusterNamespace = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: EastManagedCluster},
	}

	westManagedClusterNamespace = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: WestManagedCluster},
	}

	appNamespace = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ApplicationVolumeReplicationNamespaceName},
	}
)

var safeToProceed bool

// FakeProgressCallback of function type
func FakeProgressCallback(avrName string, done bool) {
	safeToProceed = done
}

type FakePVDownloader struct{}

func (s FakePVDownloader) DownloadPVs(ctx context.Context, r client.Reader,
	objStoreGetter controllers.ObjectStoreGetter, s3Endpoint, s3Region string,
	s3SecretName types.NamespacedName, callerTag string,
	s3Bucket string) ([]corev1.PersistentVolume, error) {
	pv1 := corev1.PersistentVolume{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolume",
			APIVersion: "ramendr.openshift.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "pv0001",
		},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					VolumeHandle: "vol-id-1",
				},
			},
			ClaimRef: &corev1.ObjectReference{
				Name: "claim1",
			},
			StorageClassName: "sc-name",
		},
	}

	pv2 := corev1.PersistentVolume{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolume",
			APIVersion: "ramendr.openshift.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "pv0002",
		},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					VolumeHandle: "vol-id-1",
				},
			},
			ClaimRef: &corev1.ObjectReference{
				Name: "claim2",
			},
			StorageClassName: "sc-name",
		},
	}

	pvList := []corev1.PersistentVolume{}
	pvList = append(pvList, pv1, pv2)

	return pvList, nil
}

func createSubscription(name, namespace, pause string) *subv1.Subscription {
	subscription := &subv1.Subscription{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app":                        "myApp",
				"ramendr":                    "protected",
				subv1.LabelSubscriptionPause: pause,
			},
		},
		Spec: subv1.SubscriptionSpec{
			Channel: "test/test-github-channel",
			Placement: &plrv1.Placement{
				PlacementRef: &corev1.ObjectReference{
					Name: "sub-placement-rule",
					Kind: "PlacementRule",
				},
			},
		},
	}

	err := k8sClient.Create(context.TODO(), subscription)
	Expect(err).NotTo(HaveOccurred())

	return subscription
}

func updateSubscriptionStatus(subscription *subv1.Subscription, targetManagedCluster string) {
	subStatus := subv1.SubscriptionStatus{
		Phase:          "Propagated",
		Reason:         "",
		LastUpdateTime: metav1.Now(),
		Statuses: subv1.SubscriptionClusterStatusMap{
			targetManagedCluster: &subv1.SubscriptionPerClusterStatus{
				SubscriptionPackageStatus: map[string]*subv1.SubscriptionUnitStatus{
					"packages": {
						Phase: subv1.SubscriptionSubscribed,
					},
				},
			},
		},
	}

	latestSub := getLatestSubscription(subscription.Name, subscription.Namespace)
	latestSub.Status = subStatus

	err := k8sClient.Status().Update(context.TODO(), latestSub)
	if err != nil {
		latestSub = getLatestSubscription(subscription.Name, subscription.Namespace)
		latestSub.Status = subStatus
		err = k8sClient.Status().Update(context.TODO(), latestSub)
	}

	Expect(err).NotTo(HaveOccurred())

	subLookupKey := types.NamespacedName{Name: subscription.Name, Namespace: subscription.Namespace}
	newSubscription := &subv1.Subscription{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), subLookupKey, newSubscription)

		return err == nil && newSubscription.Status.Phase == "Propagated"
	}, timeout, interval).Should(BeTrue(), "failed to update subscription status")
}

func getLatestSubscription(name, namespace string) *subv1.Subscription {
	subLookupKey := types.NamespacedName{Name: name, Namespace: namespace}
	latestSub := &subv1.Subscription{}
	err := k8sClient.Get(context.TODO(), subLookupKey, latestSub)
	Expect(err).NotTo(HaveOccurred())

	return latestSub
}

func pauseSubscription(subscription *subv1.Subscription) {
	subLookupKey := types.NamespacedName{Name: subscription.Name, Namespace: subscription.Namespace}
	newSubscription := &subv1.Subscription{}
	err := k8sClient.Get(context.TODO(), subLookupKey, newSubscription)
	Expect(err).NotTo(HaveOccurred())

	labels := subscription.GetLabels()
	Expect(labels).ToNot(BeNil())

	labels[subv1.LabelSubscriptionPause] = "true"
	newSubscription.SetLabels(labels)

	err = k8sClient.Update(context.TODO(), newSubscription)
	Expect(err).NotTo(HaveOccurred())

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), subLookupKey, newSubscription)

		return err == nil && newSubscription.GetLabels()[subv1.LabelSubscriptionPause] == "true"
	}, timeout, interval).Should(BeTrue(), "failed to update subscription label to 'Pause'")
}

func createManagedClusterView(name, namespace string) *fndv2.ManagedClusterView {
	mcv := &fndv2.ManagedClusterView{

		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: fndv2.ViewSpec{
			Scope: fndv2.ViewScope{
				// intentionally blank
			},
		},
	}

	err := k8sClient.Create(context.TODO(), mcv)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			err = k8sClient.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, mcv)
		}
	}

	Expect(err).NotTo(HaveOccurred())

	return mcv
}

// create a VRG, then fake ManagedClusterView results
func updateManagedClusterViewWithVRG(mcv *fndv2.ManagedClusterView, status metav1.ConditionStatus) {
	vrg := &rmn.VolumeReplicationGroup{
		TypeMeta:   metav1.TypeMeta{Kind: "VolumeReplicationGroup", APIVersion: "ramendr.openshift.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "test-vrg", Namespace: "test"},
		Spec: rmn.VolumeReplicationGroupSpec{
			VolumeReplicationClass: "volume-rep-class",
			ReplicationState:       "Primary",
			PVCSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"appclass":    "gold",
					"environment": "dev.AZ1",
				},
			},
			S3Endpoint:   "path/to/s3Endpoint",
			S3SecretName: "SecretName",
		},
	}

	updateManagedClusterView(mcv, vrg, status)
}

// take an existing ManagedClusterView and apply the given resource to it as though it were "found"
func updateManagedClusterView(mcv *fndv2.ManagedClusterView, resource interface{}, status metav1.ConditionStatus) {
	// get raw bytes
	objJSON, err := json.Marshal(resource)

	Expect(err).NotTo(HaveOccurred())

	// update Status, Result fields
	reason := fndv2.ReasonGetResource
	if status != metav1.ConditionTrue {
		reason = fndv2.ReasonGetResourceFailed
	}

	mcv.Status = fndv2.ViewStatus{
		Conditions: []metav1.Condition{
			{
				Type:               fndv2.ConditionViewProcessing,
				LastTransitionTime: metav1.Time{Time: time.Now().Local()},
				Status:             status,
				Reason:             reason,
			},
		},
		Result: runtime.RawExtension{
			Raw: objJSON,
		},
	}

	err = k8sClient.Status().Update(context.TODO(), mcv)

	Expect(err).NotTo(HaveOccurred())
}

func createPlacementRule(name, namespace string) *plrv1.PlacementRule {
	namereq := metav1.LabelSelectorRequirement{}
	namereq.Key = "key1"
	namereq.Operator = metav1.LabelSelectorOpIn

	namereq.Values = []string{"value1"}
	labelSelector := &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{namereq},
	}

	placementRule := &plrv1.PlacementRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: plrv1.PlacementRuleSpec{
			GenericPlacementFields: plrv1.GenericPlacementFields{
				ClusterSelector: labelSelector,
			},
		},
	}

	err := k8sClient.Create(context.TODO(), placementRule)
	Expect(err).NotTo(HaveOccurred())

	return placementRule
}

func updatePlacementRuleStatus(placementRule *plrv1.PlacementRule, clusterName string) {
	decision := plrv1.PlacementDecision{
		ClusterName:      clusterName,
		ClusterNamespace: "test",
	}

	plDecisions := []plrv1.PlacementDecision{decision}
	placementRule.Status = plrv1.PlacementRuleStatus{
		Decisions: plDecisions,
	}

	err := k8sClient.Status().Update(context.TODO(), placementRule)
	Expect(err).NotTo(HaveOccurred())
}

func createAVR(name, namespace string) *rmn.ApplicationVolumeReplication {
	avr := &rmn.ApplicationVolumeReplication{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: rmn.ApplicationVolumeReplicationSpec{
			SubscriptionSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "myApp",
				},
			},
			PVCSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"appclass":    "gold",
					"environment": "dev.AZ1",
				},
			},
			S3Endpoint:   "path/to/s3Endpoint",
			S3SecretName: "SecretName",
		},
	}
	Expect(k8sClient.Create(context.TODO(), avr)).Should(Succeed())

	return avr
}

func setAVRSpecExpectationTo(avr *rmn.ApplicationVolumeReplication,
	subscriptionName, s3Endpoint string, action rmn.Action) {
	drEnabledSubscriptions := rmn.DREnabledSubscriptionsMap{
		subscriptionName: action,
	}

	latestAVR := getLatestAVR(avr.Name, avr.Namespace)
	if s3Endpoint != "" {
		latestAVR.Spec.S3Endpoint = s3Endpoint
	}

	latestAVR.Spec.DREnabledSubscriptions = drEnabledSubscriptions
	err := k8sClient.Update(context.TODO(), latestAVR)
	Expect(err).NotTo(HaveOccurred())

	Eventually(func() bool {
		latestAVR := getLatestAVR(avr.Name, avr.Namespace)
		if val, found := latestAVR.Spec.DREnabledSubscriptions[subscriptionName]; found {
			return action == val
		}

		return false
	}, timeout, interval).Should(BeTrue(), "failed to update AVR DR action on time")
}

func getLatestAVR(name, namespace string) *rmn.ApplicationVolumeReplication {
	avrLookupKey := types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}
	latestAVR := &rmn.ApplicationVolumeReplication{}
	err := k8sClient.Get(context.TODO(), avrLookupKey, latestAVR)
	Expect(err).NotTo(HaveOccurred())

	return latestAVR
}

func createNamespaces() {
	eastNamespaceLookupKey := types.NamespacedName{Name: eastManagedClusterNamespace.Name}
	eastNamespaceObj := &corev1.Namespace{}

	err := k8sClient.Get(context.TODO(), eastNamespaceLookupKey, eastNamespaceObj)
	if err != nil {
		Expect(k8sClient.Create(context.TODO(), eastManagedClusterNamespace)).NotTo(HaveOccurred(),
			"failed to create east managed cluster namespace")
	}

	westNamespaceLookupKey := types.NamespacedName{Name: westManagedClusterNamespace.Name}
	westNamespaceObj := &corev1.Namespace{}

	err = k8sClient.Get(context.TODO(), westNamespaceLookupKey, westNamespaceObj)
	if err != nil {
		Expect(k8sClient.Create(context.TODO(), westManagedClusterNamespace)).NotTo(HaveOccurred(),
			"failed to create west managed cluster namespace")
	}

	appNamespaceLookupKey := types.NamespacedName{Name: appNamespace.Name}
	appNamespaceObj := &corev1.Namespace{}

	err = k8sClient.Get(context.TODO(), appNamespaceLookupKey, appNamespaceObj)
	if err != nil {
		Expect(k8sClient.Create(context.TODO(), appNamespace)).NotTo(HaveOccurred(), "failed to create app namespace")
	}
}

func createManagedClusters() {
	for _, cl := range clusters {
		mcLookupKey := types.NamespacedName{Name: cl.Name}
		mcObj := &spokeClusterV1.ManagedCluster{}

		err := k8sClient.Get(context.TODO(), mcLookupKey, mcObj)
		if err != nil {
			clinstance := cl.DeepCopy()

			err := k8sClient.Create(context.TODO(), clinstance)
			Expect(err).NotTo(HaveOccurred())
		}
	}
}

func updateManifestWorkStatus(name, namespace, clusterNamespace, mwType string) {
	manifestLookupKey := types.NamespacedName{
		Name:      controllers.BuildManifestWorkName(name, namespace, mwType),
		Namespace: clusterNamespace,
	}
	createdManifest := &ocmworkv1.ManifestWork{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), manifestLookupKey, createdManifest)

		return err == nil
	}, timeout, interval).Should(BeTrue(), "failed to wait for manifest creation")

	timeOld := time.Now().Local()
	timeMostRecent := timeOld.Add(time.Second)
	pvManifestStatus := ocmworkv1.ManifestWorkStatus{
		Conditions: []metav1.Condition{
			{
				Type:               ocmworkv1.WorkApplied,
				LastTransitionTime: metav1.Time{Time: timeMostRecent},
				Status:             metav1.ConditionTrue,
				Reason:             "test",
			},
		},
	}
	createdManifest.Status = pvManifestStatus

	err := k8sClient.Status().Update(context.TODO(), createdManifest)
	Expect(err).NotTo(HaveOccurred())

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), manifestLookupKey, createdManifest)

		return err == nil && len(createdManifest.Status.Conditions) != 0
	}, timeout, interval).Should(BeTrue(), "failed to wait for PV manifest condition type to change to 'Applied'")
}

func InitialDeployment(subscriptionName, placementName, homeCluster string) (*subv1.Subscription,
	*plrv1.PlacementRule, *rmn.ApplicationVolumeReplication) {
	createNamespaces()

	subscription := createSubscription(subscriptionName, "app-namespace", "False")
	updateSubscriptionStatus(subscription, homeCluster)

	subLookupKey := types.NamespacedName{Name: subscription.Name, Namespace: subscription.Namespace}
	createdSubscription := &subv1.Subscription{}
	err := k8sClient.Get(context.TODO(), subLookupKey, createdSubscription)
	Expect(err).NotTo(HaveOccurred())
	Expect(createdSubscription.Status.Phase).Should(Equal(subv1.SubscriptionPhase("Propagated")))

	createManagedClusters()

	placementRule := createPlacementRule(placementName, subscription.Namespace)

	avr := createAVR(ApplicationVolumeReplicationName, ApplicationVolumeReplicationNamespaceName)

	return subscription, placementRule, avr
}

func verifyVRGManifestWorkCreatedAsExpected(subscription *subv1.Subscription, managedCluster string) {
	// 4.0 Get the VRG Roles ManifestWork. The work is created per managed cluster in the AVR reconciler
	vrgManifestLookupKey := types.NamespacedName{
		Name:      "ramendr-vrg-roles",
		Namespace: managedCluster,
	}
	createdVRGRolesManifest := &ocmworkv1.ManifestWork{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), vrgManifestLookupKey, createdVRGRolesManifest)

		return err == nil
	}, timeout, interval).Should(BeTrue())

	Expect(len(createdVRGRolesManifest.Spec.Workload.Manifests)).To(Equal(2))

	vrgClusterRoleManifest := createdVRGRolesManifest.Spec.Workload.Manifests[0]
	Expect(vrgClusterRoleManifest).ToNot(BeNil())

	vrgClusterRole := &rbacv1.ClusterRole{}
	err := yaml.Unmarshal(vrgClusterRoleManifest.RawExtension.Raw, &vrgClusterRole)
	Expect(err).NotTo(HaveOccurred())

	vrgClusterRoleBindingManifest := createdVRGRolesManifest.Spec.Workload.Manifests[1]
	Expect(vrgClusterRoleBindingManifest).ToNot(BeNil())

	vrgClusterRoleBinding := &rbacv1.ClusterRoleBinding{}

	err = yaml.Unmarshal(vrgClusterRoleManifest.RawExtension.Raw, &vrgClusterRoleBinding)
	Expect(err).NotTo(HaveOccurred())

	manifestLookupKey := types.NamespacedName{
		Name:      controllers.BuildManifestWorkName(subscription.Name, subscription.Namespace, "vrg"),
		Namespace: managedCluster,
	}
	createdManifest := &ocmworkv1.ManifestWork{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), manifestLookupKey, createdManifest)

		return err == nil
	}, timeout, interval).Should(BeTrue())

	// 5.1 verify that VRG CR has been created and added to the ManifestWork
	Expect(len(createdManifest.Spec.Workload.Manifests)).To(Equal(1))
	vrgClientManifest := createdManifest.Spec.Workload.Manifests[0]

	Expect(vrgClientManifest).ToNot(BeNil())

	vrg := &rmn.VolumeReplicationGroup{}

	err = yaml.Unmarshal(vrgClientManifest.RawExtension.Raw, &vrg)
	Expect(err).NotTo(HaveOccurred())
	Expect(vrg.Name).Should(Equal(subscription.Name))
	Expect(vrg.Spec.PVCSelector.MatchLabels["appclass"]).Should(Equal("gold"))
}

func getManifestWorkCount(homeClusterNamespace string) int {
	manifestWorkList := &ocmworkv1.ManifestWorkList{}
	listOptions := &client.ListOptions{Namespace: homeClusterNamespace}

	Expect(k8sClient.List(context.TODO(), manifestWorkList, listOptions)).NotTo(HaveOccurred())

	return len(manifestWorkList.Items)
}

func verifyAVRStatusExpectation(subscription *subv1.Subscription, homeCluster, peerCluster, prevHomeCluster string,
	drState rmn.DRState) {
	avrLookupKey := types.NamespacedName{
		Name:      ApplicationVolumeReplicationName,
		Namespace: ApplicationVolumeReplicationNamespaceName,
	}

	updatedAVR := &rmn.ApplicationVolumeReplication{}

	Eventually(func() bool {
		err := k8sClient.Get(context.TODO(), avrLookupKey, updatedAVR)

		if d, found := updatedAVR.Status.Decisions[subscription.Name]; err == nil && found {
			return d.HomeCluster == homeCluster
		}

		return false
	}, timeout, interval).Should(BeTrue())

	// 7.0 check that the home and peer clusters have been selected.
	Expect(updatedAVR.Status.Decisions[subscription.Name].HomeCluster).Should(Equal(homeCluster))
	Expect(updatedAVR.Status.Decisions[subscription.Name].PeerCluster).Should(Equal(peerCluster))
	Expect(updatedAVR.Status.Decisions[subscription.Name].PrevHomeCluster).Should(Equal(prevHomeCluster))
	Expect(updatedAVR.Status.LastKnownDRStates[subscription.Name]).Should(Equal(drState))
}

func expectSubscriptionIsPaused(subscription *subv1.Subscription) {
	subLookupKey := types.NamespacedName{Name: subscription.Name, Namespace: subscription.Namespace}
	updatedSubscription := &subv1.Subscription{}
	err := k8sClient.Get(context.TODO(), subLookupKey, updatedSubscription)
	Expect(err).NotTo(HaveOccurred())
	Expect(updatedSubscription.GetLabels()).ToNot(BeNil())
	Expect(updatedSubscription.GetLabels()[subv1.LabelSubscriptionPause]).Should(Equal("true"))
}

func waitForCompletion() {
	Eventually(func() bool {
		return safeToProceed
	}, timeout*2, interval).Should(BeTrue(), "failed to wait for hook to be called")
}

// +kubebuilder:docs-gen:collapse=Imports

var _ = Describe("ApplicationVolumeReplication Reconciler", func() {
	Context("ApplicationVolumeReplication Reconciler", func() {
		subscription := &subv1.Subscription{}
		placementRule := &plrv1.PlacementRule{}
		avr := &rmn.ApplicationVolumeReplication{}

		When("Subscription is deployed for the first time", func() {
			It("Should deploy subscription to EastManagedCluster", func() {
				By("Initial Deployment")
				safeToProceed = false
				subscription, placementRule, avr = InitialDeployment("subscription-4", "sub-placement-rule", EastManagedCluster)
				verifyVRGManifestWorkCreatedAsExpected(subscription, EastManagedCluster)
				updateManifestWorkStatus(subscription.Name, subscription.Namespace, EastManagedCluster, "vrg")
				verifyAVRStatusExpectation(subscription, EastManagedCluster, WestManagedCluster, "", rmn.Initial)
				waitForCompletion()
			})
		})
		When("Subscription is paused for failover", func() {
			It("Should failover to WestManagedCluster", func() {
				// ----------------------------- FAILOVER --------------------------------------
				By("\n\n*** Failover - 1\n\n")
				safeToProceed = false

				mcv := createManagedClusterView("mcv-avr-reconciler", WestManagedCluster)
				updateManagedClusterViewWithVRG(mcv, metav1.ConditionTrue)

				pauseSubscription(subscription)
				expectSubscriptionIsPaused(subscription)
				updatePlacementRuleStatus(placementRule, WestManagedCluster)
				updateSubscriptionStatus(subscription, WestManagedCluster)
				setAVRSpecExpectationTo(avr, subscription.Name, "", rmn.ActionFailover)
				updateManifestWorkStatus(subscription.Name, subscription.Namespace, WestManagedCluster, "pv")
				updateManifestWorkStatus(subscription.Name, subscription.Namespace, WestManagedCluster, "vrg")
				verifyAVRStatusExpectation(subscription, WestManagedCluster, EastManagedCluster, EastManagedCluster, rmn.FailedOver)
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(3)) // MW for VRG+ROLES+PV
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(1)) // MW for ROLES
				waitForCompletion()
			})
		})
		When("Subscription is paused for failover for the second time", func() {
			It("Should NOT do anything", func() {
				By("\n\n*** Failover - 2: NOOP\n\n")
				safeToProceed = false
				pauseSubscription(subscription)
				expectSubscriptionIsPaused(subscription)
				updatePlacementRuleStatus(placementRule, WestManagedCluster)
				updateSubscriptionStatus(subscription, WestManagedCluster)
				// Force the reconciler to execute by changing one of the avr.Spec fields. We chose s3Endpoint
				setAVRSpecExpectationTo(avr, subscription.Name, "newS3Endpoint", rmn.ActionFailover)
				verifyAVRStatusExpectation(subscription, WestManagedCluster, EastManagedCluster, EastManagedCluster, rmn.FailedOver)
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(3)) // MW for VRG+ROLES+PV
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(1)) // MW for ROLES
				waitForCompletion()
			})
		})
		When("Subscription is paused for failback", func() {
			It("Should failback to EastManagedCluster", func() {
				// ----------------------------- FAILBACK --------------------------------------
				By("\n\n*** Failback - 1\n\n")
				safeToProceed = false

				mcv := createManagedClusterView("mcv-avr-reconciler", EastManagedCluster)
				updateManagedClusterViewWithVRG(mcv, metav1.ConditionTrue)

				pauseSubscription(subscription)
				expectSubscriptionIsPaused(subscription)
				updatePlacementRuleStatus(placementRule, EastManagedCluster)
				updateSubscriptionStatus(subscription, EastManagedCluster)
				setAVRSpecExpectationTo(avr, subscription.Name, "", rmn.ActionFailback)
				updateManifestWorkStatus(subscription.Name, subscription.Namespace, EastManagedCluster, "pv")
				updateManifestWorkStatus(subscription.Name, subscription.Namespace, EastManagedCluster, "vrg")
				verifyAVRStatusExpectation(subscription, EastManagedCluster, WestManagedCluster, "", rmn.FailedBack)
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(3)) // MW for VRG+ROLES+PV
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(1)) // MW for ROLES
				waitForCompletion()
			})
		})
		When("Subscription is paused for failback for the second time", func() {
			It("Should NOT do anything", func() {
				By("\n\n*** Failback - 2: NOOP\n\n")
				safeToProceed = false
				pauseSubscription(subscription)
				expectSubscriptionIsPaused(subscription)
				updatePlacementRuleStatus(placementRule, EastManagedCluster)
				updateSubscriptionStatus(subscription, EastManagedCluster)
				// Force the reconciler to execute by changing one of the avr.Spec fields. It is easier to change s3Endpoint
				setAVRSpecExpectationTo(avr, subscription.Name, "path/to/s3Endpoint", rmn.ActionFailback)
				verifyAVRStatusExpectation(subscription, EastManagedCluster, WestManagedCluster, "", rmn.FailedBack)
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(3)) // MW for VRG+ROLES+PV
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(1)) // MW for ROLES
				waitForCompletion()
			})
		})
		When("Subscription is paused for failover after failback", func() {
			It("Should failover to the WestManagedCluster", func() {
				// ----------------------------- FAILOVER --------------------------------------
				By("\n\n*** Failover - 3\n\n")
				safeToProceed = false
				pauseSubscription(subscription)
				expectSubscriptionIsPaused(subscription)
				updatePlacementRuleStatus(placementRule, WestManagedCluster)
				updateSubscriptionStatus(subscription, WestManagedCluster)
				setAVRSpecExpectationTo(avr, subscription.Name, "", rmn.ActionFailover)
				updateManifestWorkStatus(subscription.Name, subscription.Namespace, WestManagedCluster, "pv")
				updateManifestWorkStatus(subscription.Name, subscription.Namespace, WestManagedCluster, "vrg")
				verifyAVRStatusExpectation(subscription, WestManagedCluster, EastManagedCluster, EastManagedCluster, rmn.FailedOver)
				Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(3)) // MW for VRG+ROLES+PV
				Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(1)) // MW for ROLES
				waitForCompletion()
			})
		})
		It("Should not unpause without valid ManagedClusterView", func() {
			// ----------------------------- FAILOVER --------------------------------------
			By("\n\n*** Failover - 1\n\n")
			safeToProceed = false

			mcv := createManagedClusterView("mcv-avr-reconciler", WestManagedCluster)
			updateManagedClusterViewWithVRG(mcv, metav1.ConditionFalse)

			pauseSubscription(subscription)
			expectSubscriptionIsPaused(subscription)
			updatePlacementRuleStatus(placementRule, WestManagedCluster)
			updateSubscriptionStatus(subscription, WestManagedCluster)
			setAVRSpecExpectationTo(avr, subscription.Name, "", rmn.ActionFailover)
			updateManifestWorkStatus(subscription.Name, subscription.Namespace, WestManagedCluster, "pv")
			updateManifestWorkStatus(subscription.Name, subscription.Namespace, WestManagedCluster, "vrg")
			verifyAVRStatusExpectation(subscription, WestManagedCluster, EastManagedCluster, EastManagedCluster, rmn.FailedOver)
			Expect(getManifestWorkCount(WestManagedCluster)).Should(Equal(3)) // MW for VRG+ROLES+PV
			Expect(getManifestWorkCount(EastManagedCluster)).Should(Equal(1)) // MW for ROLES

			// should still be paused because a valid ManagedClusterView hasn't been found
			expectSubscriptionIsPaused(subscription)
		})
	})
	Context("IsManifestInAppliedState checks ManifestWork with single timestamp", func() {
		timeOld := time.Now().Local()
		timeMostRecent := timeOld.Add(time.Second)

		It("'Applied' present, Status False", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkApplied,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
							Status:             metav1.ConditionFalse,
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(false))
		})

		It("'Applied' present, Status true", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkApplied,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
							Status:             metav1.ConditionTrue,
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(true))
		})

		It("'Applied' not present", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkProgressing,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(false))
		})
	})

	Context("IsManifestInAppliedState checks ManifestWork with multiple timestamps", func() {
		timeOld := time.Now().Local()
		timeMostRecent := timeOld.Add(time.Second)

		It("no duplicate timestamps, 'Applied' is most recent", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkApplied,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
							Status:             metav1.ConditionTrue,
						},
						{
							Type:               ocmworkv1.WorkProgressing,
							LastTransitionTime: metav1.Time{Time: timeOld},
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(true))
		})

		It("no duplicates timestamps, 'Applied' is not most recent", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkProgressing,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
						{
							Type:               ocmworkv1.WorkApplied,
							LastTransitionTime: metav1.Time{Time: timeOld},
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(false))
		})

		It("with duplicate timestamps, 'Applied' is most recent", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkApplied,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
							Status:             metav1.ConditionTrue,
						},
						{
							Type:               ocmworkv1.WorkProgressing,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
							Status:             metav1.ConditionUnknown,
						},
						{
							Type:               ocmworkv1.WorkProgressing,
							LastTransitionTime: metav1.Time{Time: timeOld},
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(true))
		})

		It("with duplicate timestamps, 'Applied' is not most recent", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkAvailable,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
						{
							Type:               ocmworkv1.WorkProgressing,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
						{
							Type:               ocmworkv1.WorkApplied,
							LastTransitionTime: metav1.Time{Time: timeOld},
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(false))
		})

		It("duplicate timestamps with Degraded and Applied status", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkApplied,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
						{
							Type:               ocmworkv1.WorkDegraded,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
						},
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(false))
		})
	})

	Context("IsManifestInAppliedState checks ManifestWork with no timestamps", func() {
		It("manifest missing conditions", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						// empty
					},
				},
			}

			Expect(controllers.IsManifestInAppliedState(mw)).To(Equal(false))
		})
	})
})
