package tsfirst

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type trackingWriter struct {
	w     io.Writer
	count int
	last  byte
}

func (tw *trackingWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		tw.count += len(p)
		tw.last = p[len(p)-1]
	}
	return tw.w.Write(p)
}

func ensureStreamLineBreak(tw *trackingWriter) error {
	if tw == nil {
		return nil
	}
	if tw.count > 0 && tw.last == '\n' {
		return nil
	}
	_, err := fmt.Fprintln(tw.w)
	return err
}

func printInteractiveConnectHeader(out io.Writer, result connectResult) error {
	if _, err := fmt.Fprintf(out, "Connected to %s instance %s session %s (backend=%s model=%s)\n", result.Agent, result.Instance, result.Session, fallback(result.Backend, "default"), fallback(result.Model, "default")); err != nil {
		return err
	}
	if len(result.Env) > 0 {
		if _, err := fmt.Fprintf(out, "Env allowlist: %s\n", strings.Join(result.Env, ", ")); err != nil {
			return err
		}
	}
	if result.ToolRuntime != nil && len(result.ToolRuntime.TypedTools) > 0 {
		if _, err := fmt.Fprintf(out, "Typed tool runtime: %s (%s)\n", result.ToolRuntime.Status, typedToolNames(result.ToolRuntime)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out, "Enter one prompt per line. Ctrl-D, /exit, or /quit ends the session.")
	return err
}

func captureStreamingResponse(ctx context.Context, rc io.Reader, stream io.Writer, typedToolLineBackend any) (localInvocationResult, error) {
	capture := &streamResponseCapture{ctx: ctx, stream: stream, typedToolLineBackend: typedToolLineBackend}
	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		if err := capture.ingest(scanner.Text()); err != nil {
			return localInvocationResult{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return localInvocationResult{}, err
	}
	return capture.result(), nil
}

type streamResponseCapture struct {
	ctx                  context.Context
	invocation           localInvocationResult
	metadata             providerMetadataCollector
	response             strings.Builder
	fallback             bytes.Buffer
	stream               io.Writer
	typedToolLineBackend any
}

func (c *streamResponseCapture) ingest(line string) error {
	ingestBackendTypedToolProviderLine(c.ctx, c.typedToolLineBackend, line)
	c.metadata.ingestLine(line)
	event, ok := parseLocalStreamEvent(line)
	if !ok {
		if c.stream != nil {
			if _, err := fmt.Fprintln(c.stream, line); err != nil {
				return err
			}
		}
		fmt.Fprintln(&c.fallback, line)
		return nil
	}
	return c.ingestEvent(event)
}

func (c *streamResponseCapture) ingestEvent(event localStreamEvent) error {
	if event.ProviderSessionID != "" {
		c.invocation.ProviderSessionID = event.ProviderSessionID
	}
	if event.ProviderModel != "" {
		c.invocation.ProviderModel = event.ProviderModel
	}
	if event.Usage != nil {
		c.invocation.Usage = mergeConnectUsage(c.invocation.Usage, event.Usage)
	}
	if event.Text != "" {
		text := stripExplicitTypedToolCallText(event.Text)
		if text == "" {
			return nil
		}
		if c.stream != nil {
			if _, err := io.WriteString(c.stream, text); err != nil {
				return err
			}
		}
		c.response.WriteString(text)
	}
	if event.Result != "" && strings.TrimSpace(c.response.String()) == "" {
		c.response.WriteString(event.Result)
	}
	return nil
}

func stripExplicitTypedToolCallText(text string) string {
	if !strings.Contains(text, "loom.typed_tool") && !strings.Contains(text, "typed_tool.call") {
		return text
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "```") {
			continue
		}
		if lineIsExplicitTypedToolCall(trimmed) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func lineIsExplicitTypedToolCall(line string) bool {
	if !strings.Contains(line, "loom.typed_tool") && !strings.Contains(line, "typed_tool.call") {
		return false
	}
	if strings.HasPrefix(line, "{") || strings.HasPrefix(line, "[") {
		return true
	}
	if start := strings.Index(line, "{"); start >= 0 {
		return strings.LastIndex(line, "}") > start
	}
	return false
}

func (c *streamResponseCapture) result() localInvocationResult {
	result := c.invocation
	result.Response = strings.TrimSpace(c.response.String())
	if result.Response == "" {
		result.Response = strings.TrimSpace(c.fallback.String())
	}
	result.ProviderMetadata = c.metadata.metadata()
	if result.ProviderSessionID == "" {
		result.ProviderSessionID = c.metadata.sessionID
	}
	if result.ProviderModel == "" {
		result.ProviderModel = c.metadata.providerModel
	}
	return result
}

type localStreamEvent struct {
	Text              string
	Result            string
	ProviderSessionID string
	ProviderModel     string
	Usage             *connectUsage
}

func parseLocalStreamEvent(line string) (localStreamEvent, bool) {
	var event struct {
		Type      string         `json:"type"`
		Subtype   string         `json:"subtype"`
		SessionID string         `json:"session_id"`
		Result    string         `json:"result"`
		Message   *streamMessage `json:"message"`
		Usage     *connectUsage  `json:"usage"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return localStreamEvent{}, false
	}
	out := localStreamEvent{
		Result:            event.Result,
		ProviderSessionID: event.SessionID,
		Usage:             event.Usage,
	}
	if event.Message != nil {
		out.ProviderModel = event.Message.Model
		if event.Message.Usage != nil {
			out.Usage = mergeConnectUsage(out.Usage, event.Message.Usage)
		}
		for _, block := range event.Message.Content {
			if block.Type == "text" {
				out.Text += block.Text
			}
		}
	}
	return out, true
}

type streamMessage struct {
	Model   string          `json:"model"`
	Content []streamContent `json:"content"`
	Usage   *connectUsage   `json:"usage"`
}

type streamContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func mergeConnectUsage(current, next *connectUsage) *connectUsage {
	if next == nil {
		return current
	}
	if current == nil {
		clone := *next
		clone.TotalTokens = connectUsageTotal(&clone)
		return &clone
	}
	if next.InputTokens != 0 {
		current.InputTokens = next.InputTokens
	}
	if next.OutputTokens != 0 {
		current.OutputTokens = next.OutputTokens
	}
	if next.CacheReadInputTokens != 0 {
		current.CacheReadInputTokens = next.CacheReadInputTokens
	}
	if next.CacheCreationInputTokens != 0 {
		current.CacheCreationInputTokens = next.CacheCreationInputTokens
	}
	if next.TotalTokens != 0 {
		current.TotalTokens = next.TotalTokens
	} else {
		current.TotalTokens = connectUsageTotal(current)
	}
	return current
}

func connectUsageTotal(u *connectUsage) int64 {
	if u == nil {
		return 0
	}
	return u.InputTokens + u.OutputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
}
