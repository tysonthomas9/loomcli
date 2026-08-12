package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
)

// driverRunContext is the resolved identity preamble shared by driver runtime
// subcommands: workspace key and parent DriverRun ID. Owner identity comes
// only from LOOM_RUN_TOKEN.
type driverRunContext struct {
	WorkspaceKey string
	DriverRunID  string
}

// resolveDriverRunContext applies the documented fallback priority for driver
// runtime commands: each flag value wins, then the matching LOOM_DRIVER_* env
// var; the workspace finally falls back to the active workspace.
func resolveDriverRunContext(ctx context.Context, h *bootstrap.StoreHandle, workspaceKey, driverRunID string) (driverRunContext, error) {
	ws, err := resolveDriverWorkspace(ctx, h, workspaceKey)
	if err != nil {
		return driverRunContext{}, err
	}
	return driverRunContext{
		WorkspaceKey: ws,
		DriverRunID:  resolveDriverRunID(driverRunID),
	}, nil
}

// resolveDriverWorkspace resolves the workspace key for driver runtime
// commands: flag value, then LOOM_DRIVER_WORKSPACE, then the active workspace.
func resolveDriverWorkspace(ctx context.Context, h *bootstrap.StoreHandle, flagValue string) (string, error) {
	return resolveWorkspaceKey(ctx, h, flagValue, os.Getenv("LOOM_DRIVER_WORKSPACE"))
}

// resolveWorkerWorkspace resolves the workspace key for worker runtime
// commands: flag value, then LOOM_WORKER_WORKSPACE, then LOOM_DRIVER_WORKSPACE,
// then the active workspace.
func resolveWorkerWorkspace(ctx context.Context, h *bootstrap.StoreHandle, flagValue string) (string, error) {
	return resolveWorkspaceKey(ctx, h, flagValue, os.Getenv("LOOM_WORKER_WORKSPACE"), os.Getenv("LOOM_DRIVER_WORKSPACE"))
}

func resolveWorkspaceKey(ctx context.Context, h *bootstrap.StoreHandle, candidates ...string) (string, error) {
	if ws := firstNonEmpty(candidates...); ws != "" {
		return ws, nil
	}
	return cmdstore.ActiveWorkspace(ctx, h.Store)
}

func resolveDriverRunID(flagValue string) string {
	return firstNonEmpty(flagValue, os.Getenv("LOOM_DRIVER_RUN_ID"))
}

func resolveRunningDriverRun(ctx context.Context, h *bootstrap.StoreHandle, workspaceKey, driverRunID string) (string, *domain.DriverRun, error) {
	rc, err := resolveDriverRunContext(ctx, h, workspaceKey, driverRunID)
	if err != nil {
		return "", nil, err
	}
	client, err := newDriverRuntimeClient(driverRuntimeClientOptions(rc))
	if err != nil {
		return "", nil, err
	}
	parent, err := client.verifyRun(ctx)
	if err != nil {
		return "", nil, err
	}
	return rc.WorkspaceKey, parent, nil
}

func newDriverIssueBackend(h *bootstrap.StoreHandle, ws, actor string) (*fleet.FleetBackend, error) {
	issueBackend, err := fleet.New(fleet.Config{
		BaseURL:     h.URL(),
		WorkspaceID: ws,
		APIKey:      h.FleetDBClientAPIKey(),
		Actor:       actor,
	})
	if err != nil {
		return nil, fmt.Errorf("create fleet-db issue backend: %w", err)
	}
	return issueBackend, nil
}

func driverRunActor(runID string) string {
	return driverpkg.DriverRunActor(runID)
}

func driverRunPayloadEpicID(payload json.RawMessage) string {
	return driverpkg.DriverRunPayloadEpicID(payload)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
