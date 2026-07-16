package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// EnvFleetDBAPIKey is the env var holding the fleet-db API key value.
// Clients send it as X-API-Key and X-Fleet-API-Key. Optional in dev mode.
const EnvFleetDBAPIKey = "LOOM_FLEET_DB_API_KEY" //nolint:gosec // env var name, not a credential

// EnvFleetDBActor is the env var holding the X-Actor header value.
// Defaults to the current agent name, then $USER.
const EnvFleetDBActor = "LOOM_FLEET_DB_ACTOR"

// EnvAgentName is the env var used to identify the current agent process.
const EnvAgentName = "LOOM_AGENT_NAME"

// StoreHandle bundles a Store with the cleanup function for any
// subprocess (embedded fleet-db) the bootstrap had to start. Callers
// MUST call Close to terminate the subprocess.
type StoreHandle struct {
	Store store.Store
	mode  Mode
	url   string
	// fleetDBClientAPIKey is retained only in process memory so composition
	// can authenticate secondary FleetDB clients (for example, the
	// per-workspace mutation subscriber). It must never be logged, persisted,
	// or copied into ambient environment state.
	fleetDBClientAPIKey string //nolint:gosec // process-local FleetDB service credential
	// fleetDBClient is the single low-level transport client constructed for
	// this handle. Composition may wrap it in capability adapters while the
	// legacy Store continues to use the same connection pool and credentials.
	fleetDBClient *fleetdb.Client

	// embedded is the embedded fleet-db handle, set only in ModeLocal.
	embedded *EmbeddedFleetDB
}

// OpenStoreOptions carries startup requirements derived by the caller from the
// capability slices enabled in its own configuration. Requirements are never
// inferred from FleetDB itself: the server manifest verifies compatibility but
// does not decide which Loom behavior is enabled.
type OpenStoreOptions struct {
	RequiredFleetDBCapabilities []string
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
	if h.url != "" {
		return h.url
	}
	return os.Getenv(EnvFleetDBURL)
}

// FleetDBClient returns the shared low-level FleetDB client for capability
// adapter composition. It is nil only for test handles not opened by bootstrap.
func (h *StoreHandle) FleetDBClient() *fleetdb.Client {
	if h == nil {
		return nil
	}
	return h.fleetDBClient
}

// FleetDBClientAPIKey returns the process-local credential needed when serve
// composes a secondary FleetDB client. Callers must keep the value in memory
// and must not log, persist, or export it to child processes.
func (h *StoreHandle) FleetDBClientAPIKey() string {
	if h == nil {
		return ""
	}
	return h.fleetDBClientAPIKey
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
	return OpenStoreWithOptions(ctx, dataDir, logger, OpenStoreOptions{})
}

