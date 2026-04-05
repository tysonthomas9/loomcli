package cli

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

// StopReasonYielded is set when an agent exits in response to a yield request.
const StopReasonYielded StopReason = "yielded"

// YieldRequest contains metadata about a cooperative preemption request.
type YieldRequest struct {
	Reason      string    `json:"reason"`
	RequestedAt time.Time `json:"requested_at"`
	RequestedBy string    `json:"requested_by"`
}

// RequestYield writes a yield file to the agent's worktree, signaling it to
// finish current work and exit gracefully.
func (d *Daemon) RequestYield(ap *AgentProcess, reason string) error {
	req := &YieldRequest{
		Reason:      reason,
		RequestedAt: time.Now(),
		RequestedBy: "daemon",
	}
	if err := WriteYieldFile(ap.worktreePath, req); err != nil {
		return fmt.Errorf("request yield for %s: %w", ap.entry.Worktree, err)
	}
	slog.Info("yield requested", "worktree", ap.entry.Worktree, "reason", reason)
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

	data, err := os.ReadFile(yieldPath) //nolint:gosec // path is built from trusted worktreePath
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

// handleAgentControlYield handles the agent_yield control socket operation.
func (d *Daemon) handleAgentControlYield(name string) DaemonControlResponse {
	if name == "" {
		return DaemonControlResponse{Error: "agent name is required"}
	}

	d.agentsMu.RLock()
	var target *AgentProcess
	for _, ap := range d.agents {
		if ap.entry.Worktree == name {
			target = ap
			break
		}
	}
	d.agentsMu.RUnlock()

	if target == nil {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q not found", name)}
	}

	target.mu.Lock()
	pid := target.pid
	target.mu.Unlock()

	if pid == 0 {
		return DaemonControlResponse{Error: fmt.Sprintf("agent %q is not running", name)}
	}

	if err := d.RequestYield(target, "manual_stop"); err != nil {
		return DaemonControlResponse{Error: fmt.Sprintf("failed to yield agent %q: %v", name, err)}
	}

	return DaemonControlResponse{Success: true}
}
