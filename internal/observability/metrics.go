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
