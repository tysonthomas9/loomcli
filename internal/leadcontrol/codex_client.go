//nolint:staticcheck // The project already uses nhooyr/websocket; migration to coder/websocket is separate.
package leadcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket" //nolint:staticcheck // existing project websocket dependency
)

const codexRPCReadLimit = 8 << 20

type CodexClient struct {
	conn *websocket.Conn
	next atomic.Int64
}

type codexAppServerClient interface {
	Close(reason string) error
	ListThreads(ctx context.Context, cwd string, limit int) ([]CodexThread, error)
	ReadThread(ctx context.Context, threadID string) (*CodexThread, error)
	StartTurn(ctx context.Context, threadID, text string) error
}

var dialCodexAppServerClient = func(ctx context.Context, endpoint string) (codexAppServerClient, error) {
	return DialCodexAppServer(ctx, endpoint)
}

type CodexThread struct {
	ID          string            `json:"id"`
	Preview     string            `json:"preview"`
	Cwd         string            `json:"cwd"`
	CreatedAt   float64           `json:"createdAt"`
	CreatedAtMS float64           `json:"createdAtMs"`
	UpdatedAt   float64           `json:"updatedAt"`
	UpdatedAtMS float64           `json:"updatedAtMs"`
	Status      CodexThreadStatus `json:"status"`
	Turns       []CodexTurn       `json:"turns"`
}

type CodexThreadStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags,omitempty"`
}

type CodexTurn struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Items  []CodexTurnItem `json:"items"`
}

type CodexTurnItem struct {
	Type    string              `json:"type"`
	ID      string              `json:"id"`
	Text    string              `json:"text"`
	Content []CodexContentBlock `json:"content"`
	Phase   string              `json:"phase,omitempty"`

	// Tool-ish item fields (commandExecution, mcpToolCall, fileChange, …).
	// Unknown types keep unmarshaling; consumers ignore empty fields.
	Command          string            `json:"command,omitempty"`
	Cwd              string            `json:"cwd,omitempty"`
	Status           string            `json:"status,omitempty"`
	AggregatedOutput string            `json:"aggregatedOutput,omitempty"`
	Query            string            `json:"query,omitempty"`
	Server           string            `json:"server,omitempty"`
	Tool             string            `json:"tool,omitempty"`
	Arguments        json.RawMessage   `json:"arguments,omitempty"`
	Result           json.RawMessage   `json:"result,omitempty"`
	Error            json.RawMessage   `json:"error,omitempty"`
	Changes          []CodexFileChange `json:"changes,omitempty"`
}

type CodexFileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind,omitempty"`
	Diff string `json:"diff,omitempty"`
}

type CodexContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (it CodexTurnItem) PlainText() string {
	switch it.Type {
	case "agentMessage":
		return it.Text
	case "userMessage":
		var b strings.Builder
		for _, block := range it.Content {
			if block.Type != "text" {
				continue
			}
			b.WriteString(block.Text)
		}
		return b.String()
	default:
		return ""
	}
}

func (s CodexThreadStatus) RuntimeStatus() string {
	switch s.Type {
	case "idle":
		return RuntimeStatusIdle
	case "active":
		for _, flag := range s.ActiveFlags {
			switch flag {
			case "waitingOnApproval":
				return RuntimeStatusWaitingApproval
			case "waitingOnUserInput":
				return RuntimeStatusWaitingUserInput
			}
		}
		return RuntimeStatusActive
	case "systemError":
		return RuntimeStatusFailed
	case "notLoaded", "":
		return RuntimeStatusDisconnected
	default:
		return s.Type
	}
}

func (s CodexThreadStatus) CanStartTurn() bool {
	return s.Type == "idle"
}

