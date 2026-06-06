package driver

import (
	"context"
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
	Input          map[string]string
}

func CreateDriverRun(ctx context.Context, s store.Store, opts RunOptions) (*domain.DriverRun, error) {
	if s == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	if strings.TrimSpace(opts.WorkspaceKey) == "" || strings.TrimSpace(opts.DriverID) == "" {
		return nil, fmt.Errorf("workspace key and driver id required: %w", domain.ErrInvalid)
	}
	driver, err := s.Drivers().Get(ctx, opts.WorkspaceKey, opts.DriverID)
	if err != nil {
		return nil, fmt.Errorf("get driver: %w", err)
	}
	if driver.ActiveVersionID == "" {
		return nil, fmt.Errorf("driver %q has no active version: %w", opts.DriverID, domain.ErrInvalid)
	}
	version, err := s.DriverVersions().Get(ctx, opts.WorkspaceKey, driver.ActiveVersionID)
	if err != nil {
		return nil, fmt.Errorf("get active driver version: %w", err)
	}
	if version.DriverID != driver.DriverID || version.ValidationStatus != domain.DriverVersionValidationPassed {
		return nil, fmt.Errorf("driver %q active version %q is not a passed version: %w", driver.DriverID, driver.ActiveVersionID, domain.ErrInvalid)
	}
	runID := opts.RunID
	if runID == "" {
		runID = fmt.Sprintf("run-%d", time.Now().UTC().UnixNano())
	}
	entrypoint := opts.Entrypoint
	if entrypoint == "" {
		entrypoint = EntrypointRun
	}
	input := cloneStringMap(opts.Input)
	if input == nil {
		input = map[string]string{}
	}
	if opts.EpicID != "" {
		input["epicId"] = opts.EpicID
	}
	return s.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    opts.WorkspaceKey,
		RunID:           runID,
		DriverID:        driver.DriverID,
		DriverVersionID: version.VersionID,
		Entrypoint:      entrypoint,
		SourceKind:      "cli",
		SourceRef:       "loom driver run",
		EpicID:          opts.EpicID,
		IdempotencyKey:  opts.IdempotencyKey,
		Input:           input,
	})
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