// OpenStoreWithOptions opens the runtime Store and verifies any explicitly
// required FleetDB deployment capabilities before returning it to the caller.
// Empty requirements preserve the legacy startup path and perform no manifest
// request, allowing the readiness contract to land before any slice requires
// the not-yet-universal endpoint.
func OpenStoreWithOptions(ctx context.Context, dataDir string, logger *slog.Logger, opts OpenStoreOptions) (*StoreHandle, error) {
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

	handle, err := openStoreForMode(ctx, dataDir, cfg, logger, mode)
	if err != nil {
		return nil, err
	}
	if err := requireFleetDBCapabilities(ctx, handle, opts.RequiredFleetDBCapabilities); err != nil {
		if closeErr := handle.Close(); closeErr != nil {
			return nil, fmt.Errorf("openstore: fleet-db compatibility: %w (cleanup: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("openstore: fleet-db compatibility: %w", err)
	}
	return handle, nil
}

func openStoreForMode(ctx context.Context, dataDir string, cfg fleetdb.Config, logger *slog.Logger, mode Mode) (*StoreHandle, error) {
	switch mode {
	case ModeCloud:
		return openCloudStore(cfg, logger)
	case ModeLocal:
		return openLocalStore(ctx, dataDir, cfg, logger)
	default:
		return nil, fmt.Errorf("openstore: unknown mode %s", mode)
	}
}

type fleetDBCapabilityChecker interface {
	RequireCapabilities(context.Context, []string) error
}

func requireFleetDBCapabilities(ctx context.Context, handle *StoreHandle, required []string) error {
	if len(required) == 0 {
		return nil
	}
	if handle == nil || handle.Store == nil {
		return errors.New("store handle is unavailable")
	}
	checker, ok := handle.Store.(fleetDBCapabilityChecker)
	if !ok {
		return fmt.Errorf("store %T does not support FleetDB capability negotiation", handle.Store)
	}
	return checker.RequireCapabilities(ctx, required)
}

func openCloudStore(cfg fleetdb.Config, logger *slog.Logger) (*StoreHandle, error) {
	cfg.BaseURL = os.Getenv(EnvFleetDBURL)
	client, err := fleetdb.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("openstore: cloud: %w", err)
	}
	logger.Info("opened cloud fleet-db client", "url", cfg.BaseURL)
	return &StoreHandle{
		Store:               client,
		mode:                ModeCloud,
		url:                 cfg.BaseURL,
		fleetDBClientAPIKey: cfg.APIKey,
		fleetDBClient:       client,
	}, nil
}

func openLocalStore(ctx context.Context, dataDir string, cfg fleetdb.Config, logger *slog.Logger) (*StoreHandle, error) {
	fleetDir := filepath.Join(dataDir, "fleet-db")
	if h, ok, err := tryReuseLocalStore(ctx, fleetDir, cfg, logger); ok || err != nil {
		return h, err
	}

	emb, err := StartEmbedded(ctx, dataDir, logger)
	if err != nil {
		if errors.Is(err, ErrEmbeddedAlreadyRunning) {
			return waitAndOpenLocalStore(ctx, fleetDir, cfg, logger, err)
		}
		return nil, fmt.Errorf("openstore: local: %w", err)
	}
	cfg.BaseURL = emb.URL()
	client, err := emb.NewClient(cfg)
	if err != nil {
		_ = emb.Stop()
		return nil, fmt.Errorf("openstore: local client: %w", err)
	}
	logger.Info("opened embedded fleet-db client", "url", cfg.BaseURL)
	return &StoreHandle{
		Store:               client,
		mode:                ModeLocal,
		url:                 cfg.BaseURL,
		fleetDBClientAPIKey: emb.serviceCredential,
		fleetDBClient:       client,
		embedded:            emb,
	}, nil
}

func tryReuseLocalStore(ctx context.Context, fleetDir string, cfg fleetdb.Config, logger *slog.Logger) (*StoreHandle, bool, error) {
	url, ok, reuseErr := reuseEmbeddedRuntime(ctx, fleetDir, logger, healthCheckTimeout)
	if !ok {
		if reuseErr != nil {
			logger.Debug("existing embedded fleet-db runtime is not reusable", "err", reuseErr)
		}
		return nil, false, nil
	}
	cfg.BaseURL = url
	serviceCredential, err := authority.ReadLocalFleetDBServiceCredential(embeddedFleetDBAuthDir(filepath.Dir(fleetDir)))
	if err != nil {
		return nil, true, fmt.Errorf("openstore: local reused service credential: %w", err)
	}
	cfg.APIKey = serviceCredential
	client, err := fleetdb.New(cfg)
	if err != nil {
		return nil, true, fmt.Errorf("openstore: local reused client: %w", err)
	}
	logger.Info("opened existing embedded fleet-db client", "url", cfg.BaseURL)
	return &StoreHandle{
		Store:               client,
		mode:                ModeLocal,
		url:                 cfg.BaseURL,
		fleetDBClientAPIKey: serviceCredential,
		fleetDBClient:       client,
	}, true, nil
}

func waitAndOpenLocalStore(ctx context.Context, fleetDir string, cfg fleetdb.Config, logger *slog.Logger, lockErr error) (*StoreHandle, error) {
	url, waitErr := waitForEmbeddedRuntime(ctx, fleetDir, startupTimeout, logger)
	if waitErr != nil {
		return nil, fmt.Errorf("openstore: local: %w; existing runtime did not become healthy: %v", lockErr, waitErr)
	}
	cfg.BaseURL = url
	serviceCredential, err := authority.ReadLocalFleetDBServiceCredential(embeddedFleetDBAuthDir(filepath.Dir(fleetDir)))
	if err != nil {
		return nil, fmt.Errorf("openstore: local waited service credential: %w", err)
	}
	cfg.APIKey = serviceCredential
	client, err := fleetdb.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("openstore: local waited client: %w", err)
	}
	logger.Info("opened existing embedded fleet-db client after startup wait", "url", cfg.BaseURL)
	return &StoreHandle{
		Store:               client,
		mode:                ModeLocal,
		url:                 cfg.BaseURL,
		fleetDBClientAPIKey: serviceCredential,
		fleetDBClient:       client,
	}, nil
}

// resolveActor returns the X-Actor identity.
func resolveActor() string {
	if v := os.Getenv(EnvFleetDBActor); v != "" {
		return v
	}
	if v := os.Getenv(EnvAgentName); v != "" {
		return v
	}
	return os.Getenv("USER")
}
