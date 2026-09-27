// deploy_events 变更富化第四只读工具（v0.7，ADR-0009）：GitHub commits +
// deployments 只读拉取合成时间线。repo 从 config 读、工具不设 repo 参数——
// 作用域在配置层锁死；参数仅 since/until（RFC3339 可选，缺省最近 24h）；
// 两类事件各 10 条截断防 token 失控；无缓存直调，失败返回错误文本由调用方
// 降级，不挡诊断主链。仍是只读，不是 remediation。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"oncall-agent/internal/observability"
)

// deployFetchCap 单类事件截断上限（ADR-0009：各 10 条防 token 失控）。
const deployFetchCap = 10

// DeploySource 为 deploy_events 变更源依赖：Repo 空=工具不注册（config 门控）。
// BaseURL 缺省 api.github.com，测试可注入；Token 可选，public 仓库匿名即可。
type DeploySource struct {
	Repo    string
	Token   string
	BaseURL string
	Client  *http.Client
}

// Event 为报告 deploy_events 字段的扁平元素（env/sha/message/time，ADR-0009）：
// commit 的 Env 空串保形状统一，deployment 的 Env 取 environment。
type Event struct {
	Env     string `json:"env"`
	SHA     string `json:"sha"`
	Message string `json:"message"`
	Time    string `json:"time"`
}

type ghCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Author struct {
			Date string `json:"date"`
		} `json:"author"`
		Message string `json:"message"`
	} `json:"commit"`
}

type ghDeployment struct {
	SHA         string `json:"sha"`
	Environment string `json:"environment"`
	CreatedAt   string `json:"created_at"`
	Description string `json:"description"`
	Ref         string `json:"ref"`
}

// parseWindow 解析 since/until（RFC3339），空缺省最近 24h，坏参报错。
func parseWindow(since, until string) (time.Time, time.Time, error) {
	u := time.Now().UTC()
	s := u.Add(-24 * time.Hour)
	var err error
	if strings.TrimSpace(since) != "" {
		if s, err = time.Parse(time.RFC3339, since); err != nil {
			return s, u, fmt.Errorf("deploy_events bad since (want RFC3339): %w", err)
		}
	}
	if strings.TrimSpace(until) != "" {
		if u, err = time.Parse(time.RFC3339, until); err != nil {
			return s, u, fmt.Errorf("deploy_events bad until (want RFC3339): %w", err)
		}
	}
	return s, u, nil
}

// FetchDeployEvents 拉取窗口内 commits+deployments 合成倒序时间线（两条链
// 共用：工具 dispatch 与报告挂载）。三计数器在此累加——calls/errors 为调用面，
// total 为成功返回的事件条数。失败转 error 文本由调用方降级。
func FetchDeployEvents(ctx context.Context, src DeploySource, since, until string) ([]Event, error) {
	observability.AddDeployCalls(ctx)
	s, u, err := parseWindow(since, until)
	if err != nil {
		observability.AddDeployErrors(ctx)
		return nil, err
	}
	if strings.TrimSpace(src.Repo) == "" {
		observability.AddDeployErrors(ctx)
		return nil, fmt.Errorf("deploy_events: repo not configured")
	}
	commits, err := fetchCommits(ctx, src, s, u)
	if err != nil {
		observability.AddDeployErrors(ctx)
		return nil, err
	}
	deploys, err := fetchDeployments(ctx, src, s, u)
	if err != nil {
		observability.AddDeployErrors(ctx)
		return nil, err
	}
	evs := append(commits, deploys...)
	sort.Slice(evs, func(i, j int) bool { return evs[i].Time > evs[j].Time })
	observability.AddDeployEvents(ctx, int64(len(evs)))
	return evs, nil
}

