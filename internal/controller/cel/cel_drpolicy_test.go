package cel_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	// gomegaTypes "github.com/onsi/gomega/types"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	// corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	// validationErrors "k8s.io/kube-openapi/pkg/validation/errors"
	// "sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("DRPolicy-CEL", func() {
	drpolicyCreate := func(drpolicy *ramen.DRPolicy) {
		Expect(k8sClient.Create(context.TODO(), drpolicy)).To(Succeed())
	}

	getDRPolicy := func(drpolicy *ramen.DRPolicy) *ramen.DRPolicy {
		drp := &ramen.DRPolicy{}
		err := k8sClient.Get(context.TODO(),
			types.NamespacedName{Name: drpolicy.Name},
			drp)

		Expect(err).NotTo(HaveOccurred())

		return drp
	}

	dRClusters := []string{
		"dr-cluster-0",
		"dr-cluster-1",
	}

	drpolicies := [...]ramen.DRPolicy{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "drpolicy0"},
			Spec:       ramen.DRPolicySpec{},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "drpolicy1"},
			Spec:       ramen.DRPolicySpec{DRClusters: dRClusters[0:2], SchedulingInterval: `3m`},
		},
	}

	drpolicyDeleteAndConfirm := func(drpolicy *ramen.DRPolicy) {
		Expect(k8sClient.Delete(context.TODO(), drpolicy)).To(Succeed())
		Eventually(func() bool {
			return errors.IsNotFound(k8sClient.Get(context.TODO(), types.NamespacedName{Name: drpolicy.Name}, drpolicy))
		}, timeout, interval).Should(BeTrue())
	}
	drpolicyDelete := func(drpolicy *ramen.DRPolicy) {
		drpolicyDeleteAndConfirm(drpolicy)
	}

	When("a valid DRPolicy is given", func() {
		It("should return success", func() {
			drp := drpolicies[1].DeepCopy()
			drpolicyCreate(drp)
			getDRPolicy(drp)
			drpolicyDelete(drp)
		})
	})

	When("a drpolicy having no drclusters", func() {
		It("should fail to create drpolicy", func() {
			drp := drpolicies[0].DeepCopy()
			drp.Spec.DRClusters = nil
			err := func() *errors.StatusError {
				path := field.NewPath("spec", "drClusters")

				return errors.NewInvalid(
					schema.GroupKind{
						Group: ramen.GroupVersion.Group,
						Kind:  "DRPolicy",
					},
					drp.Name,
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
			Expect(k8sClient.Create(context.TODO(), drp)).To(MatchError(err))
		})
	})

	When("a DRPolicy having empty list in DRClusters", func() {
		It("should not create DRPolicy", func() {
			drp := drpolicies[0].DeepCopy()
			drp.Spec.DRClusters = []string{}
			Expect(k8sClient.Create(context.TODO(), drp)).NotTo(Succeed())
		})
	})

	When("a DRPolicy having only one cluster in DRClusters", func() {
		It("should not create DRPolicy", func() {
			drp := drpolicies[0].DeepCopy()
			drp.Spec.DRClusters = dRClusters[0:1]
			Expect(k8sClient.Create(context.TODO(), drp)).NotTo(Succeed())
		})
	})

	When("a valid DRPolicy is created", func() {
		It("should return with error on modifying DRCluster field", func() {
			drp := drpolicies[1].DeepCopy()
			drpolicyCreate(drp)
			drp.Spec.DRClusters = []string{
				"dr-cluster1-not-exists",
				"dr-cluster2-not-exists",
			}

			Expect(k8sClient.Update(context.TODO(), drp)).NotTo(Succeed())
			drpolicyDelete(drp)
		})
	})

	When("a valid DRPolicy is created", func() {
		It("should not update on modifying schedulingInterval field", func() {
			drp := drpolicies[1].DeepCopy()
			drpolicyCreate(drp)
			drp.Spec.SchedulingInterval = "6m"
			Expect(k8sClient.Update(context.TODO(), drp)).NotTo(Succeed())
			drpolicyDelete(drp)
		})
	})
})
