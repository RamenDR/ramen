// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	rmnutil "github.com/ramendr/ramen/controllers/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
