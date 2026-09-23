// Package tool OTel 子 span（v0.3 首刀，stdlib only，经 internal/trace）。
package tool

import (
	"context"

	"oncall-agent/internal/trace"
)

// startToolSpan 按 name + args 摘要开 Exec 子 span。
func startToolSpan(ctx context.Context, name, args string) (context.Context, *trace.Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(args) > 256 {
		args = args[:256] + "…"
	}
	ctx, s := trace.Start(ctx, "Tool.exec:"+name, map[string]string{
		"component": "Tool",
		"span.type": "exec",
		"tool.name": name,
		"tool.args": args,
	})
	return ctx, s
}

// endToolSpan 收尾：err 非空 RecordError + Error 状态。
func endToolSpan(s *trace.Span, err error) {
	if s == nil {
		return
	}
	if err != nil {
		s.RecordError(err)
		s.SetStatus(trace.StatusError, err.Error())
	} else {
		s.SetStatus(trace.StatusOK, "")
	}
	s.End()
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
