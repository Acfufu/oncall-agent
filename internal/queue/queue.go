package queue

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/hibiken/asynq"

	"oncall-agent/internal/tool"
	"oncall-agent/internal/trace"
)

// 诊断队列（ADR-0006）：Redis+asynq 硬依赖，POST /alert 入队、worker 消费后调
// Handler.RunAlertDiagnosis 同一条链。单一执行形态，无进程内回退。

// TypeAlertDiagnosis 告警驱动诊断任务类型。
const TypeAlertDiagnosis = "diagnosis:alert"

// TypeNotification 通知写回任务类型（ADR-0008）：诊断终态（low_score/failed）
// 报告投递到配置 webhook。
const TypeNotification = "notify:report"

// queueName 单队列，消费顺序由 server 并发数控制。
const queueName = "diagnosis"

// notifyQueue 通知队列：快投递不与慢诊断互堵（ADR-0008 第二类 task）。
const notifyQueue = "notify"

// taskTimeout 单任务执行上限：诊断含多跳 LLM/RAG 调用，给足余量。
const taskTimeout = 10 * time.Minute

// notifyTimeout 单次通知任务上限：一次 HTTP POST，远小于诊断。
const notifyTimeout = time.Minute

// alertPayload 任务载荷：reportID 与入队时落环的 queued 条目一致，
// worker 据此回填 running/done/failed。
type alertPayload struct {
	ReportID string       `json:"report_id"`
	Alerts   []tool.Alert `json:"alerts"`
}

// notifyPayload 通知载荷：Report 是完整报告 JSON 原文（与 GET /reports 条目
// 同形状），信封仅用于传输与定位；投递 body 即 Report 原文（ADR-0008 载荷
// 自包含——/reports 环驱逐与重启丢历史不影响在途通知）。
type notifyPayload struct {
	ReportID string          `json:"report_id"`
	Report   json.RawMessage `json:"report"`
}

// Client 入队端。
type Client struct {
	c *asynq.Client
}

// NewClient 连接 Redis（地址缺省由 config.Queue.RedisAddr 兜底 localhost:6379）。
func NewClient(redisAddr string) *Client {
	return &Client{c: asynq.NewClient(asynq.RedisClientOpt{Addr: redisAddr})}
}

// Close 释放连接。
func (c *Client) Close() error { return c.c.Close() }

// EnqueueAlertDiagnosis 入队一条诊断任务。TaskID 只取 alerts 数组 JSON 的
// sha1 截断（report_id 不参与哈希）：兼容 AM 单 webhook 多告警，
// group_interval/repeat_interval 重投换 report_id 命中同一 TaskID 不重复入队；
// 同 ID 在 pending 期间重入队返回 ErrTaskIDConflict（HTTP 侧转 503，AM 退避
// 后重试自愈）。
func (c *Client) EnqueueAlertDiagnosis(reportID string, alerts []tool.Alert) error {
	payload, err := json.Marshal(alertPayload{ReportID: reportID, Alerts: alerts})
	if err != nil {
		return fmt.Errorf("marshal alert task: %w", err)
	}
	task := asynq.NewTask(TypeAlertDiagnosis, payload)
	_, err = c.c.Enqueue(task,
		asynq.Queue(queueName),
		asynq.TaskID(taskID(payload)),
		asynq.MaxRetry(3),
		asynq.Timeout(taskTimeout),
	)
	return err
}

// taskID 只哈希 alerts 数组 JSON——report_id 不参与：AM 重投换 report_id 仍
// 命中同一 TaskID（去重的根基，ADR-0006/F02）。unmarshal 失败回退哈希原文
// （防御：调用方刚 marshal 过，不会走到）。
func taskID(payload []byte) string {
	var p alertPayload
	if err := json.Unmarshal(payload, &p); err == nil {
		if alerts, err := json.Marshal(p.Alerts); err == nil {
			payload = alerts
		}
	}
	sum := sha1.Sum(payload)
	return hex.EncodeToString(sum[:16])
}

// EnqueueNotification 入队一条通知任务（ADR-0008）：TaskID 取 "notify:"+reportID，
// 同报告重入队即 ErrTaskIDConflict 天然去重（worker 重试期 panic 复发重通知场景
// 不重复投）；at-least-once 语义由接收端按 report id 幂等去重兜底。
func (c *Client) EnqueueNotification(reportID string, reportJSON []byte) error {
	payload, err := json.Marshal(notifyPayload{ReportID: reportID, Report: reportJSON})
	if err != nil {
		return fmt.Errorf("marshal notify task: %w", err)
	}
	task := asynq.NewTask(TypeNotification, payload)
	_, err = c.c.Enqueue(task,
		asynq.Queue(notifyQueue),
		asynq.TaskID("notify:"+reportID),
		asynq.MaxRetry(3),
		asynq.Timeout(notifyTimeout),
	)
	return err
}

