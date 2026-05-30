package tsfirst

import (
	"bufio"
	"bytes"
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

func captureStreamingResponse(rc io.Reader, stream io.Writer) (localInvocationResult, error) {
	var result localInvocationResult
	var response strings.Builder
	var fallback bytes.Buffer
	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		event, ok := parseLocalStreamEvent(line)
		if !ok {
			if stream != nil {
				if _, err := fmt.Fprintln(stream, line); err != nil {
					return localInvocationResult{}, err
				}
			}
			fmt.Fprintln(&fallback, line)
			continue
		}
		if event.ProviderSessionID != "" {
			result.ProviderSessionID = event.ProviderSessionID
		}
		if event.ProviderModel != "" {
			result.ProviderModel = event.ProviderModel
		}
		if event.Usage != nil {
			result.Usage = mergeConnectUsage(result.Usage, event.Usage)
		}
		if event.Text != "" {
			if stream != nil {
				if _, err := io.WriteString(stream, event.Text); err != nil {
					return localInvocationResult{}, err
				}
			}
			response.WriteString(event.Text)
		}
		if event.Result != "" && strings.TrimSpace(response.String()) == "" {
			response.WriteString(event.Result)
		}
	}
	if err := scanner.Err(); err != nil {
		return localInvocationResult{}, err
	}
	result.Response = strings.TrimSpace(response.String())
	if result.Response == "" {
		result.Response = strings.TrimSpace(fallback.String())
	}
	return result, nil
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
