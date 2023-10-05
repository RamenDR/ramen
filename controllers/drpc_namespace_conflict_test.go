// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"

	rmn "github.com/ramendr/ramen/api/v1alpha1"
	plrv1 "github.com/stolostron/multicloud-operators-placementrule/pkg/apis/apps/v1"
)

var _ = Describe("DRPlacementControl Reconciler New", func() {
	Specify("DRClusters", func() {
		populateDRClusters()
	})
	Context("DRPlacementControl Reconciler Async DR using PlacementRule (Subscription)", func() {
		var userPlacementRule *plrv1.PlacementRule
		var drpc *rmn.DRPlacementControl
		When("An Application is deployed for the first time", func() {
			It("Should deploy to East1ManagedCluster", func() {
				By("Initial Deployment")
				var placementObj client.Object
				placementObj, drpc = InitialDeploymentAsync(
					DRPCNamespaceName, UserPlacementRuleName, East1ManagedCluster, UsePlacementRule)
				testLogger.Info("dprc rtalur logging", "drpc", drpc)
				userPlacementRule = placementObj.(*plrv1.PlacementRule)
				Expect(userPlacementRule).NotTo(BeNil())
				verifyInitialDRPCDeployment(userPlacementRule, East1ManagedCluster)
				verifyDRPCOwnedByPlacement(userPlacementRule, getLatestDRPC())
				drpc2 := createDRPC(drpc.Spec.PlacementRef.Name, "overlappingdrpc", drpc.Namespace, drpc.Spec.DRPolicyRef.Name)
				testLogger.Info("dprc rtalur logging", "drpc", drpc2)
				drpc2, err := getLatestDRPCByNamespacedName(types.NamespacedName{Namespace: drpc2.Namespace, Name: drpc2.Name})
				Expect(err).NotTo(HaveOccurred())
				testLogger.Info("dprc rtalur logging", "drpc", drpc2)
			})
		})
		//When("Deleting user PlacementRule", func() {
		//	It("Should cleanup DRPC", func() {
		//		// ----------------------------- DELETE DRPC from PRIMARY --------------------------------------
		//		By("\n\n*** DELETE User PlacementRule ***\n\n")
		//		deleteUserPlacementRule()
		//	})
		//})

		//When("Deleting DRPC", func() {
		//	It("Should delete VRG and NS MWs and MCVs from Primary (East1ManagedCluster)", func() {
		//		// ----------------------------- DELETE DRPC from PRIMARY --------------------------------------
		//		By("\n\n*** DELETE DRPC ***\n\n")
		//		Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(3)) // DRCluster + VRG MW
		//		deleteDRPC()
		//		waitForCompletion("deleted")
		//		Expect(getManifestWorkCount(East1ManagedCluster)).Should(Equal(2))       // DRCluster + NS MW only
		//		Expect(getManagedClusterViewCount(East1ManagedCluster)).Should(Equal(0)) // NS + VRG MCV
		//		deleteNamespaceMWsFromAllClusters(DRPCNamespaceName)
		//	})
		//})
	})
})
