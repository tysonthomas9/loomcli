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

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// Client talks to one running OpenCode server.
type Client struct {
	base     string // e.g. http://127.0.0.1:4096
	password string
	http     *http.Client
}

// NewClient returns a client for the server at base with the per-boot password.
func NewClient(base, password string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), password: password, http: &http.Client{}}
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
// from design v2 §8.1.4: harness_down, ask_missing, session_missing,
// input_id_conflict or bad_request.
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
	case "harness_down":
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
	case b.Tag == "PermissionNotFoundError":
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
	var rd io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("opencode", c.password)
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
