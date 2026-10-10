package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/agentsv1"
)

// SubscribeRequest names the agents to follow. After holds an agent's last
// seen seq (0: all its history); an agent without one starts after its
// current last event. Types filters saved kinds (empty: all); Deltas adds
// live text deltas. feed.gap notices always come.
type SubscribeRequest struct {
	Agents []string
	After  map[string]int64
	Types  []string
	Deltas bool
}

// Stream reads one Agent API event stream. It keeps each agent's last seq
// and, when the connection drops or the server ends it with
// subscriber_lagged, reconnects from those cursors with a fresh token, so
// no saved event is lost or repeated.
type Stream struct {
	c      *Client
	ctx    context.Context
	cancel context.CancelFunc
	req    SubscribeRequest
	body   io.ReadCloser
	rd     *bufio.Reader
}

// Subscribe opens the stream. An agent without a cursor gets one first: the
// seq of its current last event, so a reconnect never skips its events.
func (c *Client) Subscribe(ctx context.Context, req SubscribeRequest) (*Stream, error) {
	after := map[string]int64{}
	for _, id := range req.Agents {
		n, ok := req.After[id]
		if !ok {
			p, err := c.ListEvents(ctx, loomstore.EventQuery{AgentID: id, After: math.MaxInt64, Limit: 1})
			if err != nil {
				return nil, err
			}
			n = p.SnapshotSeq
		}
		after[id] = n
	}
	req.After = after
	s := &Stream{c: c, req: req}
	s.ctx, s.cancel = context.WithCancel(ctx)
	if err := s.connect(); err != nil {
		s.cancel()
		return nil, err
	}
	return s, nil
}

// Close ends the stream.
func (s *Stream) Close() error {
	s.cancel()
	return s.body.Close()
}

// Next returns the next event: a saved event (Seq > 0) or a live-only
// notice (Seq 0: delta, feed.gap). A stream error other than
// subscriber_lagged is returned as a *loomagent.Error.
func (s *Stream) Next() (agentsv1.Event, error) {
	for {
		name, data, err := s.frame()
		if err != nil {
			if s.ctx.Err() != nil {
				return agentsv1.Event{}, s.ctx.Err()
			}
			if err = s.reconnect(); err != nil {
				return agentsv1.Event{}, err
			}
			continue
		}
		if name == agentsv1.StreamError {
			e := decodeError(http.StatusInternalServerError, []byte(data))
			var ae *loomagent.Error
			if !errors.As(e, &ae) || ae.Code != loomagent.CodeSubscriberLagged {
				return agentsv1.Event{}, e
			}
			if err = s.reconnect(); err != nil {
				return agentsv1.Event{}, err
			}
			continue
		}
		var ev agentsv1.Event
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return ev, err
		}
		if ev.Seq > 0 {
			if ev.Seq <= s.req.After[ev.AgentID] {
				continue
			}
			s.req.After[ev.AgentID] = ev.Seq
		}
		return ev, nil
	}
}

func (s *Stream) reconnect() error {
	_ = s.body.Close()
	return s.connect()
}

// connect gets a one-time token, unless the server runs in open mode, and
// opens the stream from the current cursors.
func (s *Stream) connect() error {
	var tok struct {
		Token string `json:"token"`
	}
	if err := s.c.doURL(s.ctx, http.MethodGet, strings.TrimSuffix(s.c.base, "v1/")+"events/token", "", nil, &tok); err != nil {
		return err
	}
	q := url.Values{"agents": {strings.Join(s.req.Agents, ",")}}
	var after []string
	for id, n := range s.req.After {
		after = append(after, id+":"+strconv.FormatInt(n, 10))
	}
	q.Set("after", strings.Join(after, ","))
	if len(s.req.Types) > 0 {
		q.Set("types", strings.Join(s.req.Types, ","))
	}
	if s.req.Deltas {
		q.Set("deltas", "true")
	}
	if tok.Token != "" {
		q.Set("token", tok.Token)
	}
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, s.c.base+"events?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := s.c.http.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		return decodeError(resp.StatusCode, raw)
	}
	s.body, s.rd = resp.Body, bufio.NewReader(resp.Body)
	return nil
}

// frame reads one event by the SSE field rules: a line is split at its first
// colon and one leading space is dropped from the value; a line starting
// with a colon is a comment; unknown fields are ignored; a blank line ends
// an event that has data.
func (s *Stream) frame() (name, data string, err error) {
	var lines []string
	for {
		line, err := s.rd.ReadString('\n')
		if err != nil {
			return "", "", err
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			if lines != nil {
				return name, strings.Join(lines, "\n"), nil
			}
			name = ""
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			name = value
		case "data":
			lines = append(lines, value)
		}
	}
}
