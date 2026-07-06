// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ocmworkv1 "open-cluster-management.io/api/work/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rmnutil "github.com/ramendr/ramen/internal/controller/util"
)

var _ = Describe("IsManifestInAppliedState", func() {
	Context("IsManifestInAppliedState checks ManifestWork with single condition", func() {
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

			Expect(rmnutil.IsManifestInAppliedState(mw)).To(Equal(false))
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

			Expect(rmnutil.IsManifestInAppliedState(mw)).To(Equal(false))
		})

		It("'Available' present, Status False", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkAvailable,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
							Status:             metav1.ConditionFalse,
						},
					},
				},
			}

			Expect(rmnutil.IsManifestInAppliedState(mw)).To(Equal(false))
		})

		It("'Available' present, Status true", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkAvailable,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
							Status:             metav1.ConditionTrue,
						},
					},
				},
			}

			Expect(rmnutil.IsManifestInAppliedState(mw)).To(Equal(false))
		})

		It("'Applied' or 'Available' not present", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkProgressing,
							LastTransitionTime: metav1.Time{Time: timeMostRecent},
							Status:             metav1.ConditionTrue,
						},
					},
				},
			}

			Expect(rmnutil.IsManifestInAppliedState(mw)).To(Equal(false))
		})
		It("'Degraded'", func() {
			mw := &ocmworkv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ramendr-vrg-roles",
				},
				Status: ocmworkv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{
						{
							Type:               ocmworkv1.WorkDegraded,
							LastTransitionTime: metav1.Time{Time: timeOld},
							Status:             metav1.ConditionTrue,
						},
					},
				},
			}

			Expect(rmnutil.IsManifestInAppliedState(mw)).To(Equal(false))
		})
	})

	Context("IsManifestInAppliedState checks ManifestWork with multiple conditions", func() {
		timeOld := time.Now().Local()
		timeMostRecent := timeOld.Add(time.Second)

		It("'Applied' and 'Progressing'", func() {
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
							Status:             metav1.ConditionTrue,
						},
					},
				},
			}

			Expect(rmnutil.IsManifestInAppliedState(mw)).To(Equal(false))
		})

		It("'Applied' and 'Available'", func() {
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
							Type:               ocmworkv1.WorkAvailable,
							LastTransitionTime: metav1.Time{Time: timeOld},
							Status:             metav1.ConditionTrue,
						},
					},
				},
			}

			Expect(rmnutil.IsManifestInAppliedState(mw)).To(Equal(true))
		})

		It("'Applied' and 'Available' but 'Degraded'", func() {
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
							Type:               ocmworkv1.WorkAvailable,
							LastTransitionTime: metav1.Time{Time: timeOld},
							Status:             metav1.ConditionTrue,
						},
						{
							Type:               ocmworkv1.WorkDegraded,
							LastTransitionTime: metav1.Time{Time: timeOld},
							Status:             metav1.ConditionTrue,
						},
					},
				},
			}

			Expect(rmnutil.IsManifestInAppliedState(mw)).To(Equal(false))
		})

		It("'Applied' and 'Available' but NOT 'Degraded'", func() {
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
							Type:               ocmworkv1.WorkAvailable,
							LastTransitionTime: metav1.Time{Time: timeOld},
							Status:             metav1.ConditionTrue,
						},
						{
							Type:               ocmworkv1.WorkDegraded,
							LastTransitionTime: metav1.Time{Time: timeOld},
							Status:             metav1.ConditionFalse,
						},
					},
				},
			}

			Expect(rmnutil.IsManifestInAppliedState(mw)).To(Equal(true))
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

			Expect(rmnutil.IsManifestInAppliedState(mw)).To(Equal(false))
		})
	})
})

