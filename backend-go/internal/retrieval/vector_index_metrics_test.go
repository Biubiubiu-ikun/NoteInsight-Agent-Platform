package retrieval

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestVectorIndexMetricsCollectorReportsUnavailableDatabase(t *testing.T) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewVectorIndexMetricsCollector(nil))
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "retrieval_vector_index_metrics_scrape_success" {
			continue
		}
		if len(family.Metric) != 1 || family.Metric[0].GetGauge().GetValue() != 0 {
			t.Fatalf("scrape success metric = %v, want one zero gauge", family.Metric)
		}
		return
	}
	t.Fatal("scrape success metric was not collected")
}
