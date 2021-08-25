package controllers_test

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

var _ = Describe("DrpolicyController", func() {
	clusterNamesCurrent := &sets.String{}
	clusterNames := func(drpolicy *ramen.DRPolicy) sets.String {
		return sets.NewString(util.DrpolicyClusterNames(drpolicy)...)
	}
	clusterRolesExpect := func() {
		Eventually(
			func(g Gomega) {
				clusterNames := sets.String{}
				g.Expect(util.ClusterRolesList(context.TODO(), k8sClient, &clusterNames)).To(Succeed())
				g.Expect(clusterNames.UnsortedList()).To(ConsistOf(clusterNamesCurrent.UnsortedList()))
			},
			10,
			0.25,
		).Should(Succeed())
	}
	drpolicyCreate := func(drpolicy *ramen.DRPolicy) {
		for _, clusterName := range clusterNames(drpolicy).Difference(*clusterNamesCurrent).UnsortedList() {
			Expect(k8sClient.Create(
				context.TODO(),
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: clusterName}},
			)).To(Succeed())
			*clusterNamesCurrent = clusterNamesCurrent.Insert(clusterName)
		}
		Expect(k8sClient.Create(context.TODO(), drpolicy)).To(Succeed())
		clusterRolesExpect()
	}
	drpolicyDelete := func(drpolicy *ramen.DRPolicy, clusterNamesExpected sets.String) {
		Expect(k8sClient.Delete(context.TODO(), drpolicy)).To(Succeed())
		for _, clusterName := range clusterNamesCurrent.Difference(clusterNamesExpected).UnsortedList() {
			Expect(k8sClient.Delete(
				context.TODO(),
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: clusterName}},
			)).To(Succeed())
			*clusterNamesCurrent = clusterNamesCurrent.Delete(clusterName)
		}
		clusterRolesExpect()
	}
	clusters := [...]ramen.ManagedCluster{
		{Name: "cluster-a", S3ProfileName: ""},
		{Name: "cluster-b", S3ProfileName: ""},
		{Name: "cluster-c", S3ProfileName: ""},
	}
	drpolicies := [...]ramen.DRPolicy{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "drpolicy0"},
			Spec:       ramen.DRPolicySpec{DRClusterSet: clusters[0:2]},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "drpolicy1"},
			Spec:       ramen.DRPolicySpec{DRClusterSet: clusters[1:3]},
		},
	}
	clusterNamesNone := sets.String{}
	When("a 1st drpolicy is created", func() {
		It("should create a cluster roles manifest work for each cluster specified in a 1st drpolicy", func() {
			drpolicyCreate(&drpolicies[0])
		})
	})
	When("TODO a 1st drpolicy is updated to add some clusters and remove some other clusters", func() {
		It("should create a cluster roles manifest work for each cluster added and "+
			"delete a cluster roles manifest work for each cluster removed", func() {
		})
	})
	When("a 2nd drpolicy is created specifying some clusters in a 1st drpolicy and some not", func() {
		It("should create a cluster roles manifest work for each cluster specified in a 2nd drpolicy but not a 1st drpolicy",
			func() {
				drpolicyCreate(&drpolicies[1])
			},
		)
	})
	When("a 1st drpolicy is deleted", func() {
		It("should delete a cluster roles manifest work for each cluster specified in a 1st drpolicy but not a 2nd drpolicy",
			func() {
				drpolicyDelete(&drpolicies[0], clusterNames(&drpolicies[1]))
			},
		)
	})
	When("a 2nd drpolicy is deleted", func() {
		It("should delete a cluster roles manifest work for each cluster specified in a 2nd drpolicy", func() {
			drpolicyDelete(&drpolicies[1], clusterNamesNone)
		})
	})
})