var _ = Describe("DeleteNamespaceManifestWork", Ordered, func() {
	var (
		ctx         context.Context
		clusterName string
		mwu         rmnutil.MWUtil
	)

	// Use a unique namespace per test run since envtest does not support namespace
	// deletion (the kube-controller-manager is not running).
	// https://book.kubebuilder.io/reference/envtest#testing-considerations
	BeforeAll(func() {
		ctx = context.TODO()
		randomBytes := make([]byte, 8)
		rand.Read(randomBytes) // panics on failure
		clusterName = fmt.Sprintf("delete-ns-mw-%s", hex.EncodeToString(randomBytes))

		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: clusterName,
			},
		}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())

		mwu = rmnutil.MWUtil{
			Client: k8sClient,
			Ctx:    ctx,
			Log:    ctrl.Log.WithName("test"),
		}
	})

	It("sets orphan delete option on ManifestWork without one", func() {
		mwName := "without-delete-option"
		mw := &ocmworkv1.ManifestWork{
			ObjectMeta: metav1.ObjectMeta{
				Name:       mwName,
				Namespace:  clusterName,
				Finalizers: []string{"test.ramendr.openshift.io/keep-for-inspection"},
			},
		}
		Expect(k8sClient.Create(ctx, mw)).To(Succeed())
		DeferCleanup(deleteManifestWork, ctx, mwName, clusterName)

		Expect(mwu.DeleteNamespaceManifestWork(mwName, clusterName)).To(Succeed())

		key := types.NamespacedName{Name: mwName, Namespace: clusterName}
		Expect(k8sClient.Get(ctx, key, mw)).To(Succeed())
		Expect(mw.DeletionTimestamp.IsZero()).To(BeFalse())
		Expect(mw.Spec.DeleteOption).To(Equal(&ocmworkv1.DeleteOption{
			PropagationPolicy: ocmworkv1.DeletePropagationPolicyTypeOrphan,
		}))
	})

	It("keeps existing orphan delete option", func() {
		mwName := "with-delete-option"
		mw := &ocmworkv1.ManifestWork{
			ObjectMeta: metav1.ObjectMeta{
				Name:       mwName,
				Namespace:  clusterName,
				Finalizers: []string{"test.ramendr.openshift.io/keep-for-inspection"},
			},
			Spec: ocmworkv1.ManifestWorkSpec{
				DeleteOption: &ocmworkv1.DeleteOption{
					PropagationPolicy: ocmworkv1.DeletePropagationPolicyTypeOrphan,
				},
			},
		}
		Expect(k8sClient.Create(ctx, mw)).To(Succeed())
		DeferCleanup(deleteManifestWork, ctx, mwName, clusterName)

		Expect(mwu.DeleteNamespaceManifestWork(mwName, clusterName)).To(Succeed())

		key := types.NamespacedName{Name: mwName, Namespace: clusterName}
		Expect(k8sClient.Get(ctx, key, mw)).To(Succeed())
		Expect(mw.DeletionTimestamp.IsZero()).To(BeFalse())
		Expect(mw.Spec.DeleteOption).To(Equal(&ocmworkv1.DeleteOption{
			PropagationPolicy: ocmworkv1.DeletePropagationPolicyTypeOrphan,
		}))
	})
})

// deleteManifestWork is a best-effort cleanup helper that removes finalizers and deletes a
// ManifestWork. Errors are logged but do not fail the test.
func deleteManifestWork(ctx context.Context, name, namespace string) {
	mw := &ocmworkv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	patch := client.RawPatch(types.MergePatchType,
		[]byte(`{"metadata":{"finalizers":null}}`))

	if err := k8sClient.Patch(ctx, mw, patch); err != nil {
		if k8serrors.IsNotFound(err) {
			return
		}

		testLogger.Error(err, "Failed to remove finalizers", "name", name, "namespace", namespace)

		return
	}

	if err := k8sClient.Delete(ctx, mw); err != nil {
		if k8serrors.IsNotFound(err) {
			return
		}

		testLogger.Error(err, "Failed to delete ManifestWork", "name", name, "namespace", namespace)
	}
}
