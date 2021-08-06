package util

import (
	"fmt"

	dto "github.com/prometheus/client_model/go"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

func GetMetricValueSingle(name string, mfType dto.MetricType, registry metrics.RegistererGatherer) (float64, error) {
	mf, err := getMetricFamilyFromRegistry(name, registry)
	if err != nil {
		return 0.0, fmt.Errorf("GetMetricValueSingle returned error finding MetricFamily: %w", err)
	}

	val, err := getMetricValueFromMetricFamilyByTypeSingle(mf, mfType)
	if err != nil {
		return 0.0, fmt.Errorf("GetMetricValueSingle returned error finding Value: %w", err)
	}

	return val, nil
}

func GetMetricValueVector(name string, label string, mfType dto.MetricType,
	registry metrics.RegistererGatherer) (float64, error) {
	mf, err := getMetricFamilyFromRegistry(name, registry)
	if err != nil {
		return 0.0, fmt.Errorf("GetMetricValueSingle returned error finding MetricFamily: %w", err)
	}

	val, err := getMetricValueFromMetricFamilyByTypeVector(mf, label, mfType)
	if err != nil {
		return 0.0, fmt.Errorf("GetMetricValueSingle returned error finding Value: %w", err)
	}

	return val, nil
}

func getMetricFamilyFromRegistry(name string, registry metrics.RegistererGatherer) (*dto.MetricFamily, error) {
	metricsFamilies, err := registry.Gather()
	if err != nil {
		return nil, fmt.Errorf("found error during Gather step of getMetricFamilyFromRegistry: %w", err)
	}

	if len(metricsFamilies) == 0 {
		return nil, fmt.Errorf("couldn't get metricsFamilies from Prometheus Registry")
	}

	// TODO: find out if there's a better way to search than linear scan
	for i := 0; i < len(metricsFamilies); i++ {
		if *metricsFamilies[i].Name == name {
			return metricsFamilies[i], nil
		}
	}

	return nil, fmt.Errorf(fmt.Sprintf("couldn't find MetricFamily with name %s", name))
}

func getMetricValueFromMetricFamilyByTypeSingle(mf *dto.MetricFamily, mfType dto.MetricType) (float64, error) {
	if *mf.Type != mfType {
		return 0.0, fmt.Errorf("getMetricValueFromMetricFamilyByTypeSingle passed invalid type. Wanted %s, got %s",
			string(mfType), string(*mf.Type))
	}

	if len(mf.Metric) != 1 {
		return 0.0, fmt.Errorf("getMetricValueFromMetricFamilyByTypeSingle only supports Metric length=1")
	}

	return getValueFromMetricWithIndex(mf, mfType, 0)
}

func getMetricValueFromMetricFamilyByTypeVector(
	mf *dto.MetricFamily, label string, mfType dto.MetricType) (float64, error) {
	if *mf.Type != mfType {
		return 0.0, fmt.Errorf("getMetricValueFromMetricFamilyByTypeSingle passed invalid type. Wanted %s, got %s",
			string(mfType), string(*mf.Type))
	}

	// find correct index
	for i := 0; i < len(mf.Metric); i++ {
		if *mf.Metric[i].Label[0].Value == label {
			return getValueFromMetricWithIndex(mf, mfType, i)
		}
	}

	// didn't find it
	return 0.0, fmt.Errorf(
		fmt.Sprintf("getMetricValueFromMetricFamilyByTypeSingle couldn't find label with name '%s'. len(Metric)=%d",
			label, len(mf.Metric)))
}

func getValueFromMetricWithIndex(mf *dto.MetricFamily, mfType dto.MetricType, index int) (float64, error) {
	switch mfType {
	case dto.MetricType_COUNTER:
		return *mf.Metric[index].Counter.Value, nil
	case dto.MetricType_GAUGE:
		return *mf.Metric[index].Gauge.Value, nil
	case dto.MetricType_HISTOGRAM:
		// Count is more useful for testing over Sum; get Sum elsewhere if needed
		return float64(*mf.Metric[index].Histogram.SampleCount), nil
	case dto.MetricType_SUMMARY:
		fallthrough
	case dto.MetricType_UNTYPED:
		fallthrough
	default:
		return 0.0, fmt.Errorf("getValueFromMetricWithIndex doesn't support type %s yet. Implement this",
			string(mfType))
	}
}