type rpcRequest struct {
	ID     int64  `json:"id,omitempty"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type rpcResponse struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return ""
	}
	if len(e.Data) > 0 {
		return fmt.Sprintf("codex app-server rpc error %d: %s: %s", e.Code, e.Message, string(e.Data))
	}
	return fmt.Sprintf("codex app-server rpc error %d: %s", e.Code, e.Message)
}

func DialCodexAppServer(ctx context.Context, endpoint string) (*CodexClient, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, errors.New("codex app-server endpoint required")
	}
	conn, _, err := websocket.Dial(ctx, endpoint, nil) //nolint:bodyclose // websocket response body is managed by nhooyr
	if err != nil {
		return nil, fmt.Errorf("dial codex app-server %s: %w", endpoint, err)
	}
	conn.SetReadLimit(codexRPCReadLimit)
	c := &CodexClient{conn: conn}
	if err := c.Initialize(ctx); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "initialize failed")
		return nil, err
	}
	return c, nil
}

func (c *CodexClient) Close(reason string) error {
	if c == nil || c.conn == nil {
		return nil
	}
	if strings.TrimSpace(reason) == "" {
		reason = "done"
	}
	return c.conn.Close(websocket.StatusNormalClosure, reason)
}

func (c *CodexClient) Initialize(ctx context.Context) error {
	params := map[string]any{
		"clientInfo": map[string]any{
			"name":    "loom",
			"title":   "Loom",
			"version": "dev",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}
	var result json.RawMessage
	if err := c.Call(ctx, "initialize", params, &result); err != nil {
		return err
	}
	return c.Notify(ctx, "initialized", nil)
}

func (c *CodexClient) ListThreads(ctx context.Context, cwd string, limit int) ([]CodexThread, error) {
	params := map[string]any{}
	if strings.TrimSpace(cwd) != "" {
		params["cwd"] = strings.TrimSpace(cwd)
	}
	if limit > 0 {
		params["limit"] = limit
	}
	var result struct {
		Data []CodexThread `json:"data"`
	}
	if err := c.Call(ctx, "thread/list", params, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

func (c *CodexClient) ReadThread(ctx context.Context, threadID string) (*CodexThread, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, errors.New("codex thread id required")
	}
	var result struct {
		Thread CodexThread `json:"thread"`
	}
	if err := c.Call(ctx, "thread/read", map[string]any{
		"threadId":     threadID,
		"includeTurns": false,
	}, &result); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.Thread.ID) == "" {
		return nil, fmt.Errorf("codex thread/read returned no thread for %s", threadID)
	}
	return &result.Thread, nil
}

func (c *CodexClient) ReadThreadWithTurns(ctx context.Context, threadID string) (*CodexThread, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, errors.New("codex thread id required")
	}
	var result struct {
		Thread CodexThread `json:"thread"`
	}
	// itemsView defaults to "summary", which only returns chat-style
	// user/agent messages. "full" includes commandExecution / mcpToolCall /
	// fileChange / webSearch items so the PR chat can render tool pills.
	if err := c.Call(ctx, "thread/read", map[string]any{
		"threadId":     threadID,
		"includeTurns": true,
		"itemsView":    "full",
	}, &result); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.Thread.ID) == "" {
		return nil, fmt.Errorf("codex thread/read returned no thread for %s", threadID)
	}
	return &result.Thread, nil
}

func (c *CodexClient) StartTurn(ctx context.Context, threadID, text string) error {
	threadID = strings.TrimSpace(threadID)
	text = strings.TrimSpace(text)
	if threadID == "" {
		return errors.New("codex thread id required")
	}
	if text == "" {
		return errors.New("codex turn text required")
	}
	params := map[string]any{
		"threadId": threadID,
		"input": []map[string]any{{
			"type":          "text",
			"text":          text,
			"text_elements": []any{},
		}},
	}
	var result json.RawMessage
	return c.Call(ctx, "turn/start", params, &result)
}

func (c *CodexClient) Notify(ctx context.Context, method string, params any) error {
	if c == nil || c.conn == nil {
		return errors.New("codex app-server client is closed")
	}
	req := rpcRequest{Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal codex app-server notification %s: %w", method, err)
	}
	return c.conn.Write(ctx, websocket.MessageText, data)
}

func (c *CodexClient) Call(ctx context.Context, method string, params any, result any) error {
	if c == nil || c.conn == nil {
		return errors.New("codex app-server client is closed")
	}
	id := c.next.Add(1)
	req := rpcRequest{ID: id, Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal codex app-server request %s: %w", method, err)
	}
	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		return fmt.Errorf("write codex app-server request %s: %w", method, err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		readCtx, cancel := context.WithDeadline(ctx, deadline)
		_, msg, err := c.conn.Read(readCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("read codex app-server response for %s: %w", method, err)
		}
		var resp rpcResponse
		if err := json.Unmarshal(msg, &resp); err != nil {
			return fmt.Errorf("decode codex app-server response for %s: %w", method, err)
		}
		if !rpcIDMatches(resp.ID, id) {
			continue
		}
		if resp.Error != nil {
			return resp.Error
		}
		if result == nil {
			return nil
		}
		if len(resp.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("decode codex app-server result for %s: %w", method, err)
		}
		return nil
	}
}

func rpcIDMatches(raw json.RawMessage, want int64) bool {
	if len(raw) == 0 {
		return false
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n == want
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s == strconv.FormatInt(want, 10)
	}
	return false
}
