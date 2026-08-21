// Package leaddispatch is the sandbox-side client for serve's occupant
// dispatch mount. It never touches fleet-db, holds provider credentials, or
// names a workflow. A retry after a lost 202 can recover the ownable run id
// from serve's epic_run_active response and poll that exact run.
package leaddispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/leadoccupant"
)

const (
	dispatchPathPrefix = "/lead/dispatch"
	maxErrorBodyBytes  = 2 << 10
)

type Client struct {
	baseURL   string
	workspace string
	doer      leadoccupant.Doer
}

// New returns the dispatch client for a complete occupant environment.
func New() (*Client, error) {
	env, state := leadoccupant.FromEnv()
	switch state {
	case leadoccupant.StatePartial:
		return nil, leadoccupant.ErrIncompleteEnv
	case leadoccupant.StateComplete:
		return &Client{baseURL: env.BaseURL, workspace: env.Workspace, doer: env.Transport()}, nil
	case leadoccupant.StateAbsent:
		return nil, errors.New("occupant environment absent")
	default:
		return nil, fmt.Errorf("unknown occupant environment state %d", state)
	}
}

type EpicRunRequest struct {
	EpicID         string `json:"epicId"`
	MaxConcurrency *int   `json:"maxConcurrency,omitempty"`
	Runner         string `json:"runner,omitempty"`
}

type EpicRunDispatch struct {
	RunID    string `json:"runId"`
	Workflow string `json:"workflow"`
	EpicID   string `json:"epicId"`
	Status   string `json:"status"`
}

type RunStatus struct {
	RunID      string `json:"runId"`
	EpicID     string `json:"epicId,omitempty"`
	Status     string `json:"status"`
	Terminal   bool   `json:"terminal"`
	Summary    string `json:"summary,omitempty"`
	ErrorClass string `json:"errorClass,omitempty"`
}

type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("occupant dispatch HTTP %d (%s)", e.Status, e.Code)
}

func (e *APIError) Retryable() bool {
	return e != nil && (e.Status == http.StatusTooManyRequests ||
		e.Status == http.StatusBadGateway || e.Status == http.StatusServiceUnavailable ||
		e.Status == http.StatusGatewayTimeout)
}

func (c *Client) DispatchEpicRun(ctx context.Context, in EpicRunRequest) (EpicRunDispatch, error) {
	var out EpicRunDispatch
	if err := c.doJSON(ctx, http.MethodPost, "/epic-run", in, &out); err != nil {
		return out, err
	}
	return out, nil
}

func (c *Client) RunStatus(ctx context.Context, runID string) (RunStatus, error) {
	var out RunStatus
	path := "/runs/" + url.PathEscape(strings.TrimSpace(runID))
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return out, err
	}
	return out, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode occupant dispatch request: %w", err)
		}
	}
	endpoint := c.baseURL + "/api/workspaces/" + url.PathEscape(c.workspace) + dispatchPathPrefix + path
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("build occupant dispatch request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.doer.Do(req)
	if err != nil {
		return fmt.Errorf("call occupant dispatch API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return decodeAPIError(resp)
	}
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode occupant dispatch response: %w", err)
	}
	if !envelope.Success {
		return &APIError{Status: resp.StatusCode, Code: "invalid_envelope", Message: "occupant dispatch response reported failure"}
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("decode occupant dispatch data: %w", err)
	}
	return nil
}

func decodeAPIError(resp *http.Response) error {
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes+1))
	if readErr != nil {
		return fmt.Errorf("read occupant dispatch error response: %w", readErr)
	}
	if len(raw) > maxErrorBodyBytes {
		raw = raw[:maxErrorBodyBytes]
	}
	var envelope struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	apiErr := &APIError{Status: resp.StatusCode}
	if err := json.Unmarshal(raw, &envelope); err == nil {
		apiErr.Code = envelope.Code
		apiErr.Message = envelope.Error
	} else {
		apiErr.Code = "http_" + strconv.Itoa(resp.StatusCode)
		apiErr.Message = strings.TrimSpace(string(raw))
	}
	if resp.StatusCode == http.StatusUnauthorized &&
		(apiErr.Code == "unauthenticated" || apiErr.Code == "token_expired") {
		apiErr.Message = leadoccupant.UnauthorizedMessage
	}
	return apiErr
}
