package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectSTDIO 经官方 SDK 建同机子进程会话（CommandTransport）。
// initialize 握手由 sdk.Connect 完成。成功后发布会话并标 attached。
func (c *Client) connectSTDIO(ctx context.Context) error {
	c.mu.Lock()
	if c.attached || c.closed {
		attached := c.attached
		c.mu.Unlock()
		if attached {
			return nil
		}
		return fmt.Errorf("tool: mcp client closed: %w", ErrMCPNotConnected)
	}
	cmdName := strings.TrimSpace(c.cfg.Command)
	args := append([]string(nil), c.cfg.Args...)
	c.mu.Unlock()
	if cmdName == "" {
		return fmt.Errorf("tool: mcp stdio command empty: %w", ErrMCPDisabled)
	}

	sdkClient := sdkmcp.NewClient(
		&sdkmcp.Implementation{Name: "oncall-agent", Version: "v0.1"},
		nil,
	)
	transport := &sdkmcp.CommandTransport{Command: exec.Command(cmdName, args...)}
	session, err := sdkClient.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("mcp stdio connect %q: %w", cmdName, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		_ = session.Close()
		return fmt.Errorf("tool: mcp client closed: %w", ErrMCPNotConnected)
	}
	c.sdk = sdkClient
	c.session = session
	c.attached = true
	return nil
}

// connectHTTP 经官方 SDK 连远端 StreamableHTTP endpoint
// （StreamableClientTransport）。initialize 握手由 sdk.Connect 完成，
// 替代旧版裸 GET 探活。成功后发布会话并标 attached。
func (c *Client) connectHTTP(ctx context.Context) error {
	c.mu.Lock()
	if c.attached || c.closed {
		attached := c.attached
		c.mu.Unlock()
		if attached {
			return nil
		}
		return fmt.Errorf("tool: mcp client closed: %w", ErrMCPNotConnected)
	}
	base := strings.TrimRight(strings.TrimSpace(c.cfg.URL), "/")
	httpClient := c.http
	timeout := c.cfg.timeout()
	c.mu.Unlock()
	if base == "" {
		return fmt.Errorf("tool: mcp http url empty: %w", ErrMCPDisabled)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}

	sdkClient := sdkmcp.NewClient(
		&sdkmcp.Implementation{Name: "oncall-agent", Version: "v0.1"},
		nil,
	)
	transport := &sdkmcp.StreamableClientTransport{
		Endpoint:             base,
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true,
	}
	session, err := sdkClient.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("mcp http connect %s: %w", base, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		_ = session.Close()
		return fmt.Errorf("tool: mcp client closed: %w", ErrMCPNotConnected)
	}
	c.sdk = sdkClient
	c.session = session
	c.attached = true
	return nil
}

// callRemote 经 SDK 会话调 tools/call。传输错误与空结果一律包成
// *MCPError，调用方（execRemote）原样回退本地，不中断。
func (c *Client) callRemote(ctx context.Context, name string, args map[string]any) (string, error) {
	c.mu.Lock()
	session := c.session
	attached := c.attached
	closed := c.closed
	c.mu.Unlock()
	if session == nil || !attached || closed {
		return "", ErrMCPNotConnected
	}
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return "", &MCPError{Tool: name, Reason: err.Error()}
	}
	return extractToolText(name, res)
}

// extractToolText 抽 tools/call 文本：content[0].text 优先（旧 stub 语义保留），
// 非文本或空 content 时回退 StructuredContent JSON，仍无可用文本报 MCPError。
func extractToolText(name string, res *sdkmcp.CallToolResult) (string, error) {
	if res == nil {
		return "", &MCPError{Tool: name, Reason: "empty result"}
	}
	for _, content := range res.Content {
		if tc, ok := content.(*sdkmcp.TextContent); ok {
			if res.IsError {
				return "", &MCPError{Tool: name, Reason: tc.Text}
			}
			return tc.Text, nil
		}
	}
	if res.StructuredContent != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			return "", &MCPError{Tool: name, Reason: err.Error()}
		}
		if res.IsError {
			return "", &MCPError{Tool: name, Reason: string(raw)}
		}
		return string(raw), nil
	}
	if len(res.Content) == 0 {
		return "", &MCPError{Tool: name, Reason: "empty result"}
	}
	return "", &MCPError{Tool: name, Reason: "non-text content"}
}
