package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// noWorkFileName mirrors supervisor.NoWorkFileName.
// SYNC: Must stay aligned with internal/cli/daemon/supervisor/yield.go
// (NoWorkFileName / NoWorkReport, at the bottom of that file next to the
// yield marker they are modeled on). internal/cli/agent cannot import
// internal/cli/daemon/supervisor (supervisor already imports this package),
// so the marker channel's wire shape is duplicated here rather than shared.
const noWorkFileName = ".agent.nowork"

// noWorkReport mirrors supervisor.NoWorkReport.
// SYNC: see noWorkFileName above.
type noWorkReport struct {
	Reason     string    `json:"reason"`
	TaskID     string    `json:"task_id"`
	ReportedAt time.Time `json:"reported_at"`
	ReportedBy string    `json:"reported_by"`
}

// writeNoWorkFile atomically writes a no-work marker file to the given path.
// Mirrors supervisor.WriteNoWorkFile (SYNC: see noWorkFileName).
func writeNoWorkFile(noWorkPath string, r *noWorkReport) error {
	tmpPath := noWorkPath + ".tmp"

	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal no-work report: %w", err)
	}

	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write no-work tmp: %w", err)
	}

	if err := os.Rename(tmpPath, noWorkPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename no-work file: %w", err)
	}

	return nil
}
