package observability

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// RagHitsTotal is the OTel counter backing the `rag_hits_total` series
// scraped by Prometheus from /metrics (ADR 0004 acceptance).
var RagHitsTotal metric.Int64Counter

// AlertDiagnosesTotal backs `alert_diagnoses_total`: POST /alert 告警驱动诊断次数。
var AlertDiagnosesTotal metric.Int64Counter

// IncidentIngestedTotal backs `incident_ingested_total`: 事件沉淀入库次数（ADR 0005）。
var IncidentIngestedTotal metric.Int64Counter

// InitMetrics installs a Prometheus exporter + MeterProvider on the same
// process registry served by promhttp on /metrics, creates the counters,
// and returns its shutdown func.
func InitMetrics(ctx context.Context) (ShutdownFunc, error) {
	_ = ctx
	exp, err := prometheus.New()
	if err != nil {
		return nil, err
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exp))
	otel.SetMeterProvider(mp)
	meter := otel.Meter(ServiceName)
	counter, err := meter.Int64Counter(
		"rag_hits_total",
		metric.WithDescription("Total RAG search hits returned"),
	)
	if err != nil {
		_ = mp.Shutdown(context.Background())
		return nil, err
	}
	RagHitsTotal = counter
	if AlertDiagnosesTotal, err = meter.Int64Counter(
		"alert_diagnoses_total",
		metric.WithDescription("Total alert-driven diagnoses via POST /alert"),
	); err != nil {
		_ = mp.Shutdown(context.Background())
		return nil, err
	}
	if IncidentIngestedTotal, err = meter.Int64Counter(
		"incident_ingested_total",
		metric.WithDescription("Total incident notes auto-ingested into knowledge store"),
	); err != nil {
		_ = mp.Shutdown(context.Background())
		return nil, err
	}
	return mp.Shutdown, nil
}

// AddRagHits records n hits with the ambient context (no-op before InitMetrics).
func AddRagHits(ctx context.Context, n int64) {
	if RagHitsTotal == nil {
		return
	}
	RagHitsTotal.Add(ctx, n)
}

// AddAlertDiagnosis records one alert-driven diagnosis (no-op before InitMetrics).
func AddAlertDiagnosis(ctx context.Context, n int64) {
	if AlertDiagnosesTotal == nil {
		return
	}
	AlertDiagnosesTotal.Add(ctx, n)
}

// AddIncidentIngested records one incident note ingestion (no-op before InitMetrics).
func AddIncidentIngested(ctx context.Context, n int64) {
	if IncidentIngestedTotal == nil {
		return
	}
	IncidentIngestedTotal.Add(ctx, n)
}
