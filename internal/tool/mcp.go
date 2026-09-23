// Package tool MCP client 缝（ADR-0004）：三只读经远端 MCP server，本地实现作 fallback。
//
// 接法对齐官方 modelcontextprotocol/go-sdk：同机 STDIO CommandTransport，
// 远程 StreamableHTTP，SSE 不新用。自研 Exec 白名单保留作鉴权/熔断层。
//
// 接法经官方 modelcontextprotocol/go-sdk：同机 STDIO CommandTransport，
// 远程 StreamableClientTransport（initialize+tools/call 信封由 SDK 处理）。
// Client/Connect/CallTool 签名冻结，调用方 Exec/WithMCP/Close 形状不变。
package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TransportKind 为 MCP 传输类型。
type TransportKind string

const (
	// TransportSTDIO 同机子进程，CommandTransport。
	TransportSTDIO TransportKind = "stdio"
	// TransportStreamableHTTP 远程 StreamableHTTP。
	TransportStreamableHTTP TransportKind = "streamable_http"
)

// MCPConfig 为远端 MCP server 接入配置。零值 = 禁用，直走本地实现。
type MCPConfig struct {
	Kind    TransportKind
	Command string
	Args    []string
	URL     string
	Timeout time.Duration
}

// DisabledMCPConfig 返回禁用配置（默认，本地直调）。
func DisabledMCPConfig() MCPConfig { return MCPConfig{} }

// IsEnabled 未禁用即启用。
func (c MCPConfig) IsEnabled() bool {
	switch c.Kind {
	case TransportSTDIO:
		return strings.TrimSpace(c.Command) != ""
	case TransportStreamableHTTP:
		return strings.TrimSpace(c.URL) != ""
	default:
		return false
	}
}

func (c MCPConfig) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 8 * time.Second
}

var (
	// ErrMCPDisabled 未配置远端，直走本地。
	ErrMCPDisabled = errors.New("tool: mcp disabled")
	// ErrMCPNotConnected 会话未建立。
	ErrMCPNotConnected = errors.New("tool: mcp not connected")
)

// MCPError 为远端调用失败（调用方原样回退本地，不中断）。
type MCPError struct {
	Tool   string
	Reason string
}

func (e *MCPError) Error() string { return fmt.Sprintf("mcp %s: %s", e.Tool, e.Reason) }

// Client 为 MCP 会话封装。零值不可用，经 NewMCPClient 构造。
// 会话经官方 SDK 建立（Client.Connect 得 ClientSession），initialize 握手由 SDK 完成。
// 生命周期由 NewDeps 管理，进程退出前调 Close（调 session.Close，幂等）。
type Client struct {
	mu       sync.Mutex
	cfg      MCPConfig
	http     *http.Client
	sdk      *sdkmcp.Client
	session  *sdkmcp.ClientSession
	attached bool
	closed   bool
}

// NewMCPClient 构造未连接会话，需再调 Connect。
func NewMCPClient(cfg MCPConfig) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.timeout()},
	}
}

// Connect 建立会话：STDIO 起子进程（CommandTransport），HTTP 连远端
// Streamable endpoint。握手 initialize 由 SDK 完成。幂等，可重复调。
func (c *Client) Connect(ctx context.Context) error {
	if c == nil {
		return ErrMCPDisabled
	}
	c.mu.Lock()
	if !c.cfg.IsEnabled() {
		c.mu.Unlock()
		return ErrMCPDisabled
	}
	if c.attached {
		c.mu.Unlock()
		return nil
	}
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("tool: mcp client closed: %w", ErrMCPNotConnected)
	}
	kind := c.cfg.Kind
	c.mu.Unlock()
	switch kind {
	case TransportSTDIO:
		return c.connectSTDIO(ctx)
	case TransportStreamableHTTP:
		return c.connectHTTP(ctx)
	default:
		return fmt.Errorf("tool: unknown mcp transport %q: %w", c.cfg.Kind, ErrMCPDisabled)
	}
}

// Connected 报告会话是否可用。
func (c *Client) Connected() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attached && !c.closed
}

// IsEnabled 报告是否配置远端。
func (c *Client) IsEnabled() bool {
	if c == nil {
		return false
	}
	return c.cfg.IsEnabled()
}

// Close 释放会话（session.Close 关传输/子进程，SDK 负责 kill）。幂等。
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.attached = false
	if c.session != nil {
		_ = c.session.Close()
		c.session = nil
	}
	c.sdk = nil
	return nil
}

// CallTool 经远端调工具，远端失联返回 *MCPError（调用方回退本地）。
func (c *Client) CallTool(ctx context.Context, name, argsJSON string) (string, error) {
	if c == nil || !c.IsEnabled() {
		return "", ErrMCPDisabled
	}
	if !c.Connected() {
		return "", ErrMCPNotConnected
	}
	argsJSON = strings.TrimSpace(argsJSON)
	if argsJSON == "" {
		argsJSON = "{}"
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("mcp %s bad args: %w", name, err)
	}
	switch c.cfg.Kind {
	case TransportSTDIO, TransportStreamableHTTP:
		return c.callRemote(ctx, name, args)
	default:
		return "", &MCPError{Tool: name, Reason: "unknown transport"}
	}
}
