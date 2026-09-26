package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"

	"oncall-agent/internal/agent"
	"oncall-agent/internal/judge"
	"oncall-agent/internal/observability"
	"oncall-agent/internal/rag"
	"oncall-agent/internal/tool"
)

// POST /alert + GET /reports（ADR-0005/0006）：告警驱动诊断，报告落内存环供人
// 查看；诊断后置管线 RunAlertDiagnosis 为 HTTP 与队列 worker 共用链。

// 报告状态（ADR-0006 异步契约）：queued 入队 → running 执行中 → done/failed。
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// reportRingCap 报告环容量：存最近 N 条告警驱动诊断，内存态重启即失。
const reportRingCap = 20

// Report 为一条告警驱动诊断的落点记录。Score=0 表示 judge 未评分（降级或关闭）。
type Report struct {
	ID         string       `json:"id"`
	Status     string       `json:"status"`
	ReceivedAt string       `json:"received_at"`
	Alerts     []tool.Alert `json:"alerts"`
	Diagnosis  string       `json:"diagnosis"`
	Citations  []rag.Result `json:"citations"`
	Ingested   int          `json:"ingested"`
	Score      int          `json:"score"`
	LowScore   bool         `json:"low_score"`
}

// reportRing 并发安全的定长报告环，零值可用。
type reportRing struct {
	mu    sync.Mutex
	items []Report
}

func (r *reportRing) add(rep Report) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, rep)
	if len(r.items) > reportRingCap {
		r.items = r.items[len(r.items)-reportRingCap:]
	}
}

// snapshot 返回副本，新→旧排列。
func (r *reportRing) snapshot() []Report {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Report, len(r.items))
	for i, it := range r.items {
		out[len(r.items)-1-i] = it
	}
	return out
}

// update 按 ID 就地改写环内条目（worker 回填 running/done/failed 用），
// 返回是否命中（条目可能已被环容量驱逐）。
func (r *reportRing) update(id string, mut func(*Report)) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.items {
		if r.items[i].ID == id {
			mut(&r.items[i])
			return true
		}
	}
	return false
}

// newReportID 报告 ID：8 字节随机 hex。
func newReportID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// amAlert 为 Alertmanager webhook payload 的单条告警形状。
type amAlert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Description string            `json:"description"`
	StartsAt    string            `json:"startsAt"`
}

func (a amAlert) toAlert() tool.Alert {
	desc := a.Annotations["description"]
	if desc == "" {
		desc = a.Description
	}
	name := a.Labels["alertname"]
	if name == "" {
		name = a.Labels["alert_name"]
	}
	return tool.Alert{
		Name:        name,
		Severity:    a.Labels["severity"],
		Description: desc,
		Labels:      a.Labels,
		StartsAt:    a.StartsAt,
	}
}

// parseAlertPayload 兼容三种形状：AM webhook（alerts[]）、单条 amAlert、
// tool.Alert（name 直填）。只收 firing（status 空按 firing），无可用告警报错。
func parseAlertPayload(raw []byte) ([]tool.Alert, error) {
	var payload struct {
		Alerts []amAlert `json:"alerts"`
	}
	alerts := []tool.Alert{}
	if err := json.Unmarshal(raw, &payload); err == nil && len(payload.Alerts) > 0 {
		for _, a := range payload.Alerts {
			if a.Status != "" && a.Status != "firing" {
				continue
			}
			alerts = append(alerts, a.toAlert())
		}
		return alerts, nil
	}
	var one amAlert
	if err := json.Unmarshal(raw, &one); err == nil && one.Labels != nil {
		if one.Status == "" || one.Status == "firing" {
			alerts = append(alerts, one.toAlert())
		}
		return alerts, nil
	}
	var direct tool.Alert
	if err := json.Unmarshal(raw, &direct); err == nil && direct.Name != "" {
		alerts = append(alerts, direct)
	}
	return alerts, nil
}

// AlertEnqueuer 诊断任务入队缝（ADR-0006）：生产实现 internal/queue.Client，
// 测试注入同步假实现；接口收窄使 handler 不依赖 asynq 类型。
type AlertEnqueuer interface {
	EnqueueAlertDiagnosis(reportID string, alerts []tool.Alert) error
}

// NotifyEnqueuer 通知任务入队缝（ADR-0008）：生产实现 queue.Client（同进程
// 收发两端），测试注入假实现；收窄同 AlertEnqueuer。
type NotifyEnqueuer interface {
	EnqueueNotification(reportID string, report []byte) error
}

