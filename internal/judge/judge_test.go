package judge

import (
	"strings"
	"testing"

	"oncall-agent/internal/config"
)

func TestParseReplyFormatted(t *testing.T) {
	score, reason := parseReply("分数: 4 引用支撑充分，步骤可直接执行")
	if score != 4 || reason == "" {
		t.Fatalf("score=%d reason=%q", score, reason)
	}
}

func TestParseReplyFullWidthColon(t *testing.T) {
	score, _ := parseReply("分数：2\n依据不足")
	if score != 2 {
		t.Fatalf("score=%d want 2", score)
	}
}

func TestParseReplyFallbackDigit(t *testing.T) {
	score, _ := parseReply("引用尚可，整体评 3 分")
	if score != 3 {
		t.Fatalf("score=%d want 3", score)
	}
}

func TestParseReplyUnparsable(t *testing.T) {
	if score, _ := parseReply("无法评分"); score != 0 {
		t.Fatalf("score=%d want 0", score)
	}
}

// Score 入参拼装：引用为空时提示（无），请求构造路径不 panic 由集成验收覆盖。
func TestTrunc(t *testing.T) {
	if got := trunc(strings.Repeat("长", 80), 60); len([]rune(got)) != 61 {
		t.Fatalf("trunc len=%d want 61(60+…)", len([]rune(got)))
	}
}

// LowThreshold 默认值护栏：缺配置时 3。
func TestDefaultJudgeConfig(t *testing.T) {
	if config.Default().Judge.LowThreshold != 3 {
		t.Fatal("default low_threshold must be 3")
	}
}
