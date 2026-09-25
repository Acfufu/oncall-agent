package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"oncall-agent/internal/config"
	"oncall-agent/internal/rag"
)

// 诊断自评分（ADR-0006）：LLM 对诊断的 1-5 评估，纯观察值——分数挂
// /reports 条目供人参考，不驱动任何行为；调用方在评分失败时降级为无分、
// 不挡诊断主链。复用主 LLM 配置（OpenAI 兼容 chat/completions）。

// scoreRe 从回复中取「分数: N」；模型不守格式时退而取首个 1-5 数字。
var scoreRe = regexp.MustCompile(`分数[:：]\s*([1-5])`)
var anyDigitRe = regexp.MustCompile(`[1-5]`)

// Score 给诊断打 1-5 分，返回 (分数, 一句理由, error)。引用忠实度与处置
// 可用性合并为一分：5=引用充分支撑且步骤可直接执行，3=大体可用但依据
// 不足，1=引用不支撑或含编造。
func Score(ctx context.Context, cfg config.OpenAIConfig, diagnosis string, citations []rag.Result) (int, string, error) {
	var sb strings.Builder
	sb.WriteString("你是运维诊断质检员。仅依据下列知识库引用，对诊断报告打 1-5 分（纯观察值，供人参考）：\n" +
		"5=引用充分支撑且处置步骤可直接执行；3=大体可用但依据不足或步骤笼统；1=引用不支撑或含编造。\n" +
		"只输出一行：「分数: <1-5>」，随后跟一句不超过 40 字的理由。\n\n诊断：\n" + diagnosis + "\n")
	if len(citations) == 0 {
		sb.WriteString("\n知识库引用：（无）\n")
	}
	for i, c := range citations {
		fmt.Fprintf(&sb, "\n%d.【%s】%s\n", i+1, c.Doc, trunc(c.Snippet, 300))
	}
	body, err := json.Marshal(map[string]any{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "user", "content": sb.String()},
		},
		"temperature": 0,
		"max_tokens":  128,
	})
	if err != nil {
		return 0, "", fmt.Errorf("marshal judge request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.APIBase, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(cfg.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.APIKey))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("judge LLM call: %w", err)
	}
	defer resp.Body.Close()
	var rsp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&rsp); err != nil {
		return 0, "", fmt.Errorf("decode judge reply: %w", err)
	}
	if len(rsp.Choices) == 0 {
		return 0, "", fmt.Errorf("judge reply empty choices")
	}
	reply := strings.TrimSpace(rsp.Choices[0].Message.Content)
	score, reason := parseReply(reply)
	if score == 0 {
		return 0, "", fmt.Errorf("judge reply unparsable: %q", trunc(reply, 80))
	}
	return score, reason, nil
}

// parseReply 优先「分数: N」格式，理由取分数标记之后的行内剩余文本。
func parseReply(reply string) (int, string) {
	if m := scoreRe.FindStringSubmatchIndex(reply); m != nil {
		score := int(reply[m[2]] - '0')
		reason := strings.TrimSpace(strings.TrimLeft(reply[m[1]:], ":： \t"))
		return score, trunc(reason, 60)
	}
	if m := anyDigitRe.FindStringIndex(reply); m != nil {
		return int(reply[m[0]] - '0'), trunc(reply, 60)
	}
	return 0, ""
}

func trunc(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