// RunAlertDiagnosis 诊断后置管线（ADR-0005/0006，HTTP 与 asynq worker 共用链）：
// PlanPushed → 事件沉淀 → 落环（命中已有 ID 条目则就地回填，否则新增）→ 指标。
// id 由调用方生成：HTTP 同步路径现生成现用；异步路径在入队时生成并随任务透传。
func (h *Handler) RunAlertDiagnosis(ctx context.Context, id string, alerts []tool.Alert) Report {
	if h.PlannerAgent == nil {
		h.PlannerAgent = agent.New(nil, h.RAG)
	}
	diagnosis, citations := h.PlannerAgent.PlanPushed(ctx, alerts)
	rep := Report{
		ID:         id,
		Status:     StatusDone,
		ReceivedAt: time.Now().UTC().Format(time.RFC3339),
		Alerts:     alerts,
		Diagnosis:  diagnosis,
		Citations:  citations,
	}
	rep.Ingested = h.ingestIncident(alerts, diagnosis)
	// judge 自评分（ADR-0006）：纯观察值，失败降级 Score=0 不挡链；LLM 配置
	// 不全（如无 key）直接跳过。
	if h.judgeLLM.APIBase != "" && h.judgeLLM.Model != "" && strings.TrimSpace(h.judgeLLM.APIKey) != "" {
		score, reason, err := judge.Score(ctx, h.judgeLLM, diagnosis, citations)
		if err != nil {
			log.Printf("warn: judge score %s failed (degrade to unscored): %v", id, err)
		} else {
			rep.Score = score
			rep.LowScore = h.judgeThreshold > 0 && score < h.judgeThreshold
			log.Printf("info: judge %s score=%d/5 reason=%s", id, score, reason)
			if rep.LowScore {
				observability.AddDiagnosisScoreLow(ctx)
			}
		}
	}
	if !h.reports.update(id, func(r *Report) { *r = rep }) {
		h.reports.add(rep)
	}
	observability.AddAlertDiagnosis(ctx, 1)
	return rep
}

// Alert 处理 POST /alert：入队即回 202 {id, status:"queued"}（ADR-0006 单一异步
// 语义，BREAKING）。诊断由 worker 消费执行，结果落 /reports；队列未装配或同
// payload 任务在队返回 503（AM 退避后重试自愈）。
func (h *Handler) Alert(c *gin.Context) {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil || len(raw) == 0 {
		c.JSON(http.StatusBadRequest, errJSON("empty payload"))
		return
	}
	alerts, perr := parseAlertPayload(raw)
	if perr != nil {
		c.JSON(http.StatusBadRequest, errJSON("bad payload: "+perr.Error()))
		return
	}
	if len(alerts) == 0 {
		c.JSON(http.StatusBadRequest, errJSON("payload 无可用 firing 告警（需 alertname）"))
		return
	}
	for _, a := range alerts {
		if a.Name == "" {
			c.JSON(http.StatusBadRequest, errJSON("alert 缺 alertname"))
			return
		}
	}
	if h.queue == nil {
		c.JSON(http.StatusServiceUnavailable, errJSON("诊断队列未就绪（queue 未装配，需 redis）"))
		return
	}
	id := newReportID()
	h.reports.add(Report{
		ID:         id,
		Status:     StatusQueued,
		ReceivedAt: time.Now().UTC().Format(time.RFC3339),
		Alerts:     alerts,
	})
	if err := h.queue.EnqueueAlertDiagnosis(id, alerts); err != nil {
		msg := "诊断任务入队失败"
		if errors.Is(err, asynq.ErrTaskIDConflict) {
			msg = "同告警诊断任务已在队列（去重）"
		}
		log.Printf("warn: enqueue alert diagnosis %s: %v", id, err)
		h.reports.update(id, func(r *Report) {
			r.Status = StatusFailed
			r.Diagnosis = msg
		})
		c.JSON(http.StatusServiceUnavailable, errJSON(msg))
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"id":       id,
		"status":   StatusQueued,
		"received": len(alerts),
	})
}

// runChain 共用链注入缝：方法表达式的包级变量，生产零开销，测试换装注入
// panic/异常链路（F2 故障注入载体）。
var runChain = (*Handler).RunAlertDiagnosis

