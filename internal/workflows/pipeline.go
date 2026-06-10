package workflows

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/workflows/execplane"
)

// lastTextCap bounds the tail of agent text kept for the run summary.
const lastTextCap = 2048

// Usage aggregates token usage reported by Flue turn events.
type Usage struct {
	InputTokens  int64
	OutputTokens int64
}

// CaptureResult summarizes one consumed invocation stream. fleet-db
// has no run-log append API, so this aggregate (not an event log) is
// what Loom persists on the DriverRun at finish; the full-fidelity
// stream goes to the structured log and any OnEvent sink.
type CaptureResult struct {
	Events       int
	ToolCalls    int
	TextBytes    int
	LastText     string
	Usage        Usage
	Terminal     bool   // a terminal frame (idle/error) was seen
	ErrorMessage string // set when the terminal frame was an error
	StreamErr    error  // transport/context error, if the stream broke
}

// CaptureOptions configures CaptureStream.
type CaptureOptions struct {
	// Logger receives one debug line per event and warn lines for
	// errors. Defaults to slog.Default().
	Logger *slog.Logger
	// OnEvent, when set, observes every event (e.g. the daemon
	// publishing live-tail notifications). Must not block.
	OnEvent func(execplane.Event)
}

// CaptureStream drains an invocation stream, aggregating usage and
// output until the stream closes or ctx is cancelled (which cancels
// the stream). It always returns a result; inspect Terminal/StreamErr
// to classify how the stream ended.
func CaptureStream(ctx context.Context, h execplane.StreamHandle, opts CaptureOptions) CaptureResult {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	var res CaptureResult
	var textTail strings.Builder
	finish := func() CaptureResult {
		tail := textTail.String()
		if len(tail) > lastTextCap {
			tail = tail[len(tail)-lastTextCap:]
		}
		res.LastText = tail
		return res
	}

	events := h.Events()
	for {
		select {
		case <-ctx.Done():
			h.Cancel()
			for range events { //nolint:revive // drain so the reader goroutine exits
			}
			res.StreamErr = ctx.Err()
			return finish()
		case e, ok := <-events:
			if !ok {
				if err := h.Err(); err != nil {
					res.StreamErr = err
				}
				return finish()
			}
			res.Events++
			if opts.OnEvent != nil {
				opts.OnEvent(e)
			}
			switch e.Type {
			case "text_delta":
				var body struct {
					Text string `json:"text"`
				}
				_ = json.Unmarshal(e.Data, &body)
				res.TextBytes += len(body.Text)
				textTail.WriteString(body.Text)
				if textTail.Len() > 2*lastTextCap {
					s := textTail.String()
					textTail.Reset()
					textTail.WriteString(s[len(s)-lastTextCap:])
				}
			case "tool_call":
				res.ToolCalls++
				logger.Debug("flue tool call", "data", string(e.Data))
			case "turn":
				// pi-ai turn usage: {"usage":{"input":N,"output":N,...}}.
				var body struct {
					Usage struct {
						Input  int64 `json:"input"`
						Output int64 `json:"output"`
					} `json:"usage"`
				}
				_ = json.Unmarshal(e.Data, &body)
				res.Usage.InputTokens += body.Usage.Input
				res.Usage.OutputTokens += body.Usage.Output
			case execplane.EventError:
				res.Terminal = true
				res.ErrorMessage = e.ErrorMessage()
				logger.Warn("flue invocation error", "message", res.ErrorMessage)
			case execplane.EventIdle:
				res.Terminal = true
			default:
				logger.Debug("flue event", "type", e.Type)
			}
		}
	}
}
