// Package trace 最轻量 span 封装（v0.3 真实 OTel 接线）。
//
// 底层走全局 otel.TracerProvider（由 internal/observability.InitTracer 初始化，
// OTLP gRPC -> collector -> Jaeger，见 ADR-0004），此处不另建 provider。
// 包外 API 与首刀同形：Tracer(TracerName) + Start + span.End/RecordError/
// SetStatus，context 透传（W3C TraceContext 经 ctx 父子链透传）。
package trace

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// TracerName 与 ADR-0004 约定一致。
const TracerName = "oncall-agent/eino"

// Status codes（对齐 OTel codes，包外 API 保持稳定）。
type StatusCode int

const (
	StatusUnset StatusCode = iota
	StatusOK
	StatusError
)

type ctxKey struct{}

// Span 为 OTel span 薄封装：真实上报走内嵌 otelSpan，
// start 仅用于 Elapsed()/duration_ms，nil 安全。
// 无 provider/exporter 时 otel 返回 noop span，不打日志不阻塞。
type Span struct {
	mu       sync.Mutex
	name     string
	start    time.Time
	ended    bool
	otelSpan oteltrace.Span
}

// Start 沿用 otel.Tracer.Start 形：返回带 span 的 ctx + span。
// 属性经 attribute.String 打入，父子关系经 ctx 透传。
func Start(ctx context.Context, name string, attrs map[string]string) (context.Context, *Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	kvs := make([]attribute.KeyValue, 0, len(attrs))
	for k, v := range attrs {
		if k == "" {
			continue
		}
		kvs = append(kvs, attribute.String(k, v))
	}
	ctx2, os := otel.Tracer(TracerName).Start(ctx, name, oteltrace.WithAttributes(kvs...))
	s := &Span{name: name, start: time.Now(), otelSpan: os}
	return context.WithValue(ctx2, ctxKey{}, s), s
}

// FromContext 取出当前 span，无则 nil。
func FromContext(ctx context.Context) *Span {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(ctxKey{}).(*Span)
	return s
}

// Name 返回 span 名。
func (s *Span) Name() string {
	if s == nil {
		return ""
	}
	return s.name
}

// SetAttribute 写属性，nil 安全。
func (s *Span) SetAttribute(k, v string) {
	if s == nil || k == "" {
		return
	}
	s.mu.Lock()
	os := s.otelSpan
	s.mu.Unlock()
	if os == nil {
		return
	}
	os.SetAttributes(attribute.String(k, v))
}

// SetStatus 写状态，nil 安全。
func (s *Span) SetStatus(code StatusCode, msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	os := s.otelSpan
	s.mu.Unlock()
	if os == nil {
		return
	}
	os.SetStatus(toOTelStatus(code), msg)
}

// RecordError 记错 + 标 error 状态，nil 安全（err 为 nil 时 no-op）。
func (s *Span) RecordError(err error) {
	if s == nil || err == nil {
		return
	}
	s.mu.Lock()
	os := s.otelSpan
	s.mu.Unlock()
	if os == nil {
		return
	}
	os.RecordError(err)
	os.SetStatus(codes.Error, err.Error())
}

// End 结束 span 并落耗时属性，重复调用 no-op。
func (s *Span) End() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	os := s.otelSpan
	el := time.Since(s.start)
	s.mu.Unlock()
	if os == nil {
		return
	}
	os.SetAttributes(attribute.String("duration_ms", formatMs(el)))
	os.End()
}

// Elapsed 返回已耗时（未 End 时为至今）。
func (s *Span) Elapsed() time.Duration {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.start)
}

func toOTelStatus(c StatusCode) codes.Code {
	switch c {
	case StatusOK:
		return codes.Ok
	case StatusError:
		return codes.Error
	default:
		return codes.Unset
	}
}

func formatMs(d time.Duration) string {
	ms := float64(d.Nanoseconds()) / 1e6
	b := make([]byte, 0, 16)
	neg := false
	if ms < 0 {
		neg = true
		ms = -ms
	}
	// 最多 3 位小数，手写避免 fmt 依赖开销。
	intPart := int64(ms)
	frac := int64((ms-float64(intPart))*1000 + 0.5)
	if frac >= 1000 {
		intPart++
		frac -= 1000
	}
	if neg {
		b = append(b, '-')
	}
	b = appendInt(b, intPart)
	b = append(b, '.')
	b = append(b, byte('0'+frac/100), byte('0'+(frac/10)%10), byte('0'+frac%10))
	return string(b)
}

func appendInt(b []byte, n int64) []byte {
	if n == 0 {
		return append(b, '0')
	}
	var tmp [20]byte
	i := len(tmp)
	for n > 0 {
		i--
		tmp[i] = byte('0' + n%10)
		n /= 10
	}
	return append(b, tmp[i:]...)
}
