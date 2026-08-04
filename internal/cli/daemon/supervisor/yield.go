package supervisor

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// YieldFileName is the name of the yield file in each worktree directory.
const YieldFileName = ".agent.yield"

// YieldRequest contains metadata about a cooperative preemption request.
type YieldRequest struct {
	Reason      string    `json:"reason"`
	RequestedAt time.Time `json:"requested_at"`
	RequestedBy string    `json:"requested_by"`
}

// RequestYield writes a yield file to the agent's worktree, signaling it to
// finish current work and exit gracefully.
func (s *Supervisor) RequestYield(ap *AgentProcess, reason string) error {
	req := &YieldRequest{
		Reason:      reason,
		RequestedAt: time.Now(),
		RequestedBy: "daemon",
	}
	if err := WriteYieldFile(ap.WorktreePath, req); err != nil {
		return fmt.Errorf("request yield for %s: %w", ap.Entry.Worktree, err)
	}
	slog.Info("yield requested", "worktree", ap.Entry.Worktree, "reason", reason)
	return nil
}

// WriteYieldFile atomically writes a yield file to the given directory.
func WriteYieldFile(dir string, req *YieldRequest) error {
	yieldPath := filepath.Join(dir, YieldFileName)
	tmpPath := yieldPath + ".tmp"

	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal yield request: %w", err)
	}

	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write yield tmp: %w", err)
	}

	if err := os.Rename(tmpPath, yieldPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename yield file: %w", err)
	}

	return nil
}

// ReadYieldFile reads and parses the yield file from the given directory.
// Returns (nil, nil) if the file does not exist.
func ReadYieldFile(dir string) (*YieldRequest, error) {
	yieldPath := filepath.Join(dir, YieldFileName)

	data, err := os.ReadFile(yieldPath) //nolint:gosec // path is built from trusted WorktreePath
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read yield file: %w", err)
	}

	var req YieldRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("failed to parse yield file: %w", err)
	}

	return &req, nil
}

// ClearYieldFile removes the yield file. Ignores os.ErrNotExist.
func ClearYieldFile(dir string) error {
	yieldPath := filepath.Join(dir, YieldFileName)
	err := os.Remove(yieldPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove yield file: %w", err)
	}
	return nil
}

// IsYieldRequested returns true if a yield file exists in the given directory.
// This is the fast-path check for hot loops — avoids JSON parse overhead.
func IsYieldRequested(dir string) bool {
	yieldPath := filepath.Join(dir, YieldFileName)
	_, err := os.Stat(yieldPath)
	return err == nil
}
