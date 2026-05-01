package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// EnvFleetDBAPIKey is the env var holding the X-Fleet-API-Key value
// for fleet-db requests. Optional in dev mode.
const EnvFleetDBAPIKey = "LOOM_FLEET_DB_API_KEY" //nolint:gosec // env var name, not a credential

// EnvFleetDBActor is the env var holding the X-Actor header value.
// Defaults to LOOM_AGENT_NAME, then $USER.
const EnvFleetDBActor = "LOOM_FLEET_DB_ACTOR"

// EnvAgentName is the legacy env var for the actor identity. Used as a
// fallback when LOOM_FLEET_DB_ACTOR is unset.
const EnvAgentName = "LOOM_AGENT_NAME"

// StoreHandle bundles a Store with the cleanup function for any
// subprocess (embedded fleet-db) the bootstrap had to start. Callers
// MUST call Close to terminate the subprocess.
type StoreHandle struct {
	Store store.Store
	mode  Mode

	// embedded is the embedded fleet-db handle, set only in ModeLocal.
	embedded *EmbeddedFleetDB
}

// Mode reports the deployment mode chosen at OpenStore time.
func (h *StoreHandle) Mode() Mode { return h.mode }

// URL reports the fleet-db base URL used by this handle.
func (h *StoreHandle) URL() string {
	if h == nil {
		return ""
	}
	if h.embedded != nil {
		return h.embedded.URL()
	}
	return os.Getenv(EnvFleetDBURL)
}

// Close shuts down the store and any subprocess it owns. Idempotent.
func (h *StoreHandle) Close() error {
	var firstErr error
	if h.Store != nil {
		if err := h.Store.Close(); err != nil {
			firstErr = err
		}
	}
	if h.embedded != nil {
		if err := h.embedded.Stop(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// OpenStore constructs a Store appropriate for the current Mode.
//
// In ModeCloud (LOOM_FLEET_DB_URL set), it builds an HTTP client against
// the configured URL. The returned handle's embedded field is nil.
//
// In ModeLocal (default), it spawns an embedded fleet-db subprocess
// (via StartEmbedded) and builds an HTTP client against its URL. The
// caller MUST call handle.Close() during shutdown to terminate the
// subprocess.
//
// dataDir is the per-user loom directory (typically LoomDir()) and is
// only used in ModeLocal — it's where the embedded fleet-db's
// miniredis snapshot lives.
func OpenStore(ctx context.Context, dataDir string, logger *slog.Logger) (*StoreHandle, error) {
	if logger == nil {
		logger = slog.Default()
	}
	mode := DetectMode()

	// Reuse the shared transport-pooled client so fleet-db RPCs aren't
	// throttled by http.DefaultTransport's MaxIdleConnsPerHost=2 cap.
	// See internal/backend/fleet/transport.go for the rationale.
	cfg := fleetdb.Config{
		APIKey:     os.Getenv(EnvFleetDBAPIKey),
		Actor:      resolveActor(),
		HTTPClient: fleet.SharedHTTPClient(),
	}

	switch mode {
	case ModeCloud:
		cfg.BaseURL = os.Getenv(EnvFleetDBURL)
		client, err := fleetdb.New(cfg)
		if err != nil {
			return nil, fmt.Errorf("openstore: cloud: %w", err)
		}
		logger.Info("opened cloud fleet-db client", "url", cfg.BaseURL)
		return &StoreHandle{Store: client, mode: mode}, nil

	case ModeLocal:
		emb, err := StartEmbedded(ctx, dataDir, logger)
		if err != nil {
			return nil, fmt.Errorf("openstore: local: %w", err)
		}
		cfg.BaseURL = emb.URL()
		client, err := fleetdb.New(cfg)
		if err != nil {
			_ = emb.Stop()
			return nil, fmt.Errorf("openstore: local client: %w", err)
		}
		logger.Info("opened embedded fleet-db client", "url", cfg.BaseURL)
		return &StoreHandle{Store: client, mode: mode, embedded: emb}, nil

	default:
		return nil, fmt.Errorf("openstore: unknown mode %s", mode)
	}
}

// resolveActor returns the X-Actor identity. Priority:
// LOOM_FLEET_DB_ACTOR > LOOM_AGENT_NAME > $USER.
func resolveActor() string {
	if v := os.Getenv(EnvFleetDBActor); v != "" {
		return v
	}
	if v := os.Getenv(EnvAgentName); v != "" {
		return v
	}
	return os.Getenv("USER")
}
