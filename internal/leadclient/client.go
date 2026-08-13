// Package leadclient provides the sandbox-side client for serve's lead API.
package leadclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadoccupant"
	"github.com/tysonthomas9/loomcli/internal/netbase"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const defaultHTTPTimeout = 10 * time.Second

var (
	ErrCapabilityDenied = errors.New("leadclient: capability denied")
	ErrAuth             = errors.New("leadclient: auth failed")
	ErrRetryable        = errors.New("leadclient: retryable")
	ErrUnsupported      = errors.New("leadclient: unsupported in sandbox")
)

// Config wires a Client to serve's occupant-authenticated lead API.
type Config struct {
	BaseURL       string
	WorkspaceKey  string
	OccupantToken string //nolint:gosec // occupant bearer credential intentionally held by the client.
	HTTPClient    *http.Client
	PersistToken  func(string) error
}

// Client is a placement-scoped partial store.Store backed by serve's lead API.
type Client struct {
	baseURL      string
	workspaceKey string
	httpClient   *http.Client

	tokenMu       sync.RWMutex
	occupantToken string //nolint:gosec // guarded bearer credential, never logged.
	persistToken  func(string) error

	agentSessions store.AgentSessionStore
	agents        store.AgentStore
	inboxMessages store.AgentInboxMessageStore
}

// New constructs a placement-scoped lead API client.
func New(cfg Config) (*Client, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	workspaceKey := strings.TrimSpace(cfg.WorkspaceKey)
	token := strings.TrimSpace(cfg.OccupantToken)
	if baseURL == "" {
		return nil, fmt.Errorf("lead API base URL required: %w", domain.ErrInvalid)
	}
	if workspaceKey == "" {
		return nil, fmt.Errorf("lead API workspace key required: %w", domain.ErrInvalid)
	}
	if token == "" {
		return nil, fmt.Errorf("lead API occupant token required: %w", domain.ErrInvalid)
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout:   defaultHTTPTimeout,
			Transport: netbase.Transport(),
		}
	}
	c := &Client{
		baseURL:       strings.TrimRight(baseURL, "/"),
		workspaceKey:  workspaceKey,
		httpClient:    httpClient,
		occupantToken: token,
		persistToken:  cfg.PersistToken,
	}
	c.agentSessions = agentSessionStore{client: c}
	c.agents = agentStore{client: c}
	c.inboxMessages = agentInboxMessageStore{client: c}
	return c, nil
}

func (c *Client) do(ctx context.Context, op string, reqBody any, out any) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal lead op %s request: %w: %w", op, err, domain.ErrInvalid)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.opURL(op), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("construct lead op %s request: %w", op, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.currentToken())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("lead op %s transport: %w: %w", op, err, ErrRetryable)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read lead op %s response: %w: %w", op, err, ErrRetryable)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return mapOpError(op, resp.StatusCode, respBody)
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode lead op %s response: %w: %w", op, err, domain.ErrInvalid)
		}
	}
	c.rotateTokenFromResponse(respBody)
	return nil
}

func (c *Client) opURL(op string) string {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return c.baseURL
	}
	base.Path = path.Join(base.Path, "api", "workspaces", c.workspaceKey, "lead", op)
	return base.String()
}

func (c *Client) currentToken() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.occupantToken
}

func (c *Client) rotateTokenFromResponse(body []byte) {
	var renewal struct {
		OccupantToken string `json:"occupantToken"`
	}
	if err := json.Unmarshal(body, &renewal); err != nil {
		return
	}
	token := strings.TrimSpace(renewal.OccupantToken)
	if token == "" {
		return
	}
	c.tokenMu.Lock()
	c.occupantToken = token
	c.tokenMu.Unlock()
	if c.persistToken != nil {
		if err := c.persistToken(token); err != nil {
			path, _ := leadoccupant.TokenPath()
			slog.Warn("persist rotated occupant token", "path", path, "err", err)
		}
	}
}

type opErrorEnvelope struct {
	Error opError `json:"error"`
}

type opError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func mapOpError(op string, status int, body []byte) error {
	var envelope opErrorEnvelope
	_ = json.Unmarshal(body, &envelope)
	code := strings.TrimSpace(envelope.Error.Code)
	message := strings.TrimSpace(envelope.Error.Message)
	if message == "" {
		message = http.StatusText(status)
	}
	if message == "" {
		message = fmt.Sprintf("HTTP %d", status)
	}

	switch {
	case status == http.StatusUnauthorized:
		return fmt.Errorf("lead op %s auth failed (%s): %s: %w", op, firstNonEmpty(code, "unauthorized"), message, ErrAuth)
	case code == "cap_denied":
		return fmt.Errorf("lead op %s capability denied: %s: %w", op, message, ErrCapabilityDenied)
	case code == "not_found" || status == http.StatusNotFound:
		return fmt.Errorf("lead op %s not found: %s: %w", op, message, domain.ErrNotFound)
	case code == "invalid" || status == http.StatusBadRequest:
		return fmt.Errorf("lead op %s invalid: %s: %w", op, message, domain.ErrInvalid)
	case code == "not_owner":
		return fmt.Errorf("lead op %s not owner: %s: %w", op, message, domain.ErrNotOwner)
	case status == http.StatusForbidden:
		return fmt.Errorf("lead op %s forbidden (%s): %s: %w", op, firstNonEmpty(code, "forbidden"), message, domain.ErrNotOwner)
	case status >= 500 || envelope.Error.Retryable:
		return fmt.Errorf("lead op %s retryable (%s): %s: %w", op, firstNonEmpty(code, "unavailable"), message, ErrRetryable)
	default:
		return fmt.Errorf("lead op %s failed HTTP %d (%s): %s", op, status, code, message)
	}
}

func unsupported(method string) error {
	return fmt.Errorf("%s unsupported in sandbox lead store: %w", method, ErrUnsupported)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
