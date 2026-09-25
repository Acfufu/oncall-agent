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

// queueName 单队列，消费顺序由 server 并发数控制。
const queueName = "diagnosis"

// taskTimeout 单任务执行上限：诊断含多跳 LLM/RAG 调用，给足余量。
const taskTimeout = 10 * time.Minute

// alertPayload 任务载荷：reportID 与入队时落环的 queued 条目一致，
// worker 据此回填 running/done/failed。
type alertPayload struct {
	ReportID string       `json:"report_id"`
	Alerts   []tool.Alert `json:"alerts"`
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

// EnqueueAlertDiagnosis 入队一条诊断任务。TaskID 取整个 alerts 数组 JSON 的
// sha1 截断：兼容 AM 单 webhook 多告警，group_interval/repeat_interval 重投
// 不重复入队；同 ID 在 pending 期间重入队返回 ErrTaskIDConflict（HTTP 侧转
// 503，AM 退避后重试自愈）。
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

// taskID sha1 截 16 字节 hex（asynq TaskID 上限 255 字符）。
func taskID(payload []byte) string {
	sum := sha1.Sum(payload)
	return hex.EncodeToString(sum[:16])
}

// Handler worker 侧回调：执行诊断后置管线并回填报告环状态。
type Handler interface {
	ProcessAlertDiagnosis(ctx context.Context, reportID string, alerts []tool.Alert) error
}

// Server 消费端。启动/停止用 Start/Stop/Shutdown 组合——asynq 的 Run() 自装
// 信号处理器，与 main 的 signal.NotifyContext 冲突，禁用。
type Server struct {
	srv *asynq.Server
	h   Handler
}

// NewServer 建消费端：并发 2、单队列、失败任务退避重试（重试上限入队时定）。
func NewServer(redisAddr string, h Handler) *Server {
	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisAddr},
		asynq.Config{Concurrency: 2, Queues: map[string]int{queueName: 10}},
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