// Handler worker 侧回调：执行诊断后置管线并回填报告环状态；投递终态通知。
type Handler interface {
	ProcessAlertDiagnosis(ctx context.Context, reportID string, alerts []tool.Alert) error
	ProcessNotification(ctx context.Context, reportID string, report []byte) error
}

// Server 消费端。启动/停止用 Start/Stop/Shutdown 组合——asynq 的 Run() 自装
// 信号处理器，与 main 的 signal.NotifyContext 冲突，禁用。
type Server struct {
	srv *asynq.Server
	h   Handler
}

// NewServer 建消费端：并发 2、双队列（诊断/通知，通知快投递不与慢诊断互堵）、
// 失败任务退避重试（重试上限入队时定）。通知任务用 5s<<n 的显式指数退避
// （ADR-0008，验收窗口内可观测重投）；诊断任务沿用 asynq 默认退避不动
// （ADR-0006 语义不变）。
func NewServer(redisAddr string, h Handler) *Server {
	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisAddr},
		asynq.Config{
			Concurrency: 2,
			Queues:      map[string]int{queueName: 10, notifyQueue: 5},
			RetryDelayFunc: func(n int, err error, t *asynq.Task) time.Duration {
				if t.Type() == TypeNotification {
					return 5 * time.Second << n
				}
				return asynq.DefaultRetryDelayFunc(n, err, t)
			},
		},
	)
	return &Server{srv: srv, h: h}
}

// Start 阻塞前先在 goroutine 调本方法的调用方自行把握：内部即刻开始拉取。
func (s *Server) Start() error { return s.srv.Start(s.mux()) }

// Stop 停止拉取新任务，在途任务继续跑完。
func (s *Server) Stop() { s.srv.Stop() }

// Shutdown 等待在途任务收尾并落盘状态（进程退出前必须调）。
func (s *Server) Shutdown() { s.srv.Shutdown() }

func (s *Server) mux() *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.HandleFunc(TypeAlertDiagnosis, s.handleAlertDiagnosis)
	mux.HandleFunc(TypeNotification, s.handleNotification)
	return mux
}

func (s *Server) handleAlertDiagnosis(ctx context.Context, t *asynq.Task) error {
	var p alertPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		// 载荷坏任务重试无意义，直接跳过重试进 archived。
		return fmt.Errorf("bad payload: %w: %v", asynq.SkipRetry, err)
	}
	// worker 根 span（ADR-0006）：HTTP span 树在异步边界断裂为已接受取舍，
	// Jaeger 以本 span 为根，带 report_id/alertname 定位。
	alertname := ""
	if len(p.Alerts) > 0 {
		alertname = p.Alerts[0].Name
	}
	ctx, span := trace.Start(ctx, "AlertWorker.Process", map[string]string{
		"report_id": p.ReportID,
		"alertname": alertname,
	})
	defer span.End()
	if err := s.h.ProcessAlertDiagnosis(ctx, p.ReportID, p.Alerts); err != nil {
		span.SetStatus(trace.StatusError, err.Error())
		span.RecordError(err)
		log.Printf("warn: alert diagnosis task %s failed: %v", p.ReportID, err)
		return err
	}
	span.SetStatus(trace.StatusOK, "")
	return nil
}

// handleNotification 通知任务包装（ADR-0008）：坏载荷 SkipRetry（重试无意义，
// 永远投不出去）；其余错误交 asynq 退避重投，重试语义在 handler.ProcessNotification。
func (s *Server) handleNotification(ctx context.Context, t *asynq.Task) error {
	var p notifyPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil || len(p.Report) == 0 {
		return fmt.Errorf("bad payload: %w: %v", asynq.SkipRetry, err)
	}
	ctx, span := trace.Start(ctx, "NotifyWorker.Deliver", map[string]string{
		"report_id": p.ReportID,
	})
	defer span.End()
	if err := s.h.ProcessNotification(ctx, p.ReportID, p.Report); err != nil {
		span.SetStatus(trace.StatusError, err.Error())
		span.RecordError(err)
		return err
	}
	span.SetStatus(trace.StatusOK, "")
	return nil
}
