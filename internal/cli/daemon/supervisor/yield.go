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

// NoWorkFileName is the name of the no-work marker file in each worktree
// directory. Modeled on YieldFileName above: an advisory/read-only agent that
// holds no claim (or holds one but finds nothing actionable) writes this file
// before exiting 0 to tell the supervisor "I looked, there was nothing to
// do" — distinctly from an ordinary clean success. See `loom complete
// --no-work`.
const NoWorkFileName = ".agent.nowork"

// NoWorkReport contains metadata about an agent's "nothing to do" signal.
type NoWorkReport struct {
	Reason     string    `json:"reason"`
	TaskID     string    `json:"task_id"`
	ReportedAt time.Time `json:"reported_at"`
	ReportedBy string    `json:"reported_by"`
}

// WriteNoWorkFile atomically writes a no-work marker file to the given directory.
func WriteNoWorkFile(dir string, r *NoWorkReport) error {
	noWorkPath := filepath.Join(dir, NoWorkFileName)
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

// ReadNoWorkFile reads and parses the no-work marker file from the given
// directory. Returns (nil, nil) if the file does not exist.
func ReadNoWorkFile(dir string) (*NoWorkReport, error) {
	noWorkPath := filepath.Join(dir, NoWorkFileName)

	data, err := os.ReadFile(noWorkPath) //nolint:gosec // path is built from trusted WorktreePath
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read no-work file: %w", err)
	}

	var r NoWorkReport
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("failed to parse no-work file: %w", err)
	}

	return &r, nil
}

// ClearNoWorkFile removes the no-work marker file. Ignores os.ErrNotExist.
func ClearNoWorkFile(dir string) error {
	noWorkPath := filepath.Join(dir, NoWorkFileName)
	err := os.Remove(noWorkPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove no-work file: %w", err)
	}
	return nil
}

// IsNoWorkReported returns true if a no-work marker file exists in the given
// directory. Fast-path stat-only check — avoids JSON parse overhead.
func IsNoWorkReported(dir string) bool {
	noWorkPath := filepath.Join(dir, NoWorkFileName)
	_, err := os.Stat(noWorkPath)
	return err == nil
}

// NoWorkReportedAfter reads the no-work marker file and returns it only when
// the file's ModTime is at or after since. This is a staleness guard: a
// marker left behind by a killed agent (from a prior cycle) must not pin a
// later, unrelated run into the no-work classification — preFlightSetup
// clears the marker before every spawn, but a crash between clear and the
// next classification could otherwise leave a stale file on disk.
//
// A marker that exists and is fresh but fails to parse is still treated as
// "reported, no reason" (with a warning logged) rather than "not reported" —
// a malformed marker must never mask a legitimate no-work signal or fail the
// exit path.
// noWorkFreshnessSlack absorbs filesystem timestamp granularity: on Linux a
// file's mtime comes from the kernel's coarse clock, so a marker written
// microseconds after a time.Now() reading can carry an mtime a few
// milliseconds before it and look stale when it is not. Real stale markers
// come from a previous agent cycle, minutes older, so a small backward
// tolerance keeps the guard while never rejecting an honest same-instant
// write (an agent that reports no-work immediately after spawn).
const noWorkFreshnessSlack = 2 * time.Second

func NoWorkReportedAfter(dir string, since time.Time) (*NoWorkReport, bool) {
	noWorkPath := filepath.Join(dir, NoWorkFileName)
	info, err := os.Stat(noWorkPath)
	if err != nil {
		return nil, false
	}
	if info.ModTime().Before(since.Add(-noWorkFreshnessSlack)) {
		return nil, false
	}
	report, err := ReadNoWorkFile(dir)
	if err != nil {
		slog.Warn("failed to parse no-work marker", "dir", dir, "err", err)
		return &NoWorkReport{}, true
	}
	if report == nil {
		return nil, false
	}
	return report, true
}
