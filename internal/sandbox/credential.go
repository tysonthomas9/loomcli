package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
)

// CredentialTTLSeconds bounds a provisioned sandbox credential's lifetime.
const CredentialTTLSeconds = 6 * 60 * 60 // 6 hours

// FleetDBURL returns a fleet-db URL the sandbox container can reach, or "" if
// none is configured. It prefers an explicit LOOM_SANDBOX_FLEETDB_URL (the
// operator-supplied, container-reachable address); otherwise it rewrites a
// localhost LOOM_FLEET_DB_URL to the container's host gateway so the in-container
// agent can reach the host's fleet-db. (Point LOOM_SANDBOX_FLEETDB_URL at a loom
// serve instance to route through the serve config proxy instead — 2C.)
func FleetDBURL() string {
	if v := strings.TrimSpace(os.Getenv("LOOM_SANDBOX_FLEETDB_URL")); v != "" {
		return v
	}
	host := strings.TrimSpace(os.Getenv(bootstrap.EnvFleetDBURL))
	if host == "" {
		return ""
	}
	gw := HostGateway()
	for _, lh := range []string{"localhost", "127.0.0.1", "0.0.0.0"} {
		host = strings.ReplaceAll(host, lh, gw)
	}
	return host
}

// ProvisionCredential mints a short-TTL, workspace-scoped `developer` API key for
// a unique sandbox actor (`sandbox:<workspace>:<agentID>:<ts>`) and returns the
// key, actor, and a revoke func. It runs only when the host holds an admin
// fleet-db credential (LOOM_FLEET_DB_API_KEY + LOOM_FLEET_DB_URL); otherwise it is
// a no-op — empty key/actor and a nil-safe revoke — leaving the sandbox to use the
// host config's ambient credential (e.g. a dev/auth-off fleet-db). Provisioning
// errors are fatal: a configured admin path that fails must not silently fall back
// to an over-privileged key.
func ProvisionCredential(ctx context.Context, workspaceID, agentID string) (key, actor string, revoke func(), err error) {
	adminKey := strings.TrimSpace(os.Getenv(bootstrap.EnvFleetDBAPIKey))
	hostURL := strings.TrimSpace(os.Getenv(bootstrap.EnvFleetDBURL))
	if adminKey == "" || hostURL == "" {
		return "", "", func() {}, nil
	}
	client, err := fleetdb.New(fleetdb.Config{
		BaseURL: hostURL,
		APIKey:  adminKey,
		Actor:   strings.TrimSpace(os.Getenv(bootstrap.EnvFleetDBActor)),
	})
	if err != nil {
		return "", "", nil, fmt.Errorf("sandbox credential client: %w", err)
	}
	actor = fmt.Sprintf("sandbox:%s:%s:%x", workspaceID, agentID, time.Now().UnixMilli())
	key, err = client.ProvisionScopedKey(ctx, actor, workspaceID, "developer", CredentialTTLSeconds)
	if err != nil {
		return "", "", nil, fmt.Errorf("provision sandbox developer key for %q: %w", actor, err)
	}
	slog.Info("provisioned scoped sandbox credential", "actor", actor, "workspace", workspaceID)
	revoke = func() {
		rctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if rerr := client.RevokeKey(rctx, actor); rerr != nil {
			slog.Warn("sandbox credential revoke failed", "actor", actor, "err", rerr)
		}
	}
	return key, actor, revoke, nil
}
