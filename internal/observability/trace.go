// Package observability wires OpenTelemetry tracing for oncall-agent.
//
// Trace path: OTLP gRPC -> collector -> Jaeger (ADR 0004).
// Metrics path: /metrics scraped directly by Prometheus, not via collector.
package observability

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Service identity shared by trace resource and otelgin span.
const (
	ServiceName    = "oncall-agent"
	ServiceVersion = "v0.3.0"
	// OTLPEndpoint is the local collector gRPC endpoint (ADR 0004).
	OTLPEndpoint = "localhost:4317"
)

// ShutdownFunc releases a provider with a bounded context.
type ShutdownFunc func(context.Context) error

// InitTracer builds a global TracerProvider backed by OTLP gRPC,
// sets W3C TraceContext+Baggage propagator, and returns its shutdown func.
// Dev policy: AlwaysSample (prod ParentBased 0.1 handled by collector/sample config).
func InitTracer(ctx context.Context) (ShutdownFunc, error) {
	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(OTLPEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(ServiceName),
			semconv.ServiceVersion(ServiceVersion),
		),
	)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return tp.Shutdown, nil
}
