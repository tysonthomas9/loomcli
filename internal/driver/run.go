package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type RunOptions struct {
	WorkspaceKey   string
	DriverID       string
	EpicID         string
	RunID          string
	IdempotencyKey string
	Entrypoint     string
	SourceKind     string
	SourceRef      string
	Payload        json.RawMessage
}

func CreateDriverRun(ctx context.Context, s store.Store, opts RunOptions) (*domain.DriverRun, error) {
	if s == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	if strings.TrimSpace(opts.WorkspaceKey) == "" || strings.TrimSpace(opts.DriverID) == "" {
		return nil, fmt.Errorf("workspace key and driver id required: %w", domain.ErrInvalid)
	}
	driver, version, err := activeDriverVersion(ctx, s, opts.WorkspaceKey, opts.DriverID)
	if err != nil {
		return nil, err
	}
	runID := opts.RunID
	if runID == "" {
		runID = fmt.Sprintf("run-%d", time.Now().UTC().UnixNano())
	}
	entrypoint := opts.Entrypoint
	if entrypoint == "" {
		entrypoint = EntrypointRun
	}
	payload := clonePayload(opts.Payload)
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if !json.Valid(payload) {
		return nil, fmt.Errorf("payload must be valid JSON: %w", domain.ErrInvalid)
	}
	sourceKind := strings.TrimSpace(opts.SourceKind)
	if sourceKind == "" {
		sourceKind = "cli"
	}
	sourceRef := strings.TrimSpace(opts.SourceRef)
	if sourceRef == "" {
		sourceRef = "loom driver run"
	}
	return s.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    opts.WorkspaceKey,
		RunID:           runID,
		DriverID:        driver.DriverID,
		DriverVersionID: version.VersionID,
		Entrypoint:      entrypoint,
		SourceKind:      sourceKind,
		SourceRef:       sourceRef,
		EpicID:          opts.EpicID,
		IdempotencyKey:  opts.IdempotencyKey,
		Payload:         payload,
	})
}

func activeDriverVersion(ctx context.Context, s store.Store, workspaceKey, driverID string) (*domain.Driver, *domain.DriverVersion, error) {
	driver, err := s.Drivers().Get(ctx, workspaceKey, driverID)
	if err != nil {
		return nil, nil, fmt.Errorf("get driver: %w", err)
	}
	if driver.ActiveVersionID == "" {
		return nil, nil, fmt.Errorf("driver %q has no active version: %w", driverID, domain.ErrInvalid)
	}
	version, err := s.DriverVersions().Get(ctx, workspaceKey, driver.ActiveVersionID)
	if err != nil {
		return nil, nil, fmt.Errorf("get active driver version: %w", err)
	}
	if version.DriverID != driver.DriverID || version.ValidationStatus != domain.DriverVersionValidationPassed {
		return nil, nil, fmt.Errorf("driver %q active version %q is not a passed version: %w", driver.DriverID, driver.ActiveVersionID, domain.ErrInvalid)
	}
	return driver, version, nil
}

func clonePayload(in json.RawMessage) json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	out := make(json.RawMessage, len(in))
	copy(out, in)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

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
