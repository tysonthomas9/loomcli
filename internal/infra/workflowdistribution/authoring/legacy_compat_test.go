package authoring

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

var builtinMu sync.Mutex

// handleBuiltinWorkflowRegistrationError remains only for the legacy-store
// compatibility fixtures. Production behavior lives in app/workflowauthoring.
func handleBuiltinWorkflowRegistrationError(
	err error,
	name,
	workspace,
	current,
	digest string,
	reuse bool,
	reuseMissingRunners []string,
	requireManagedRefresh bool,
) error {
	if reuse && errors.Is(err, ErrBuildToolchainUnavailable) {
		if requireManagedRefresh {
			return fmt.Errorf("refresh managed built-in workflow %q to the embedded digest: %w", name, err)
		}
		slog.Warn(
			"builtin digest refresh unavailable; reusing registered version",
			"workflow", name,
			"workspace", workspace,
			"registered_digest", current,
			"embedded_digest", digest,
			"err", err.Error(),
		)
		return nil
	}
	if len(reuseMissingRunners) > 0 {
		if requireManagedRefresh {
			return fmt.Errorf("refresh managed built-in workflow %q runner manifest: %w", name, err)
		}
		slog.Warn(
			"builtin runner manifest is missing runners and re-register failed; reusing the registered version",
			"workflow", name,
			"workspace", workspace,
			"missing_runners", strings.Join(reuseMissingRunners, ","),
			"err", err.Error(),
		)
		return nil
	}
	return fmt.Errorf("register built-in workflow %q: %w", name, err)
}
