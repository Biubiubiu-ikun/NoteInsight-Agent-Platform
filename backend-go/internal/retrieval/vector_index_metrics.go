package retrieval

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus"
)

type VectorIndexMetricsCollector struct {
	db            *sqlx.DB
	builds        *prometheus.Desc
	checkpointLag *prometheus.Desc
	oldestUpdate  *prometheus.Desc
	scrapeSuccess *prometheus.Desc
}

func NewVectorIndexMetricsCollector(db *sqlx.DB) *VectorIndexMetricsCollector {
	return &VectorIndexMetricsCollector{
		db: db,
		builds: prometheus.NewDesc(
			"retrieval_vector_index_builds", "Vector index control rows by status.", []string{"status"}, nil,
		),
		checkpointLag: prometheus.NewDesc(
			"retrieval_vector_index_max_checkpoint_lag_points",
			"Maximum remaining points between an ingestion snapshot and its vector checkpoint by status.",
			[]string{"status"}, nil,
		),
		oldestUpdate: prometheus.NewDesc(
			"retrieval_vector_index_oldest_update_age_seconds",
			"Age of the oldest vector index control-row update by status.", []string{"status"}, nil,
		),
		scrapeSuccess: prometheus.NewDesc(
			"retrieval_vector_index_metrics_scrape_success",
			"Whether the latest vector index database metrics scrape succeeded.", nil, nil,
		),
	}
}

func (c *VectorIndexMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.builds
	ch <- c.checkpointLag
	ch <- c.oldestUpdate
	ch <- c.scrapeSuccess
}

func (c *VectorIndexMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	if c.db == nil {
		ch <- prometheus.MustNewConstMetric(c.scrapeSuccess, prometheus.GaugeValue, 0)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := c.db.QueryxContext(ctx, `
SELECT vi.status,
       COUNT(*)::double precision AS build_count,
       COALESCE(MAX(GREATEST(ir.chunk_count-vi.checkpoint_point_count,0)),0)::double precision AS checkpoint_lag,
       COALESCE(MAX(EXTRACT(EPOCH FROM (now()-vi.updated_at))),0)::double precision AS oldest_update_age
FROM retrieval_vector_indexes vi
JOIN ingestion_runs ir ON ir.run_id=vi.ingestion_run_id
GROUP BY vi.status
ORDER BY vi.status`)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(c.scrapeSuccess, prometheus.GaugeValue, 0)
		return
	}
	defer rows.Close()
	type metricRow struct {
		Status          string  `db:"status"`
		BuildCount      float64 `db:"build_count"`
		CheckpointLag   float64 `db:"checkpoint_lag"`
		OldestUpdateAge float64 `db:"oldest_update_age"`
	}
	for rows.Next() {
		var row metricRow
		if err := rows.StructScan(&row); err != nil {
			ch <- prometheus.MustNewConstMetric(c.scrapeSuccess, prometheus.GaugeValue, 0)
			return
		}
		ch <- prometheus.MustNewConstMetric(c.builds, prometheus.GaugeValue, row.BuildCount, row.Status)
		ch <- prometheus.MustNewConstMetric(c.checkpointLag, prometheus.GaugeValue, row.CheckpointLag, row.Status)
		ch <- prometheus.MustNewConstMetric(c.oldestUpdate, prometheus.GaugeValue, row.OldestUpdateAge, row.Status)
	}
	if err := rows.Err(); err != nil {
		ch <- prometheus.MustNewConstMetric(c.scrapeSuccess, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.scrapeSuccess, prometheus.GaugeValue, 1)
}
