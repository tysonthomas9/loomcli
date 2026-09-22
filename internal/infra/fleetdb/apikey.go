package fleetdb

import (
	"context"
	"fmt"
)

// ProvisionScopedKey creates an API key for actor and grants it the given
// workspace role atomically (fleet-db's POST /api/v1/admin/apikeys with
// workspace+role), returning the raw key. ttlSeconds=0 means no expiry. The
// client's own credential must hold apikey.manage (i.e. an admin key) — this is
// how the host/lead mints a short-TTL, least-privilege key for a sandbox agent.
func (c *Client) ProvisionScopedKey(ctx context.Context, actor, workspace, role string, ttlSeconds int) (string, error) {
	body := struct {
		ActorID    string `json:"actor_id"`
		Workspace  string `json:"workspace"`
		Role       string `json:"role"`
		TTLSeconds int    `json:"ttl_seconds,omitempty"`
	}{ActorID: actor, Workspace: workspace, Role: role, TTLSeconds: ttlSeconds}

	var resp struct {
		Key string `json:"key"`
	}
	if err := c.do(ctx, "POST", "/api/v1/admin/apikeys", body, &resp); err != nil {
		return "", err
	}
	if resp.Key == "" {
		return "", fmt.Errorf("fleetdb: provision scoped key for %q: empty key in response", actor)
	}
	return resp.Key, nil
}

// RevokeKey deletes the API key(s) for actor and (server-side) the workspace
// ACL role provisioned alongside it. Idempotent: deleting an unknown actor is a
// no-op (HTTP 204).
func (c *Client) RevokeKey(ctx context.Context, actor string) error {
	return c.do(ctx, "DELETE", "/api/v1/admin/apikeys/"+pathEscape(actor), nil, nil)
}
