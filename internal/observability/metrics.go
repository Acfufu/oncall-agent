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

// InitMetrics installs a Prometheus exporter + MeterProvider on the same
// process registry served by promhttp on /metrics, creates rag_hits_total,
// and returns its shutdown func.
func InitMetrics(ctx context.Context) (ShutdownFunc, error) {
	_ = ctx
	exp, err := prometheus.New()
	if err != nil {
		return nil, err
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exp))
	otel.SetMeterProvider(mp)
	counter, err := otel.Meter(ServiceName).Int64Counter(
		"rag_hits_total",
		metric.WithDescription("Total RAG search hits returned"),
	)
	if err != nil {
		_ = mp.Shutdown(context.Background())
		return nil, err
	}
	RagHitsTotal = counter
	return mp.Shutdown, nil
}

// AddRagHits records n hits with the ambient context (no-op before InitMetrics).
func AddRagHits(ctx context.Context, n int64) {
	if RagHitsTotal == nil {
		return
	}
	RagHitsTotal.Add(ctx, n)
}
