package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	gomegaTypes "github.com/onsi/gomega/types"
	workv1 "github.com/open-cluster-management/api/work/v1"
	ramen "github.com/ramendr/ramen/api/v1alpha1"
	"github.com/ramendr/ramen/controllers/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	validationErrors "k8s.io/kube-openapi/pkg/validation/errors"
)

var _ = Describe("DrpolicyController", func() {
	clusterNamesCurrent := &sets.String{}
	clusterNames := func(drpolicy *ramen.DRPolicy) sets.String {
		return sets.NewString(util.DrpolicyClusterNames(drpolicy)...)
	}
	drClustersExpect := func() {
		Eventually(
			func(g Gomega) {
				clusterNames := sets.String{}
				g.Expect(util.DrClustersList(context.TODO(), k8sClient, &clusterNames)).To(Succeed())
				g.Expect(clusterNames.UnsortedList()).To(ConsistOf(clusterNamesCurrent.UnsortedList()))
			},
			timeout,
			interval,
		).Should(Succeed())
	}
	validatedConditionExpect := func(drpolicy *ramen.DRPolicy, status metav1.ConditionStatus,
		messageMatcher gomegaTypes.GomegaMatcher,
	) {
		Eventually(
			func(g Gomega) {
				g.Expect(apiReader.Get(
					context.TODO(),
					types.NamespacedName{Name: drpolicy.Name},
					drpolicy,
				)).To(Succeed())
				g.Expect(drpolicy.Status.Conditions).To(MatchElements(
					func(element interface{}) string {
						return element.(metav1.Condition).Type
					},
					IgnoreExtras,
					Elements{
						ramen.DRPolicyValidated: MatchAllFields(Fields{
							`Type`:               Ignore(),
							`Status`:             Equal(status),
							`ObservedGeneration`: Equal(drpolicy.Generation),
							`LastTransitionTime`: Ignore(),
							`Reason`:             Ignore(),
							`Message`:            messageMatcher,
						}),
					},
				))
			},
			timeout,
			interval,
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
		drClustersExpect()
	}
	drpolicyDeleteAndConfirm := func(drpolicy *ramen.DRPolicy) {
		Expect(k8sClient.Delete(context.TODO(), drpolicy)).To(Succeed())
		Eventually(func() bool {
			return errors.IsNotFound(apiReader.Get(context.TODO(), types.NamespacedName{Name: drpolicy.Name}, drpolicy))
		}, timeout, interval).Should(BeTrue())
	}
	namespaceDeleteAndConfirm := func(namespaceName string) {
		// TODO: debug namespace delete not finalized
		if true {
			return
		}
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceName}}
		Expect(k8sClient.Delete(context.TODO(), namespace)).To(Succeed())
		Eventually(func() bool {
			err := apiReader.Get(context.TODO(), types.NamespacedName{Name: namespaceName}, namespace)
			s, _ := (json.MarshalIndent(*namespace, "", "  "))
			fmt.Println(string(s))

			return errors.IsNotFound(err)
		}, timeout*3, interval).Should(BeTrue())
	}
	drpolicyDelete := func(drpolicy *ramen.DRPolicy, clusterNamesExpected sets.String) {
		drpolicyDeleteAndConfirm(drpolicy)
		for _, clusterName := range clusterNamesCurrent.Difference(clusterNamesExpected).UnsortedList() {
			manifestWorkName := types.NamespacedName{
				Name:      util.DrClusterManifestWorkName,
				Namespace: clusterName,
			}
			Eventually(func() bool {
				manifestWork := &workv1.ManifestWork{}

				return errors.IsNotFound(apiReader.Get(context.TODO(), manifestWorkName, manifestWork))
			}, timeout, interval).Should(BeTrue())
			namespaceDeleteAndConfirm(clusterName)
			*clusterNamesCurrent = clusterNamesCurrent.Delete(clusterName)
		}
		drClustersExpect()
	}
	clusters := [...]ramen.ManagedCluster{
		{Name: `cluster0`, S3ProfileName: s3ProfileNameConnectSucc},
		{Name: `cluster1`, S3ProfileName: s3ProfileNameConnectSucc},
		{Name: `cluster2`, S3ProfileName: s3ProfileNameConnectSucc},
	}
	objectMetas := [...]metav1.ObjectMeta{
		{Name: `drpolicy0`},
		{Name: `drpolicy1`},
	}
	drpolicies := [...]ramen.DRPolicy{
		{
			ObjectMeta: objectMetas[0],
			Spec:       ramen.DRPolicySpec{DRClusterSet: clusters[0:2], SchedulingInterval: `00m`},
		},
		{
			ObjectMeta: objectMetas[1],
			Spec:       ramen.DRPolicySpec{DRClusterSet: clusters[1:3], SchedulingInterval: `9999999d`},
		},
	}
	clusterNamesNone := sets.String{}
	var drpolicy *ramen.DRPolicy
	Specify(`a drpolicy`, func() {
		drpolicy = &drpolicies[0]
	})
	When("a drpolicy is created specifying a cluster name and a namespace of the same name does not exist", func() {
		It("should set its validated status condition's status to false", func() {
			Expect(k8sClient.Create(context.TODO(), drpolicy)).To(Succeed())
			validatedConditionExpect(drpolicy, metav1.ConditionFalse, Ignore())
		})
	})
	Specify("drpolicy delete", func() {
		drpolicyDeleteAndConfirm(drpolicy)
	})
	Specify("a drpolicy", func() {
		drpolicy.ObjectMeta = objectMetas[0]
	})
	When("a 1st drpolicy is created", func() {
		It("should create a drcluster manifest work for each cluster specified in a 1st drpolicy", func() {
			drpolicyCreate(drpolicy)
		})
	})
	When(`a drpolicy is created containing an s3 profile that connects successfully`, func() {
		It("should set its validated status condition's status to true", func() {
			validatedConditionExpect(drpolicy, metav1.ConditionTrue, Ignore())
		})
	})
	When("TODO a 1st drpolicy is updated to add some clusters and remove some other clusters", func() {
		It("should create a drcluster manifest work for each cluster added and "+
			"delete a drcluster manifest work for each cluster removed", func() {
		})
	})
	When("a 2nd drpolicy is created specifying some clusters in a 1st drpolicy and some not", func() {
		It("should create a drcluster manifest work for each cluster specified in a 2nd drpolicy but not a 1st drpolicy",
			func() {
				drpolicyCreate(&drpolicies[1])
			},
		)
	})
	When("a 1st drpolicy is deleted", func() {
		It("should delete a drcluster manifest work for each cluster specified in a 1st drpolicy but not a 2nd drpolicy",
			func() {
				drpolicyDelete(drpolicy, clusterNames(&drpolicies[1]))
			},
		)
	})
	When("a 2nd drpolicy is deleted", func() {
		It("should delete a drcluster manifest work for each cluster specified in a 2nd drpolicy", func() {
			drpolicyDelete(&drpolicies[1], clusterNamesNone)
		})
	})
	Specify(`a drpolicy`, func() {
		drpolicy.ObjectMeta = objectMetas[0]
	})
	When(`a drpolicy creation request contains an invalid scheduling interval`, func() {
		It(`should fail`, func() {
			err := func() *errors.StatusError {
				path := field.NewPath(`spec`, `schedulingInterval`)
				value := ``

				return errors.NewInvalid(
					schema.GroupKind{
						Group: ramen.GroupVersion.Group,
						Kind:  `DRPolicy`,
					},
					drpolicy.Name,
					field.ErrorList{
						field.Invalid(
							path,
							value,
							validationErrors.FailedPattern(
								path.String(),
								`body`,
								`^\d+[mhd]$`,
								value,
							).Error(),
						),
					},
				)
			}()
			drpolicy.Spec.SchedulingInterval = `3s`
			Expect(k8sClient.Create(context.TODO(), drpolicy)).To(MatchError(err))
			drpolicy.Spec.SchedulingInterval = `0`
			Expect(k8sClient.Create(context.TODO(), drpolicy)).To(MatchError(err))
		})
	})
	Specify(`a drpolicy`, func() {
		drpolicy.Spec.SchedulingInterval = `00m`
	})
	When(`a drpolicy is created containing an s3 profile that connects unsuccessfully`, func() {
		It("should set its validated status condition's status to false and "+
			"message to specify the name of the first listed s3 profile that connected unsuccessfully", func() {
			drpolicy.Spec.DRClusterSet[1].S3ProfileName = s3ProfileNameConnectFail
			Expect(k8sClient.Create(context.TODO(), drpolicy)).To(Succeed())
			validatedConditionExpect(drpolicy, metav1.ConditionFalse, HavePrefix(s3ProfileNameConnectFail+": "))
		})
	})
	When("a drpolicy is updated containing an s3 profile that connects unsuccessfully "+
		"ordered before one that previously connected unsuccessfully", func() {
		It("should change its validated status condition"+
			"message to specify the name of the first listed s3 profile that connected unsuccessfully", func() {
			drpolicy.Spec.DRClusterSet[0].S3ProfileName = s3ProfileNameConnectFail2
			Expect(k8sClient.Update(context.TODO(), drpolicy)).To(Succeed())
			validatedConditionExpect(drpolicy, metav1.ConditionFalse, HavePrefix(s3ProfileNameConnectFail2+": "))
		})
	})
	When("a drpolicy is updated containing s3 profiles that all connect successfully", func() {
		It("should update its validated status condition's status to true", func() {
			drpolicy.Spec.DRClusterSet[0].S3ProfileName = s3ProfileNameConnectSucc
			drpolicy.Spec.DRClusterSet[1].S3ProfileName = s3ProfileNameConnectSucc
			Expect(k8sClient.Update(context.TODO(), drpolicy)).To(Succeed())
			validatedConditionExpect(drpolicy, metav1.ConditionTrue, Ignore())
		})
	})
	When("a previously valid drpolicy is updated containing a different s3 profile that connects successfully", func() {
		It("should update its validated status condition's observed generation", func() {
			drpolicy.Spec.DRClusterSet[0].S3ProfileName = s3ProfileNameConnectSucc2
			Expect(k8sClient.Update(context.TODO(), drpolicy)).To(Succeed())
			validatedConditionExpect(drpolicy, metav1.ConditionTrue, Ignore())
		})
	})
	When("a previously valid drpolicy is updated containing an s3 profile connects unsuccessfully", func() {
		It("should update its validated status condition's status to false", func() {
			drpolicy.Spec.DRClusterSet[0].S3ProfileName = s3ProfileNameConnectFail
			Expect(k8sClient.Update(context.TODO(), drpolicy)).To(Succeed())
			validatedConditionExpect(drpolicy, metav1.ConditionFalse, HavePrefix(s3ProfileNameConnectFail+": "))
		})
	})
	Specify(`drpolicy delete`, func() {
		drpolicyDeleteAndConfirm(drpolicy)
	})
})