// fetchCommits commits API 原生 since/until 窗过滤，per_page=10 截断。
func fetchCommits(ctx context.Context, src DeploySource, s, u time.Time) ([]Event, error) {
	q := url.Values{
		"since":    {s.Format(time.RFC3339)},
		"until":    {u.Format(time.RFC3339)},
		"per_page": {"10"},
	}
	raw, err := githubGet(ctx, src, "/repos/"+src.Repo+"/commits?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var cs []ghCommit
	if err := json.Unmarshal(raw, &cs); err != nil {
		return nil, fmt.Errorf("deploy_events decode commits: %w", err)
	}
	if len(cs) > deployFetchCap {
		cs = cs[:deployFetchCap]
	}
	evs := make([]Event, 0, len(cs))
	for _, c := range cs {
		evs = append(evs, Event{
			Env:     "",
			SHA:     c.SHA,
			Message: firstLine(c.Commit.Message),
			Time:    c.Commit.Author.Date,
		})
	}
	return evs, nil
}

// fetchDeployments deployments 端点无 since/until 参数——per_page=10 按创建
// 时间倒序拉取后客户端按窗口过滤，再截 10。
func fetchDeployments(ctx context.Context, src DeploySource, s, u time.Time) ([]Event, error) {
	q := url.Values{"per_page": {"10"}}
	raw, err := githubGet(ctx, src, "/repos/"+src.Repo+"/deployments?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var ds []ghDeployment
	if err := json.Unmarshal(raw, &ds); err != nil {
		return nil, fmt.Errorf("deploy_events decode deployments: %w", err)
	}
	evs := make([]Event, 0, len(ds))
	for _, d := range ds {
		t, err := time.Parse(time.RFC3339, d.CreatedAt)
		if err != nil || t.Before(s) || t.After(u) {
			continue
		}
		msg := d.Description
		if msg == "" {
			msg = d.Ref
		}
		evs = append(evs, Event{Env: d.Environment, SHA: d.SHA, Message: msg, Time: d.CreatedAt})
		if len(evs) >= deployFetchCap {
			break
		}
	}
	return evs, nil
}

// githubGet 只读 GET GitHub API，非 2xx 转含 status 的 error 文本（与
// prometheus_query 同缝，ADR-0009）。
func githubGet(ctx context.Context, src DeploySource, path string) ([]byte, error) {
	base := strings.TrimRight(strings.TrimSpace(src.BaseURL), "/")
	if base == "" {
		base = "https://api.github.com"
	}
	client := &http.Client{Timeout: 10 * time.Second}
	if src.Client != nil {
		client = src.Client
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if strings.TrimSpace(src.Token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(src.Token))
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("deploy_events github unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("deploy_events github %d: %s", resp.StatusCode, truncStr(string(raw), 256))
	}
	return raw, nil
}

// DeployEventsWithContext 工具面：解析 since/until 参数，返回 agent 面文本与
// 结构化事件（结构化供将来消费，文本进 ReAct 上下文）。
func (s *DeploySource) DeployEventsWithContext(ctx context.Context, argsJSON string) (string, []Event, error) {
	var args struct {
		Since string `json:"since"`
		Until string `json:"until"`
	}
	if err := argsOf(argsJSON, &args); err != nil {
		return "", nil, fmt.Errorf("deploy_events bad args: %w", err)
	}
	evs, err := FetchDeployEvents(ctx, *s, args.Since, args.Until)
	if err != nil {
		return "", nil, err
	}
	sw, uw, _ := parseWindow(args.Since, args.Until)
	return renderDeployEvents(s.Repo, sw, uw, evs), evs, nil
}

// renderDeployEvents agent 面文本定形：首行 header + 每事件一行。
func renderDeployEvents(repo string, since, until time.Time, evs []Event) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "deploy_events repo=%s window=[%s, %s] total=%d\n", repo, since.Format(time.RFC3339), until.Format(time.RFC3339), len(evs))
	for _, e := range evs {
		fmt.Fprintf(&sb, "%s env=%s sha=%s %s\n", e.Time, e.Env, shortSHA(e.SHA), truncStr(e.Message, 80))
	}
	return sb.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
