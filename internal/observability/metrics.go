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

// DiagnosisScoreLowTotal backs `diagnosis_score_low_total`: judge 自评分低于
// 阈值的诊断数（v0.5，ADR 0006，纯观察值）。
var DiagnosisScoreLowTotal metric.Int64Counter

// NotificationSentTotal backs `notification_sent_total`: 通知成功送达数（ADR 0008；
// 与 failed 成对——失败计数无成功分母不可读）。
var NotificationSentTotal metric.Int64Counter

// NotificationFailedTotal backs `notification_failed_total`: 通知重试耗尽终败数
// （ADR 0008，观察面不升级为状态机）。
var NotificationFailedTotal metric.Int64Counter

// DeployEventsCallsTotal backs `deploy_events_calls_total`: deploy_events 调用次数
// （v0.7，ADR 0009）。
var DeployEventsCallsTotal metric.Int64Counter

// DeployEventsErrorsTotal backs `deploy_events_errors_total`: deploy_events 失败
// 次数（ADR 0009，与 calls 成对——降级面可观测）。
var DeployEventsErrorsTotal metric.Int64Counter

// DeployEventsTotal backs `deploy_events_total`: deploy_events 成功返回的事件条数
// （ADR 0009，Add 带值非恒 1）。
var DeployEventsTotal metric.Int64Counter

// StoreFallbackTotal backs `store_fallback_total`: 向量库降级内存模式的转移次数
// （F04：memOnly 闩锁可观测）。
var StoreFallbackTotal metric.Int64Counter

// EmbedFallbackTotal backs `embed_fallback_total`: embedding 调用降级 hash 的次数
// （F04：embedder 静默回退可观测）。
var EmbedFallbackTotal metric.Int64Counter

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
	if DiagnosisScoreLowTotal, err = meter.Int64Counter(
		"diagnosis_score_low_total",
		metric.WithDescription("Total diagnoses scored below judge low threshold"),
	); err != nil {
		_ = mp.Shutdown(context.Background())
		return nil, err
	}
	if NotificationSentTotal, err = meter.Int64Counter(
		"notification_sent_total",
		metric.WithDescription("Total diagnosis notifications delivered to webhook"),
	); err != nil {
		_ = mp.Shutdown(context.Background())
		return nil, err
	}
	if NotificationFailedTotal, err = meter.Int64Counter(
		"notification_failed_total",
		metric.WithDescription("Total notifications dropped after retry exhaustion"),
	); err != nil {
		_ = mp.Shutdown(context.Background())
		return nil, err
	}
	if DeployEventsCallsTotal, err = meter.Int64Counter(
		"deploy_events_calls_total",
		metric.WithDescription("Total deploy_events tool calls (ADR 0009)"),
	); err != nil {
		_ = mp.Shutdown(context.Background())
		return nil, err
	}
	if DeployEventsErrorsTotal, err = meter.Int64Counter(
		"deploy_events_errors_total",
		metric.WithDescription("Total deploy_events calls failed (degraded, ADR 0009)"),
	); err != nil {
		_ = mp.Shutdown(context.Background())
		return nil, err
	}
	if DeployEventsTotal, err = meter.Int64Counter(
		"deploy_events_total",
		metric.WithDescription("Total deploy events returned on success (ADR 0009)"),
	); err != nil {
		_ = mp.Shutdown(context.Background())
		return nil, err
	}
	if StoreFallbackTotal, err = meter.Int64Counter(
		"store_fallback_total",
		metric.WithDescription("Total vector-store fallback transitions to memory mode (F04)"),
	); err != nil {
		_ = mp.Shutdown(context.Background())
		return nil, err
	}
	if EmbedFallbackTotal, err = meter.Int64Counter(
		"embed_fallback_total",
		metric.WithDescription("Total embedding calls degraded to hash fallback (F04)"),
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

// AddDiagnosisScoreLow records one low-score diagnosis (no-op before InitMetrics).
func AddDiagnosisScoreLow(ctx context.Context) {
	if DiagnosisScoreLowTotal == nil {
		return
	}
	DiagnosisScoreLowTotal.Add(ctx, 1)
}

// AddNotificationSent records one delivered notification (no-op before InitMetrics).
func AddNotificationSent(ctx context.Context) {
	if NotificationSentTotal == nil {
		return
	}
	NotificationSentTotal.Add(ctx, 1)
}

// AddNotificationFailed records one notification lost to retry exhaustion
// (no-op before InitMetrics).
func AddNotificationFailed(ctx context.Context) {
	if NotificationFailedTotal == nil {
		return
	}
	NotificationFailedTotal.Add(ctx, 1)
}

// AddDeployCalls records one deploy_events call (no-op before InitMetrics).
func AddDeployCalls(ctx context.Context) {
	if DeployEventsCallsTotal == nil {
		return
	}
	DeployEventsCallsTotal.Add(ctx, 1)
}

// AddDeployErrors records one failed deploy_events call (no-op before InitMetrics).
func AddDeployErrors(ctx context.Context) {
	if DeployEventsErrorsTotal == nil {
		return
	}
	DeployEventsErrorsTotal.Add(ctx, 1)
}

// AddDeployEvents records n deploy events returned on success (no-op before
// InitMetrics).
func AddDeployEvents(ctx context.Context, n int64) {
	if DeployEventsTotal == nil {
		return
	}
	DeployEventsTotal.Add(ctx, n)
}

// AddStoreFallback records one memory-mode fallback transition (no-op before
// InitMetrics).
func AddStoreFallback(ctx context.Context) {
	if StoreFallbackTotal == nil {
		return
	}
	StoreFallbackTotal.Add(ctx, 1)
}

// AddEmbedFallback records one hash-embed degradation (no-op before InitMetrics).
func AddEmbedFallback(ctx context.Context) {
	if EmbedFallbackTotal == nil {
		return
	}
	EmbedFallbackTotal.Add(ctx, 1)
}
