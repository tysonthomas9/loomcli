package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
)

// driverRunContext is the resolved identity preamble shared by driver runtime
// subcommands: workspace key, parent DriverRun ID, and owner credentials, each
// resolved from the per-command flag with the documented LOOM_DRIVER_* env
// fallback applied.
type driverRunContext struct {
	WorkspaceKey string
	DriverRunID  string
	NodeID       string
	LeaseID      string
	fencingFlag  int64
}

// resolveDriverRunContext applies the documented fallback priority for driver
// runtime commands: each flag value wins, then the matching LOOM_DRIVER_* env
// var; the workspace finally falls back to the active workspace.
func resolveDriverRunContext(ctx context.Context, h *bootstrap.StoreHandle, workspaceKey, driverRunID, nodeID, leaseID string, fencingToken int64) (driverRunContext, error) {
	ws, err := resolveDriverWorkspace(ctx, h, workspaceKey)
	if err != nil {
		return driverRunContext{}, err
	}
	return driverRunContext{
		WorkspaceKey: ws,
		DriverRunID:  resolveDriverRunID(driverRunID),
		NodeID:       firstNonEmpty(nodeID, os.Getenv("LOOM_DRIVER_NODE_ID")),
		LeaseID:      firstNonEmpty(leaseID, os.Getenv("LOOM_DRIVER_LEASE_ID")),
		fencingFlag:  fencingToken,
	}, nil
}

// FencingToken resolves the fencing token from the flag value, then the
// LOOM_DRIVER_FENCING_TOKEN env var. Parsing is deferred so commands that
// never need the token keep their current error behavior.
func (c driverRunContext) FencingToken() (int64, error) {
	return resolveDriverRunFencingToken(c.fencingFlag)
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

func resolveRunningDriverRun(ctx context.Context, h *bootstrap.StoreHandle, workspaceKey, driverRunID, nodeID, leaseID string, fencingToken int64) (string, *domain.DriverRun, error) {
	rc, err := resolveDriverRunContext(ctx, h, workspaceKey, driverRunID, nodeID, leaseID, fencingToken)
	if err != nil {
		return "", nil, err
	}
	fence, err := rc.FencingToken()
	if err != nil {
		return "", nil, err
	}
	client, err := newDriverRuntimeClient(driverRuntimeClientOptions{
		WorkspaceKey: rc.WorkspaceKey, DriverRunID: rc.DriverRunID,
		NodeID: rc.NodeID, LeaseID: rc.LeaseID, FencingToken: fence,
	})
	if err != nil {
		return "", nil, err
	}
	parent, err := client.verifyRun(ctx)
	if err != nil {
		return "", nil, err
	}
	return rc.WorkspaceKey, parent, nil
}

func resolveDriverRunFencingToken(flagValue int64) (int64, error) {
	if flagValue != 0 {
		return flagValue, nil
	}
	raw := strings.TrimSpace(os.Getenv("LOOM_DRIVER_FENCING_TOKEN"))
	if raw == "" {
		return 0, nil
	}
	token, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || token <= 0 {
		if err == nil {
			err = domain.ErrInvalid
		}
		return 0, fmt.Errorf("parse LOOM_DRIVER_FENCING_TOKEN: %w", err)
	}
	return token, nil
}

func newDriverIssueBackend(h *bootstrap.StoreHandle, ws, actor string) (*fleet.FleetBackend, error) {
	issueBackend, err := fleet.New(fleet.Config{
		BaseURL:     h.URL(),
		WorkspaceID: ws,
		APIKey:      os.Getenv(bootstrap.EnvFleetDBAPIKey),
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
