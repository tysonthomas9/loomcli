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
