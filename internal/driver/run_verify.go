package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// VerifyRunningDriverRun loads the parent DriverRun and proves the caller may
// act on its behalf. The run must be running; when it is locked (lease or
// fencing token set) the caller's owner credentials are verified through a
// fenced heartbeat, so a stale executor can never act after losing the lease.
// fencingToken is a resolver so callers with lazily-parsed credentials only
// pay (and surface) the parse when the run is actually locked. Shared by the
// driver CLI subcommands and the driver-op HTTP API.
func VerifyRunningDriverRun(ctx context.Context, st store.Store, ws, runID, nodeID, leaseID string, fencingToken func() (int64, error)) (*domain.DriverRun, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("driver-run-id required: %w", domain.ErrInvalid)
	}
	parent, err := st.DriverRuns().Get(ctx, ws, runID)
	if err != nil {
		return nil, fmt.Errorf("get parent driver run: %w", err)
	}
	if parent.Status != domain.DriverRunRunning {
		return nil, fmt.Errorf("driver run %q is %s, want running: %w", runID, parent.Status, domain.ErrInvalidTransition)
	}
	if parent.LeaseID != "" || parent.FencingToken != 0 {
		ownerFence := int64(0)
		if fencingToken != nil {
			ownerFence, err = fencingToken()
			if err != nil {
				return nil, err
			}
		}
		if nodeID == "" || leaseID == "" || ownerFence == 0 {
			return nil, fmt.Errorf("driver run %q owner credentials required: %w", runID, domain.ErrNotOwner)
		}
		parent, err = st.DriverRuns().Heartbeat(ctx, ws, runID, nodeID, leaseID, ownerFence)
		if err != nil {
			return nil, fmt.Errorf("verify driver run owner: %w", err)
		}
	}
	return parent, nil
}

// DriverRunActor is the store actor identity a driver run acts as.
func DriverRunActor(runID string) string {
	return "driver-run:" + runID
}

// DriverRunPayloadEpicID extracts the epicId field from a driver run payload,
// returning "" when absent or unparseable.
func DriverRunPayloadEpicID(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		return ""
	}
	value, ok := object["epicId"].(string)
	if !ok {
		return ""
	}
	return value
}
