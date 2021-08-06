package util_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	rmnutil "github.com/ramendr/ramen/controllers/util"
)

var (
	testGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "ramen_test_gauge",
		Help: "Test Gauge for use in Metrics_Util only",
	})

	testCounter = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ramen_test_counter",
			Help: "Test Counter for use in Metrics_Util only",
		},
	)

	testHistogram = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "ramen_test_histogram",
			Help:    "Test Histogram for use in Metrics_Util only",
			Buckets: prometheus.ExponentialBuckets(1.0, 2.0, 12),
		},
	)

	testGaugeVec = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ramen_test_gauge_vector",
			Help: "Test Gauge Vec for use in Metrics_Util only",
		},
		[]string{
			"count",
		},
	)
)

// register Prometheus metrics for testing
func init() {
	metrics.Registry.MustRegister(testGauge, testGaugeVec, testCounter, testHistogram)
}

var _ = Describe("test metrics util functions", func() {
	Context("GetMetricValueFromName tests Prometheus metrics", func() {
		It("register custom metrics", func() {
			// note: go_goroutines was picked because it's A) a supported type, B) does not use vector values
			val, err := rmnutil.GetMetricValueSingle("ramen_test_gauge", dto.MetricType_GAUGE, metrics.Registry)

			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal(0.0)) // unused: default to zero (float64)
		})
	})

	Context("GetMetricValueFromName tests custom Prometheus metrics", func() {
		It("basic timer functionality", func() {
			timer := prometheus.NewTimer(prometheus.ObserverFunc(testGauge.Set))
			timer.ObserveDuration() // stop timer when function returns (all cases)

			val, err := rmnutil.GetMetricValueSingle("ramen_test_gauge", dto.MetricType_GAUGE, metrics.Registry)

			Expect(err).NotTo(HaveOccurred())
			Expect(val).NotTo(Equal(0.0)) // should be some small but non-zero value
		})

		It("basic counter functionality", func() {
			val, err := rmnutil.GetMetricValueSingle("ramen_test_counter", dto.MetricType_COUNTER, metrics.Registry)

			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal(0.0))

			testCounter.Inc()

			val, err = rmnutil.GetMetricValueSingle("ramen_test_counter", dto.MetricType_COUNTER, metrics.Registry)

			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal(1.0))
		})

		It("basic histogram functionality", func() {
			val, err := rmnutil.GetMetricValueSingle("ramen_test_histogram", dto.MetricType_HISTOGRAM, metrics.Registry)

			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal(0.0))

			testHistogram.Observe(2.5) // add arbitrary observation

			val, err = rmnutil.GetMetricValueSingle("ramen_test_histogram", dto.MetricType_HISTOGRAM, metrics.Registry)

			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal(1.0)) // get observation count
		})

		It("basic gaugeVec functionality", func() {
			nameA := "vrg-1"
			nameB := "vrg-2"
			valA := 2.0

			testGaugeVec.WithLabelValues(nameA).Set(valA)
			testGaugeVec.WithLabelValues(nameB) // create a labeled instance, but gets default value of 0

			val, err := rmnutil.GetMetricValueVector("ramen_test_gauge_vector", nameA, dto.MetricType_GAUGE, metrics.Registry)
			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal(valA))

			val, err = rmnutil.GetMetricValueVector("ramen_test_gauge_vector", nameB, dto.MetricType_GAUGE, metrics.Registry)
			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal(0.0))
		})
	})

	Context("GetMetricValueFromName error checks", func() {
		It("invalid name", func() {
			val, err := rmnutil.GetMetricValueSingle("invalid_metric", dto.MetricType_GAUGE, metrics.Registry)

			Expect(err).To(HaveOccurred())
			Expect(val).To(Equal(0.0))
		})

		It("incorrect metric type", func() {
			val, err := rmnutil.GetMetricValueSingle("ramen_test_gauge", dto.MetricType_HISTOGRAM, metrics.Registry)

			Expect(err).To(HaveOccurred())
			Expect(val).To(Equal(0.0))
		})
	})
})