// ProcessAlertDiagnosis 队列 worker 回调（ADR-0006）：回填 running 后走共用链
// 回填终态；终态为 low_score 或 failed 时入队通知（ADR-0008 触发器只挂 worker
// 终态，手动 /chat 本人在场不通知）。链内 panic 在此收口：落 failed 终态 +
// 通知 + error 上抛交 asynq 退避重试——不恢复的现状是报告永停 running（ADR-0008
// 所指黑洞）。
func (h *Handler) ProcessAlertDiagnosis(ctx context.Context, reportID string, alerts []tool.Alert) (err error) {
	h.reports.update(reportID, func(r *Report) { r.Status = StatusRunning })
	defer func() {
		if rec := recover(); rec != nil {
			rep := h.markFailed(reportID, rec)
			h.maybeNotify(ctx, rep)
			err = fmt.Errorf("alert diagnosis %s panic: %v", reportID, rec)
		}
	}()
	rep := runChain(h, ctx, reportID, alerts)
	h.maybeNotify(ctx, rep)
	return nil
}

// markFailed 把报告环内条目落 failed 终态（panic 摘要进 Diagnosis），不中
// （条目被驱逐）则新增兜底。
func (h *Handler) markFailed(reportID string, rec any) Report {
	rep := Report{
		ID:         reportID,
		Status:     StatusFailed,
		ReceivedAt: time.Now().UTC().Format(time.RFC3339),
		Diagnosis:  fmt.Sprintf("%v", rec),
	}
	if !h.reports.update(reportID, func(r *Report) {
		r.Status = StatusFailed
		r.Diagnosis = rep.Diagnosis
	}) {
		h.reports.add(rep)
	}
	return rep
}

// maybeNotify 通知触发器（ADR-0008）：low_score（沿用 v0.5 布尔——未评分
// score=0 是「无分」不是低分）或 failed 终态才入队；载荷=报告整体 JSON（与
// GET /reports 条目同形状，自包含）。入队失败仅告警不挡诊断主链；同报告
// TaskID 冲突是 worker 重试期复发场景，记 info 即去重。
func (h *Handler) maybeNotify(ctx context.Context, rep Report) {
	if h.webhookURL == "" || h.notifier == nil || !(rep.LowScore || rep.Status == StatusFailed) {
		return
	}
	body, err := json.Marshal(rep)
	if err != nil {
		log.Printf("warn: notify %s marshal: %v", rep.ID, err)
		return
	}
	if err := h.notifier.EnqueueNotification(rep.ID, body); err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) {
			log.Printf("info: notify %s already queued (dedup)", rep.ID)
			return
		}
		// 通知失败不挡诊断主链（ADR-0008，与 judge 同降级姿态）。
		log.Printf("warn: notify %s enqueue: %v", rep.ID, err)
	}
}

// Reports 处理 GET /reports：返回最近告警驱动诊断（新→旧）。
func (h *Handler) Reports(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"reports": h.reports.snapshot()})
}

// ingestIncident 事件沉淀（ADR-0005）：每告警名一篇（doc={name}.incident.md），
// 同题覆盖；开关关或 RAG 缺席时跳过。返回入库篇数。
func (h *Handler) ingestIncident(alerts []tool.Alert, diagnosis string) int {
	if !h.autoIngest || h.RAG == nil {
		return 0
	}
	seen := map[string]bool{}
	ingested := 0
	for _, a := range alerts {
		if a.Name == "" || seen[a.Name] {
			continue
		}
		seen[a.Name] = true
		if err := h.RAG.IngestIncident(a.Name+".incident.md", incidentMarkdown(a, diagnosis)); err != nil {
			log.Printf("warn: incident ingest %s failed: %v", a.Name, err)
			continue
		}
		ingested++
	}
	if ingested > 0 {
		observability.AddIncidentIngested(context.Background(), int64(ingested))
	}
	return ingested
}

// incidentMarkdown 沉淀文档形状：一级标题即题（含告警名），标注来源与信任级。
func incidentMarkdown(a tool.Alert, diagnosis string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s 事件沉淀\n\n> AI 诊断报告自动入库（source=incident，检索降权，未经人工审定）。\n\n## 告警\n- alertname: %s\n- severity: %s\n- startsAt: %s\n- description: %s\n\n## 诊断\n%s\n",
		a.Name, a.Name, a.Severity, a.StartsAt, a.Description, diagnosis)
	return sb.String()
}
