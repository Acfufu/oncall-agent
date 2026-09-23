// Package agent OTel callbacks handler（v0.3 首刀，Eino issue #1028 手写版）。
//
// Eino 无原生 OTel handler，ADK 仅 Runner.Query/Run + WithCallbacks 触发，
// 直接 Run 无 callback，故此处手写 HandlerBuilder：OnStart 包 tracer.Start，
// OnEnd/OnError 包 span.End + RecordError + SetStatus。无 callback 处由
// 调用方透 context 手动 Start（见 Run/Plan/chat/post/fallback）。
//
// go.mod 暂无 otel 依赖，底层走 internal/trace（同形 API）；
// Tracer 名沿用 otel.Tracer("oncall-agent/eino") 约定，接真实 OTel 时无缝换。
package agent

import (
	"context"
	"time"

	"oncall-agent/internal/trace"
)

// TracerName 对齐 otel.Tracer("oncall-agent/eino")。
const TracerName = trace.TracerName

// 组件 + 类型属性键（span 上打 component/type，便于 Jaeger 16686 过滤）。
const (
	AttrComponent = "component"
	AttrSpanType  = "span.type"
	AttrModel     = "gen_ai.request.model"
	AttrSystem    = "gen_ai.system"
	AttrTokensIn  = "gen_ai.usage.input_tokens"
	AttrTokensOut = "gen_ai.usage.output_tokens"
)

// Component 为 Eino 回调组件名。
type Component string

const (
	ComponentChatModel Component = "ChatModel"
	ComponentTool      Component = "Tool"
	ComponentRAG       Component = "RAG"
	ComponentProm      Component = "Prom"
	ComponentRun       Component = "Run"
	ComponentPlan      Component = "Plan"
)

// CallbackInfo 为单次回调身份：组件 + 动作 + 对象名。
type CallbackInfo struct {
	Component Component
	Type      string // 如 chat / exec / search / query / firing
	Name      string // 如 model 名 / tool 名 / 查询摘要
}

// Handler 为手写 Eino callbacks.Handler：OnStart/OnEnd/OnError 包 span。
type Handler struct {
	info  CallbackInfo
	start time.Time
}

// HandlerBuilder 构造 Handler（对齐 Eino callbacks.HandlerBuilder 语义）。
type HandlerBuilder struct{}

// NewHandlerBuilder 构造 builder，无状态，可复用。
func NewHandlerBuilder() *HandlerBuilder { return &HandlerBuilder{} }

// OnStart 开子 span，返回带 span 的 ctx。
func (b *HandlerBuilder) OnStart(ctx context.Context, info CallbackInfo) context.Context {
	ctx, h := StartCallbackSpan(ctx, info)
	_ = h
	return ctx
}

// OnEnd 正常结束当前 span。
func (b *HandlerBuilder) OnEnd(ctx context.Context, tokensIn, tokensOut int) {
	EndCallbackSpan(ctx, nil, tokensIn, tokensOut)
}

// OnError 记错结束当前 span。
func (b *HandlerBuilder) OnError(ctx context.Context, err error) {
	EndCallbackSpan(ctx, err, 0, 0)
}

// StartCallbackSpan 按 component/type 打属性开 span。
func StartCallbackSpan(ctx context.Context, info CallbackInfo) (context.Context, *Handler) {
	attrs := map[string]string{
		AttrComponent: string(info.Component),
		AttrSpanType:  info.Type,
		AttrSystem:    "openai",
	}
	spanName := string(info.Component) + "." + info.Type
	if info.Name != "" {
		spanName += ":" + info.Name
	}
	ctx, _ = trace.Start(ctx, spanName, attrs)
	if s := trace.FromContext(ctx); s != nil && info.Name != "" {
		s.SetAttribute("name", info.Name)
	}
	return ctx, &Handler{info: info, start: time.Now()}
}

// EndCallbackSpan 落 token/耗时并结束 span；err 非空则 RecordError + Error 状态。
func EndCallbackSpan(ctx context.Context, err error, tokensIn, tokensOut int) {
	s := trace.FromContext(ctx)
	if s == nil {
		return
	}
	if tokensIn > 0 {
		s.SetAttribute(AttrTokensIn, itoa(tokensIn))
	}
	if tokensOut > 0 {
		s.SetAttribute(AttrTokensOut, itoa(tokensOut))
	}
	if err != nil {
		s.RecordError(err)
		s.SetStatus(trace.StatusError, err.Error())
	} else {
		s.SetStatus(trace.StatusOK, "")
	}
	s.End()
}

// 根 + 子 span 快捷构造（调用方透 ctx 手动 Start）。

// StartRunSpan ReAct.Run 根 span。
func StartRunSpan(ctx context.Context, sessionID string) (context.Context, *Handler) {
	ctx, h := StartCallbackSpan(ctx, CallbackInfo{Component: ComponentRun, Type: "run", Name: sessionID})
	return ctx, h
}

// StartPlanSpan Planner.Plan 根 span。
func StartPlanSpan(ctx context.Context) (context.Context, *Handler) {
	return StartCallbackSpan(ctx, CallbackInfo{Component: ComponentPlan, Type: "plan"})
}

// StartChatModelSpan ChatModel 子 span（gen_ai.request.model 打 model）。
func StartChatModelSpan(ctx context.Context, model string) context.Context {
	ctx, _ = StartCallbackSpan(ctx, CallbackInfo{Component: ComponentChatModel, Type: "chat", Name: model})
	if s := trace.FromContext(ctx); s != nil {
		s.SetAttribute(AttrModel, model)
	}
	return ctx
}

// StartToolSpan Tool 子 span（name + args 摘要）。
func StartToolSpan(ctx context.Context, name, args string) context.Context {
	if len(args) > 256 {
		args = args[:256] + "…"
	}
	ctx, _ = StartCallbackSpan(ctx, CallbackInfo{Component: ComponentTool, Type: "exec", Name: name})
	if s := trace.FromContext(ctx); s != nil {
		s.SetAttribute("tool.name", name)
		s.SetAttribute("tool.args", args)
	}
	return ctx
}

// StartRAGSpan RAG 子 span。
func StartRAGSpan(ctx context.Context, query string, topK int) context.Context {
	ctx, _ = StartCallbackSpan(ctx, CallbackInfo{Component: ComponentRAG, Type: "search"})
	if s := trace.FromContext(ctx); s != nil {
		if len(query) > 256 {
			query = query[:256] + "…"
		}
		s.SetAttribute("rag.query", query)
		s.SetAttribute("rag.top_k", itoa(topK))
	}
	return ctx
}

// StartPromQuerySpan PromQuery 子 span。
func StartPromQuerySpan(ctx context.Context, query string) context.Context {
	ctx, _ = StartCallbackSpan(ctx, CallbackInfo{Component: ComponentProm, Type: "query"})
	if s := trace.FromContext(ctx); s != nil {
		if len(query) > 256 {
			query = query[:256] + "…"
		}
		s.SetAttribute("prom.query", query)
	}
	return ctx
}

// StartFiringSpan Firing 拉告警子 span。
func StartFiringSpan(ctx context.Context) context.Context {
	ctx, _ = StartCallbackSpan(ctx, CallbackInfo{Component: ComponentProm, Type: "firing"})
	return ctx
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var tmp [20]byte
	i := len(tmp)
	for n > 0 {
		i--
		tmp[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		tmp[i] = '-'
	}
	return string(tmp[i:])
}
