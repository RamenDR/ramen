package util_test

import (
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	ocmworkv1 "github.com/open-cluster-management/api/work/v1"
	rmnutil "github.com/ramendr/ramen/controllers/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// register Prometheus metrics for testing
func init() {
	metrics.Registry.MustRegister(testGauge, testCounter, testHistogram)
}

var (
	testGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ramen_test_gauge",
		Help: "Test Gauge for use in MW_Util only",
	})

	testCounter = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ramen_test_counter",
			Help: "Test Counter for use in MW_Util only",
		},
	)

	testHistogram = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "ramen_test_histogram",
			Help:    "Test Histogram for use in MW_Util only",
			Buckets: prometheus.ExponentialBuckets(1.0, 2.0, 12),
		},
	)
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

	Context("GetMetricValueFromName tests Prometheus metrics", func() {
		It("register custom metrics", func() {
			// note: go_goroutines was picked because it's A) a supported type, B) does not use vector values
			val, err := rmnutil.GetMetricValueSingle("ramen_test_gauge", dto.MetricType_GAUGE)

			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal(0.0)) // unused: default to zero (float64)
		})
	})

	Context("GetMetricValueFromName tests custom Prometheus metrics", func() {
		It("basic timer functionality", func() {
			timer := prometheus.NewTimer(prometheus.ObserverFunc(testGauge.Set))
			time.Sleep(1 * time.Second) // let the observer gather some time
			timer.ObserveDuration()     // stop timer when function returns (all cases)

			val, err := rmnutil.GetMetricValueSingle("ramen_test_gauge", dto.MetricType_GAUGE)

			Expect(err).NotTo(HaveOccurred())
			Expect(val).NotTo(Equal(0.0)) // should be some small but non-zero value
		})

		It("basic counter functionality", func() {
			val, err := rmnutil.GetMetricValueSingle("ramen_test_counter", dto.MetricType_COUNTER)

			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal(0.0))

			testCounter.Inc()

			val, err = rmnutil.GetMetricValueSingle("ramen_test_counter", dto.MetricType_COUNTER)

			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal(1.0))
		})

		It("basic histogram functionality", func() {
			val, err := rmnutil.GetMetricValueSingle("ramen_test_histogram", dto.MetricType_HISTOGRAM)

			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal(0.0))

			testHistogram.Observe(2.5) // add arbitrary observation

			val, err = rmnutil.GetMetricValueSingle("ramen_test_histogram", dto.MetricType_HISTOGRAM)

			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal(1.0)) // get observation count
		})
	})

	Context("GetMetricValueFromName error checks", func() {
		It("invalid name", func() {
			val, err := rmnutil.GetMetricValueSingle("invalid_metric", dto.MetricType_GAUGE)

			Expect(err).To(HaveOccurred())
			Expect(val).To(Equal(0.0))
		})

		It("incorrect metric type", func() {
			val, err := rmnutil.GetMetricValueSingle("ramen_test_gauge", dto.MetricType_HISTOGRAM)

			Expect(err).To(HaveOccurred())
			Expect(val).To(Equal(0.0))
		})
	})
})
