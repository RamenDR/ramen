package cel_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	// gomegaTypes "github.com/onsi/gomega/types"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	// corev1 "k8s.io/api/core/v1"
	// validationErrors "k8s.io/kube-openapi/pkg/validation/errors"
	// "sigs.k8s.io/controller-runtime/pkg/client"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("DRCluster-CEL", func() {
	s3Profiles := []string{"s3profile1", "s3profile2"}
	drclusters := []ramen.DRCluster{}
	populateDRClusters := func() {
		drclusters = nil
		drclusters = append(drclusters,
			ramen.DRCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "drc-cluster0",
				},
				Spec: ramen.DRClusterSpec{
					S3ProfileName: s3Profiles[0],
					Region:        "east",
				},
			},
			ramen.DRCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "drc-cluster1",
				},
				Spec: ramen.DRClusterSpec{
					S3ProfileName: s3Profiles[1],
					Region:        "east",
				},
			},
		)
	}

	createDRCluster := func(drcluster *ramen.DRCluster) {
		Expect(k8sClient.Create(context.TODO(), drcluster)).To(Succeed())
	}

	getDRCluster := func(drcluster *ramen.DRCluster) *ramen.DRCluster {
		drc := &ramen.DRCluster{}
		err := k8sClient.Get(context.TODO(),
			types.NamespacedName{Name: drcluster.Name},
			drc)

		Expect(err).NotTo(HaveOccurred())

		return drc
	}

	drclusterDelete := func(drcluster *ramen.DRCluster) {
		Expect(k8sClient.Delete(context.TODO(), drcluster)).To(Succeed())
		Eventually(func() bool {
			return errors.IsNotFound(k8sClient.Get(context.TODO(), types.NamespacedName{
				Name: drcluster.Name,
			}, drcluster))
		}, timeout, interval).Should(BeTrue())
	}

	Specify("DRCluster initialize tests", func() {
		populateDRClusters()
	})

	When("a valid DRCluster is given", func() {
		It("should return success", func() {
			drc := drclusters[0].DeepCopy()
			createDRCluster(drc)
			getDRCluster(drc)
			drclusterDelete(drc)
		})
	})

	When("a valid DRCluster is created", func() {
		It("should not update on modifying S3ProfileName field", func() {
			drc := drclusters[0].DeepCopy()
			createDRCluster(drc)
			drc.Spec.S3ProfileName = "new-profile"
			Expect(k8sClient.Update(context.TODO(), drc)).NotTo(Succeed())
			drclusterDelete(drc)
		})
	})

	When("a valid DRCluster is created", func() {
		It("should not update on modifying Region field", func() {
			drc := drclusters[0].DeepCopy()
			createDRCluster(drc)
			drc.Spec.Region = "new-region"
			Expect(k8sClient.Update(context.TODO(), drc)).NotTo(Succeed())
			drclusterDelete(drc)
		})
	})
})
