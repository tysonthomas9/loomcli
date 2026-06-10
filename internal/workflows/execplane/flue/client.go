// Package flue implements execplane.ExecutionPlane over Flue's HTTP
// protocol: POST /agents/{name}/{id} with Accept: text/event-stream
// returns an SSE stream of agent events ending in `idle` or `error`.
// No Flue SDK is needed — the wire protocol is plain HTTP + SSE.
package flue

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/workflows/execplane"
)

// Config holds connection parameters for a Flue server.
type Config struct {
	// BaseURL is the Flue server root, e.g. "http://127.0.0.1:3583".
	BaseURL string
	// HTTPClient optionally overrides the transport. The default has no
	// overall timeout — invocations are long-lived streams bounded by
	// the caller's context.
	HTTPClient *http.Client
	// HealthPath is the health endpoint probed by Healthy. Defaults to
	// /healthz (added by the loom app.ts template).
	HealthPath string
}

// Client is an HTTP/SSE Flue client. Implements
// execplane.ExecutionPlane.
type Client struct {
	baseURL    string
	http       *http.Client
	healthPath string
}

// New constructs a Flue client.
func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("flue: BaseURL required")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{} // streams must not be cut by a client timeout
	}
	healthPath := cfg.HealthPath
	if healthPath == "" {
		healthPath = "/healthz"
	}
	return &Client{
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		http:       httpClient,
		healthPath: healthPath,
	}, nil
}

var _ execplane.ExecutionPlane = (*Client)(nil)

// Healthy probes the health endpoint.
func (c *Client) Healthy(ctx context.Context) error {
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, c.baseURL+c.healthPath, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("flue: health probe: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("flue: health probe: HTTP %d", resp.StatusCode)
	}
	return nil
}

// Invoke starts a streamed invocation of agent instance
// <agent>/<instanceID>.
func (c *Client) Invoke(ctx context.Context, agent, instanceID string, in execplane.InvokeRequest) (execplane.StreamHandle, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("flue: marshal invoke request: %w", err)
	}
	u := c.baseURL + "/agents/" + url.PathEscape(agent) + "/" + url.PathEscape(instanceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("flue: build invoke request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("flue: invoke %s/%s: %w", agent, instanceID, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, fmt.Errorf("flue: invoke %s/%s: HTTP %d: %s", agent, instanceID, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	s := &stream{
		body:   resp.Body,
		events: make(chan execplane.Event, 16),
	}
	go s.read()
	return s, nil
}

// stream reads SSE frames off the response body.
type stream struct {
	body   io.ReadCloser
	events chan execplane.Event

	cancelOnce sync.Once
	cancelled  bool

	mu  sync.Mutex
	err error
}

var _ execplane.StreamHandle = (*stream)(nil)

func (s *stream) Events() <-chan execplane.Event { return s.events }

func (s *stream) Cancel() {
	s.cancelOnce.Do(func() {
		s.cancelled = true
		_ = s.body.Close()
	})
}

func (s *stream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *stream) setErr(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

// read parses SSE frames until a terminal event, EOF, or read error.
// SSE format per frame: optional "event: <type>", optional "id: <n>",
// one or more "data: <chunk>" lines, blank-line terminator. Comment
// lines (": heartbeat") are skipped.
func (s *stream) read() {
	defer close(s.events)
	defer s.Cancel()

	scanner := bufio.NewScanner(s.body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)

	var eventType string
	var data []byte
	flush := func() bool {
		if eventType == "" && len(data) == 0 {
			return false
		}
		ev := execplane.Event{Type: eventType, Data: json.RawMessage(data)}
		if ev.Type == "" {
			// Some SSE producers omit event: and put type in the payload.
			var probe struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(data, &probe)
			ev.Type = probe.Type
		}
		eventType = ""
		data = nil
		s.events <- ev
		return ev.IsTerminal()
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if flush() {
				return
			}
		case strings.HasPrefix(line, ":"):
			// heartbeat comment
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			chunk := strings.TrimPrefix(line, "data:")
			chunk = strings.TrimPrefix(chunk, " ")
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, chunk...)
		case strings.HasPrefix(line, "id:"):
			// event index — unused, ordering is the channel's
		}
	}
	// Stream ended without a blank-line flush — emit any buffered frame.
	if eventType != "" || len(data) > 0 {
		if flush() {
			return
		}
	}
	if err := scanner.Err(); err != nil && !s.cancelled {
		s.setErr(fmt.Errorf("flue: stream read: %w", err))
	}
}
