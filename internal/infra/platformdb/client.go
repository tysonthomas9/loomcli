// Package platformdb implements platform.Store as an HTTP client
// against fleet-db's platform API (/api/v1/{ws}/drivers, driver-runs,
// task-runs, action-ledger, events/mutations).
//
// It mirrors internal/infra/fleetdb's transport conventions (fleethttp
// headers, error classification to domain sentinels, body draining)
// but stays a separate client: the platform API has its own lifecycle
// semantics, and Phase 4 (cloud mode) scopes credentials per run —
// the broker will mint a run-scoped token and SetAuthToken it onto a
// per-run client, which the shared fleetdb.Client cannot express.
package platformdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/fleethttp"
	"github.com/tysonthomas9/loomcli/internal/workflows/platform"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// maxResponseBody caps response reads (mirrors fleetdb client).
const maxResponseBody = 16 << 20

// Config holds connection parameters.
type Config struct {
	// BaseURL is the fleet-db base URL. Required.
	BaseURL string
	// APIKey is sent as X-Fleet-API-Key. Optional in dev mode.
	APIKey string
	// Actor is sent as X-Actor on every request.
	Actor string
	// AuthToken is a JWT bearer for production auth.
	AuthToken string
	// HTTPClient is an optional override.
	HTTPClient *http.Client
}

// Client is the fleet-db platform HTTP client. Implements
// platform.Store.
type Client struct {
	baseURL string
	http    *http.Client

	mu        sync.RWMutex
	apiKey    string
	actor     string
	authToken string
}

// New constructs a platform client.
func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("platformdb: BaseURL required")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		}
	}
	return &Client{
		baseURL:   strings.TrimRight(cfg.BaseURL, "/"),
		http:      httpClient,
		apiKey:    cfg.APIKey,
		actor:     cfg.Actor,
		authToken: cfg.AuthToken,
	}, nil
}

var _ platform.Store = (*Client)(nil)

func (c *Client) Drivers() platform.DriverStore            { return &driverStore{c} }
func (c *Client) DriverRuns() platform.DriverRunStore      { return &driverRunStore{c} }
func (c *Client) TaskRuns() platform.TaskRunStore          { return &taskRunStore{c} }
func (c *Client) ActionLedger() platform.ActionLedgerStore { return &ledgerStore{c} }
func (c *Client) Events() platform.EventStore              { return &eventStore{c} }

// SetAuthToken updates the bearer token. Safe for concurrent use.
func (c *Client) SetAuthToken(token string) {
	c.mu.Lock()
	c.authToken = token
	c.mu.Unlock()
}

func pathEscape(s string) string { return url.PathEscape(s) }

func wsPath(ws, rest string) string {
	return "/api/v1/" + pathEscape(ws) + "/" + rest
}

// do executes a request, decoding the JSON response into out when
// non-nil. HTTP errors map to domain sentinels:
// 404→ErrNotFound, 409→ErrAlreadyExists or ErrConflict (by code),
// 400/422→ErrInvalid, 403→ErrConflict, 410→ErrGone.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	c.mu.RLock()
	auth := fleethttp.Auth{BearerToken: c.authToken, APIKey: c.apiKey, Actor: c.actor}
	c.mu.RUnlock()

	req, err := fleethttp.BuildJSONRequest(ctx, method, c.baseURL+path, auth, body)
	if err != nil {
		return fmt.Errorf("platformdb: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("platformdb: %s %s: %w", method, path, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode >= 400 {
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		if readErr != nil {
			return fmt.Errorf("platformdb: %s %s: HTTP %d (read body: %w)", method, path, resp.StatusCode, readErr)
		}
		return classifyHTTPError(method, path, resp.StatusCode, respBody)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("platformdb: decode response (%s %s): %w", method, path, err)
	}
	return nil
}

func classifyHTTPError(method, path string, status int, body []byte) error {
	msg := fleethttp.ExtractErrorMessage(body)
	prefix := fmt.Sprintf("platformdb: %s %s: HTTP %d", method, path, status)
	if msg != "" {
		prefix += ": " + msg
	}
	var sentinel error
	switch status {
	case http.StatusNotFound:
		sentinel = domain.ErrNotFound
	case http.StatusConflict:
		// fleet-db uses 409 for already_exists, already_claimed, and
		// invalid_transition. Distinguish creation dedupe from lifecycle
		// conflicts by the error code when present.
		if strings.Contains(string(body), "already_exists") {
			sentinel = domain.ErrAlreadyExists
		} else {
			sentinel = domain.ErrConflict
		}
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		sentinel = domain.ErrInvalid
	case http.StatusForbidden:
		sentinel = domain.ErrConflict
	case http.StatusGone:
		sentinel = domain.ErrGone
	default:
		return errors.New(prefix)
	}
	return fmt.Errorf("%s: %w", prefix, sentinel)
}
