package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// BrowserDelegationHeader carries the short-lived signed browser delegation.
// It is deliberately separate from the client's API key / actor credentials:
// ordinary FleetDB authentication identifies the Loom server, while the
// delegation alone names the browser owner.
const BrowserDelegationHeader = "X-Browser-Delegation"

// BrowserClient is the typed FleetDB adapter for durable browser identities.
// Every call takes the delegation explicitly so it is never stored on the
// client or in any record.
type BrowserClient struct {
	client *Client
}

// Browsers returns the browser adapter. It is not part of store.Store: only
// the browser service, which holds the delegation signer, may call it.
func (c *Client) Browsers() *BrowserClient {
	return &BrowserClient{client: c}
}

// Create creates (or idempotently replays) a browser for the delegation's owner.
func (b *BrowserClient) Create(ctx context.Context, ws, delegation string, req domain.BrowserCreate) (*domain.Browser, error) {
	var out domain.Browser
	if err := b.call(ctx, http.MethodPost, "/api/v1/"+pathEscape(ws)+"/browsers", delegation, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// List returns every browser owned by the delegation's owner. Never nil.
func (b *BrowserClient) List(ctx context.Context, ws, delegation string) ([]domain.Browser, error) {
	var out []domain.Browser
	if err := b.call(ctx, http.MethodGet, "/api/v1/"+pathEscape(ws)+"/browsers", delegation, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []domain.Browser{}
	}
	return out, nil
}

// Get returns one browser owned by the delegation's owner.
func (b *BrowserClient) Get(ctx context.Context, ws, delegation, id string) (*domain.Browser, error) {
	var out domain.Browser
	if err := b.call(ctx, http.MethodGet, "/api/v1/"+pathEscape(ws)+"/browsers/"+pathEscape(id), delegation, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Select makes id the owner's selected browser and returns it.
func (b *BrowserClient) Select(ctx context.Context, ws, delegation, id string) (*domain.Browser, error) {
	var out domain.Browser
	path := "/api/v1/" + pathEscape(ws) + "/browsers/" + pathEscape(id) + "/select"
	if err := b.call(ctx, http.MethodPost, path, delegation, struct{}{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (b *BrowserClient) call(ctx context.Context, method, path, delegation string, body, out any) error {
	if b == nil || b.client == nil {
		return fmt.Errorf("fleetdb browsers: %w", domain.ErrBrowserUnavailable)
	}
	if strings.TrimSpace(delegation) == "" {
		return fmt.Errorf("fleetdb browsers: missing delegation: %w", domain.ErrBrowserUnauthorized)
	}
	status, _, err := b.client.doWithResponse(ctx, method, path, body, out, map[string]string{BrowserDelegationHeader: delegation})
	if err == nil {
		return nil
	}
	return classifyBrowserError(status, err)
}

// classifyBrowserError maps FleetDB browser responses onto browser sentinels.
// The generic classifier folds 401 into ErrConflict and 503 into a bare error;
// the browser surface needs them distinct so the UI can say "sign in again"
// versus "FleetDB unavailable" instead of inventing state.
func classifyBrowserError(status int, err error) error {
	switch {
	case status == http.StatusUnauthorized:
		return fmt.Errorf("%w: %w", domain.ErrBrowserUnauthorized, err)
	case status == http.StatusForbidden:
		return fmt.Errorf("%w: %w", domain.ErrBrowserForbidden, err)
	case status == http.StatusConflict:
		return fmt.Errorf("%w: %w", domain.ErrBrowserRequestConflict, err)
	case status == http.StatusNotFound, status == http.StatusBadRequest:
		return err // already wraps domain.ErrNotFound / domain.ErrInvalid
	case status == 0, status >= 500:
		return fmt.Errorf("%w: %w", domain.ErrBrowserUnavailable, err)
	}
	if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrInvalid) {
		return err
	}
	return fmt.Errorf("%w: %w", domain.ErrBrowserUnavailable, err)
}
