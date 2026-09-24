package serve

import (
	"log/slog"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/browserauth"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/browsers"
)

// envBrowserOperatorGrants holds a JSON array of browsers.OperatorGrant: each
// grant gives remote user subjects (JWT `sub`) browser operator access
// (list/get/select) in exactly one canonical workspace, optionally limited to
// named agents. Unset denies all remote browser operator requests; malformed
// grants fail closed. It is separate from the file-browser role.
const envBrowserOperatorGrants = "LOOM_BROWSER_OPERATOR_GRANTS"

// envBrowserOperatorSubjectsRemoved was a global, workspace-blind allowlist.
// It is no longer honored; setting it only logs an error.
const envBrowserOperatorSubjectsRemoved = "LOOM_BROWSER_OPERATOR_SUBJECTS"

// applyBrowserConfig wires the durable-browser dependencies. Every missing
// piece leaves the routes authenticating and answering a visible 503/403
// instead of inventing an identity.
func applyBrowserConfig(cfg *webui.ServerConfig, storeHandle *bootstrap.StoreHandle, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	if client := storeHandle.Browsers(); client != nil {
		cfg.BrowserBackend = client
	}
	if signer := resolveBrowserSigner(storeHandle, logger); signer != nil {
		cfg.BrowserSigner = signer
	}
	if cfg.ExtAuthURL == "" {
		cfg.BrowserOperatorSocketPath = strings.TrimSpace(os.Getenv(browserauth.EnvOperatorSocket))
	}
	cfg.BrowserPermissionResolver = browserOperatorGrantResolver(os.Getenv(envBrowserOperatorGrants), os.Getenv(envBrowserOperatorSubjectsRemoved), logger)
}

// resolveBrowserSigner prefers an explicitly configured signing key; in local
// mode (embedded fleet-db) it uses the data dir's local key, which the
// embedded fleet-db is configured to verify.
func resolveBrowserSigner(storeHandle *bootstrap.StoreHandle, logger *slog.Logger) *browserauth.Signer {
	signer, err := browserauth.SignerFromEnv(os.Getenv)
	if err != nil {
		logger.Error("browser delegation signing key is invalid; durable browser routes disabled", "err", err)
		return nil
	}
	if signer != nil {
		return signer
	}
	if storeHandle == nil || storeHandle.Mode() != bootstrap.ModeLocal {
		logger.Info("no browser delegation signing key configured; durable browser routes disabled", "env", browserauth.EnvSigningKey)
		return nil
	}
	kp, err := browserauth.LoadOrCreateLocalKey(bootstrap.LoomDir())
	if err != nil {
		logger.Error("local browser delegation key unavailable; durable browser routes disabled", "err", err)
		return nil
	}
	local, err := browserauth.LocalSigner(kp)
	if err != nil {
		logger.Error("local browser delegation signer invalid; durable browser routes disabled", "err", err)
		return nil
	}
	return local
}

// browserOperatorGrantResolver builds the remote operator permission check from
// workspace-scoped grants. Any configuration error returns nil, which denies
// every remote browser operator request.
func browserOperatorGrantResolver(grants, removedSubjects string, logger *slog.Logger) browsers.PermissionResolver {
	if strings.TrimSpace(removedSubjects) != "" {
		logger.Error("LOOM_BROWSER_OPERATOR_SUBJECTS is no longer supported because it ignored workspaces; use workspace-scoped LOOM_BROWSER_OPERATOR_GRANTS",
			"env", envBrowserOperatorGrants)
	}
	resolver, count, err := browsers.ParseOperatorGrants(grants)
	if err != nil {
		logger.Error("browser operator grants are invalid; remote browser operator access disabled", "env", envBrowserOperatorGrants, "err", err)
		return nil
	}
	if resolver != nil {
		logger.Warn("remote browser operator grants enabled", "grants", count)
	}
	return resolver
}
