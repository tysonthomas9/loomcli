package fleetdb

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"context"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/fleethttp"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// capabilitiesPath is server-scoped, not workspace-scoped: the document
// describes the binary's registered routes, which are the same for every
// workspace it serves.
const capabilitiesPath = "/api/v1/capabilities"

type capabilityStore struct{ client *Client }

var _ store.CapabilityStore = (*capabilityStore)(nil)

// Get reads GET /api/v1/capabilities.
//
// The request is issued directly rather than through Client.do because the two
// failure shapes this call must keep apart are both erased by the shared
// classifier. A 404/405 with no error envelope is the capability endpoint
// itself missing — an older fleet-db, reported as
// domain.ErrCapabilityEndpointUnsupported — while every other non-200 is an
// ordinary transport/server failure and must stay one. Collapsing the two
// would let a 503 read as "this server predates capability reporting".
func (s *capabilityStore) Get(ctx context.Context) (domain.FleetDBCapabilities, error) {
	c := s.client

	c.mu.RLock()
	auth := fleethttp.Auth{BearerToken: c.authToken, APIKey: c.apiKey, Actor: c.actor}
	c.mu.RUnlock()

	req, err := fleethttp.BuildJSONRequest(ctx, http.MethodGet, c.baseURL+capabilitiesPath, auth, nil)
	if err != nil {
		return domain.FleetDBCapabilities{}, fmt.Errorf("fleetdb: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return domain.FleetDBCapabilities{}, fmt.Errorf("fleetdb: GET %s: %w", capabilitiesPath, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return domain.FleetDBCapabilities{}, fmt.Errorf("fleetdb: GET %s: HTTP %d (read body: %w)", capabilitiesPath, resp.StatusCode, err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		return decodeCapabilities(body)
	case isMissingCapabilityRoute(resp.StatusCode, body):
		return domain.FleetDBCapabilities{}, fmt.Errorf("fleetdb: GET %s: HTTP %d: %w",
			capabilitiesPath, resp.StatusCode, domain.ErrCapabilityEndpointUnsupported)
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return domain.FleetDBCapabilities{}, fmt.Errorf("fleetdb: GET %s: unexpected HTTP %d redirect",
			capabilitiesPath, resp.StatusCode)
	default:
		return domain.FleetDBCapabilities{}, classifyHTTPError(http.MethodGet, capabilitiesPath, resp.StatusCode, body)
	}
}

// isMissingCapabilityRoute reports the one shape that means "this fleet-db
// does not know about capabilities": the route is unrouted, so the answer is a
// bare mux 404 (or a 405 from a server that routes the path but not the
// method) with no fleet-db error envelope. A 404 that *does* carry an envelope
// came from a handler and is a real error, not an old build.
func isMissingCapabilityRoute(status int, body []byte) bool {
	if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
		return false
	}
	// fleethttp.ExtractErrorMessage, not the local extractErrorMessage: the
	// latter falls back to the raw body for debuggability, which would make a
	// bare mux "404 page not found" look like an enveloped error.
	return extractErrorCode(body) == "" && fleethttp.ExtractErrorMessage(body) == ""
}

// decodeCapabilities parses a 200 body, refusing anything that does not
// actually carry a capability list.
//
// Capabilities is a pointer precisely so an absent field is distinguishable
// from an empty list: a truncated or unrelated 200 body must never decode into
// "the server advertises nothing", which would read as every route missing.
func decodeCapabilities(body []byte) (domain.FleetDBCapabilities, error) {
	var doc struct {
		APIVersion   int       `json:"api_version"`
		Commit       string    `json:"commit"`
		Capabilities *[]string `json:"capabilities"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return domain.FleetDBCapabilities{}, fmt.Errorf("fleetdb: decode capabilities (GET %s): %w", capabilitiesPath, err)
	}
	if doc.Capabilities == nil {
		return domain.FleetDBCapabilities{}, fmt.Errorf("fleetdb: GET %s: response carries no capabilities field", capabilitiesPath)
	}
	return domain.FleetDBCapabilities{
		APIVersion:   doc.APIVersion,
		Commit:       doc.Commit,
		Capabilities: *doc.Capabilities,
	}, nil
}
