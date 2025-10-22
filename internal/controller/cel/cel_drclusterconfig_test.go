// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package cel_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"

	ramen "github.com/ramendr/ramen/api/v1alpha1"
)

var _ = Describe("DRClusterConfig-CEL", func() {
	drclusterconfigs := []ramen.DRClusterConfig{}
	populateDRClusterConfigs := func() {
		drclusterconfigs = nil
		drclusterconfigs = append(drclusterconfigs,
			ramen.DRClusterConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "dr-cluster-config-0",
				},
				Spec: ramen.DRClusterConfigSpec{
					ReplicationSchedules: []string{"* * * * *"},
					ClusterID:            "test-config-cid-0",
				},
			},
			ramen.DRClusterConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "dr-cluster-config-1",
				},
				Spec: ramen.DRClusterConfigSpec{
					ReplicationSchedules: []string{"* * * * *"},
					ClusterID:            "",
				},
			},
			ramen.DRClusterConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "dr-cluster-config-2",
				},
				Spec: ramen.DRClusterConfigSpec{
					ReplicationSchedules: []string{"* * * * *"},
				},
			},
		)
	}

	createDRClusterConfig := func(drclusterconfig *ramen.DRClusterConfig) {
		Expect(k8sClient.Create(context.TODO(), drclusterconfig)).To(Succeed())
	}

	getDRClusterConfig := func(drclusterconfig *ramen.DRClusterConfig) *ramen.DRClusterConfig {
		drcc := &ramen.DRClusterConfig{}
		err := k8sClient.Get(context.TODO(),
			types.NamespacedName{Name: drclusterconfig.Name},
			drcc)

		Expect(err).NotTo(HaveOccurred())

		return drcc
	}

	deleteDRClusterConfig := func(drclusterconfig *ramen.DRClusterConfig) {
		Expect(k8sClient.Delete(context.TODO(), drclusterconfig)).To(Succeed())
		Eventually(func() bool {
			return k8serrors.IsNotFound(k8sClient.Get(context.TODO(), types.NamespacedName{
				Name: drclusterconfig.Name,
			}, drclusterconfig))
		}, timeout, interval).Should(BeTrue())
	}

	Specify("DRClusterConfig initialize tests", func() {
		populateDRClusterConfigs()
	})

	When("a valid DRClusterConfig is given", func() {
		It("should return success", func() {
			drcc := drclusterconfigs[0].DeepCopy()
			createDRClusterConfig(drcc)
			getDRClusterConfig(drcc)
			deleteDRClusterConfig(drcc)
		})
	})

	When("a valid DRClusterConfig is created", func() {
		It("should not update on modifying cluster ID field", func() {
			drcc := drclusterconfigs[0].DeepCopy()
			createDRClusterConfig(drcc)
			drcc.Spec.ClusterID = "new-cid-0"
			Expect(k8sClient.Update(context.TODO(), drcc)).NotTo(Succeed())
			deleteDRClusterConfig(drcc)
		})
	})

	When("an invalid DRClusterConfig with no cluster ID is created", func() {
		It("should fail to create DRClusterConfig", func() {
			drcc := drclusterconfigs[1].DeepCopy()
			drccSec := drclusterconfigs[2].DeepCopy()

			errDrcc := func() *k8serrors.StatusError {
				path := field.NewPath("spec", "clusterID")

				return k8serrors.NewInvalid(
					schema.GroupKind{
						Group: ramen.GroupVersion.Group,
						Kind:  "DRClusterConfig",
					},
					drcc.Name,
					field.ErrorList{
						field.Required(
							path,
							"",
						),
						field.Invalid(nil, "null",
							"some validation rules were not checked because the object was invalid; "+
								"correct the existing errors to complete validation"),
					},
				)
			}()

			errDrccSec := func() *k8serrors.StatusError {
				path := field.NewPath("spec", "clusterID")

				return k8serrors.NewInvalid(
					schema.GroupKind{
						Group: ramen.GroupVersion.Group,
						Kind:  "DRClusterConfig",
					},
					drccSec.Name,
					field.ErrorList{
						field.Required(
							path,
							"",
						),
						field.Invalid(nil, "null",
							"some validation rules were not checked because the object was invalid; "+
								"correct the existing errors to complete validation"),
					},
				)
			}()
			Expect(k8sClient.Create(context.TODO(), drcc)).To(MatchError(errDrcc))
			Expect(k8sClient.Create(context.TODO(), drccSec)).To(MatchError(errDrccSec))
		})
	})
})
