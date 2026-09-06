package fleet

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/fleethttp"
)

// Idempotent-create transport support. The X-Idempotency-Key /
// X-Idempotency-Force values travel as request headers — fleet-db's strict
// JSON decode rejects unknown body fields — and the replay / soft-duplicate
// signals come back as response headers, which the backend contract
// (IssueData only) cannot carry, so they are surfaced via logs here.

// doRequestHeaders is doRequest with extra request headers and the response
// headers surfaced to the caller.
func (b *FleetBackend) doRequestHeaders(ctx context.Context, method, path string, body interface{}, headers map[string]string) (*apiResponse, int, http.Header, error) {
	b.mu.RLock()
	auth := fleethttp.Auth{BearerToken: b.authToken, APIKey: b.apiKey, Actor: b.actor}
	b.mu.RUnlock()

	req, err := fleethttp.BuildJSONRequest(ctx, method, b.baseWorkspaceURL+path, auth, body)
	if err != nil {
		return nil, 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, resp.StatusCode, resp.Header, fmt.Errorf("read response body: %w", err)
	}

	apiResp, err := parseFleetResponse(respBody, resp.StatusCode)
	if err != nil {
		return nil, resp.StatusCode, resp.Header, err
	}
	captureRetryAfter(apiResp, resp.Header)
	return apiResp, resp.StatusCode, resp.Header, nil
}

// logIdempotencyResponse surfaces replay / soft-duplicate response headers in
// the log (visible in serve logs) — dedup metadata would otherwise be
// invisible because the backend contract returns only IssueData.
func logIdempotencyResponse(h http.Header, issueID string) {
	if h.Get("X-Idempotency-Replayed") == "true" {
		slog.Info("idempotent create replayed existing issue", "issue", issueID)
	}
	if warn := h.Get("X-Idempotency-Warning"); warn != "" {
		slog.Info("create returned existing issue", "warning", warn, "issue", issueID)
	}
}
