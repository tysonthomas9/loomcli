// Package opencode is the OpenCode v2 harness adapter (design v2 §8.1.4).
// This file holds the HTTP framing, error translation and derived ids only;
// waiting messages, asks and recovery decisions stay in loomagent.
package opencode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// Client talks to one running OpenCode server. The endpoint can change when
// a supervising Adapter restarts the server.
type Client struct {
	mu       sync.RWMutex
	base     string // e.g. http://127.0.0.1:4096
	password string
	http     *http.Client
	ready    func(context.Context) error // starts a supervised server on first use; nil for a fixed one
	shellEnv func() ([]string, error)    // environment for session shell commands; nil leaves OpenCode's default
	presets  string                      // the worktrees root whose .opencode/agent holds Loom's presets; "" refuses preset sessions
	defined  func(agent string) bool     // whether Loom currently defines the loom-* agent; nil skips the check

	rulesMu sync.Mutex
	rules   map[string][]map[string]string // native session id -> the rules Loom last installed
	roots   map[string]string              // native session id -> the Root Loom opened or resumed it with

	opening sync.Map // native session id -> *sync.Mutex held by an Open for that id
}

// remember records the Root of a session Loom opened or resumed, for the feed.
func (c *Client) remember(ref loomharness.NativeRef) {
	c.rulesMu.Lock()
	defer c.rulesMu.Unlock()
	c.roots[ref.NativeID] = ref.Root
}

// rootOf is the recorded Root of a session, or "".
func (c *Client) rootOf(nativeID string) string {
	c.rulesMu.Lock()
	defer c.rulesMu.Unlock()
	return c.roots[nativeID]
}

// NewClient returns a client for the server at base with the per-boot password.
func NewClient(base, password string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), password: password, http: &http.Client{}, rules: map[string][]map[string]string{}, roots: map[string]string{}}
}

func (c *Client) setEndpoint(base, password string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.base, c.password = base, password
}

func (c *Client) endpoint() (string, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.base, c.password
}

// SessionID derives the native session id from the stable OpenSpec.Key.
func SessionID(key string) string { return "ses_" + digest(key) }

// PromptID derives the native prompt id from the AgentID and the RequestID.
func PromptID(agentID, requestID string) string { return "msg_" + digest(agentID+"\x00"+requestID) }

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:13])
}

// Error is a translated OpenCode error response. Code is the adapter code
// from design v2 §8.1.4: harness_down, auth_failed, ask_missing,
// session_missing, input_id_conflict or bad_request.
type Error struct {
	Status  int
	Code    string
	Tag     string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("opencode: %s (%d %s): %s", e.Code, e.Status, e.Tag, e.Message)
}

// Unwrap lets callers match the port sentinels with errors.Is.
func (e *Error) Unwrap() error {
	switch e.Code {
	case "harness_down", "auth_failed":
		return loomharness.ErrUnavailable
	case "session_missing":
		return loomharness.ErrSessionNotFound
	}
	return nil
}

func translate(status int, body []byte) error {
	var b struct {
		Tag     string `json:"_tag"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &b)
	e := &Error{Status: status, Tag: b.Tag, Message: b.Message}
	switch {
	case status >= 500:
		e.Code = "harness_down"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		e.Code = "auth_failed"
	case b.Tag == "PermissionNotFoundError", b.Tag == "FormNotFoundError", b.Tag == "FormAlreadySettledError":
		e.Code = "ask_missing"
	case b.Tag == "SessionNotFoundError" || status == http.StatusNotFound:
		e.Code = "session_missing"
	case status == http.StatusConflict:
		e.Code = "input_id_conflict"
	default:
		e.Code = "bad_request"
	}
	return e
}

// call sends one JSON request and decodes the response's "data" into out.
func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	resp, err := c.do(ctx, method, path, in)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("opencode %s %s: %w: %w", method, path, loomharness.ErrUnavailable, err)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("opencode %s %s: %w", method, path, translate(resp.StatusCode, body))
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("opencode %s %s: decode: %w", method, path, err)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, in any) (*http.Response, error) {
	if c.ready != nil {
		if err := c.ready(ctx); err != nil {
			return nil, fmt.Errorf("opencode %s %s: %w", method, path, err)
		}
	}
	base, password := c.endpoint()
	var rd io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", password)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("opencode %s %s: %w: %w", method, path, loomharness.ErrUnavailable, err)
	}
	return resp, nil
}

func isCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}
