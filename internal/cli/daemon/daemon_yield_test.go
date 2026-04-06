//go:build ignore

package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Yield file I/O tests
// ---------------------------------------------------------------------------

func TestWriteYieldFile_Success(t *testing.T) {
	dir := t.TempDir()

	req := &YieldRequest{
		Reason:      "preempt",
		RequestedAt: time.Now().Truncate(time.Millisecond),
		RequestedBy: "daemon",
	}

	if err := WriteYieldFile(dir, req); err != nil {
		t.Fatalf("WriteYieldFile error: %v", err)
	}

	yieldPath := filepath.Join(dir, YieldFileName)
	data, err := os.ReadFile(yieldPath)
	if err != nil {
		t.Fatalf("yield file not found after WriteYieldFile: %v", err)
	}

	var got YieldRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("yield file contains invalid JSON: %v", err)
	}

	if got.Reason != req.Reason {
		t.Errorf("Reason = %q, want %q", got.Reason, req.Reason)
	}
	if got.RequestedBy != req.RequestedBy {
		t.Errorf("RequestedBy = %q, want %q", got.RequestedBy, req.RequestedBy)
	}
}

func TestWriteYieldFile_Atomic(t *testing.T) {
	dir := t.TempDir()

	req := &YieldRequest{
		Reason:      "preempt",
		RequestedAt: time.Now(),
		RequestedBy: "daemon",
	}

	if err := WriteYieldFile(dir, req); err != nil {
		t.Fatalf("WriteYieldFile error: %v", err)
	}

	tmpPath := filepath.Join(dir, YieldFileName+".tmp")
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("temp file %s should not exist after successful write", tmpPath)
	}
}

func TestReadYieldFile_Success(t *testing.T) {
	dir := t.TempDir()

	want := &YieldRequest{
		Reason:      "higher_priority",
		RequestedAt: time.Now().Truncate(time.Millisecond),
		RequestedBy: "scheduler",
	}

	if err := WriteYieldFile(dir, want); err != nil {
		t.Fatalf("WriteYieldFile error: %v", err)
	}

	got, err := ReadYieldFile(dir)
	if err != nil {
		t.Fatalf("ReadYieldFile error: %v", err)
	}
	if got == nil {
		t.Fatal("ReadYieldFile returned nil, expected non-nil")
	}

	if got.Reason != want.Reason {
		t.Errorf("Reason = %q, want %q", got.Reason, want.Reason)
	}
	if got.RequestedBy != want.RequestedBy {
		t.Errorf("RequestedBy = %q, want %q", got.RequestedBy, want.RequestedBy)
	}
	if !got.RequestedAt.Equal(want.RequestedAt) {
		t.Errorf("RequestedAt = %v, want %v", got.RequestedAt, want.RequestedAt)
	}
}

func TestReadYieldFile_NotExists(t *testing.T) {
	dir := t.TempDir()

	got, err := ReadYieldFile(dir)
	if err != nil {
		t.Fatalf("ReadYieldFile error: %v", err)
	}
	if got != nil {
		t.Errorf("ReadYieldFile returned %+v, want nil for non-existent file", got)
	}
}

func TestClearYieldFile_Success(t *testing.T) {
	dir := t.TempDir()

	req := &YieldRequest{
		Reason:      "preempt",
		RequestedAt: time.Now(),
		RequestedBy: "daemon",
	}

	if err := WriteYieldFile(dir, req); err != nil {
		t.Fatalf("WriteYieldFile error: %v", err)
	}

	if err := ClearYieldFile(dir); err != nil {
		t.Fatalf("ClearYieldFile error: %v", err)
	}

	yieldPath := filepath.Join(dir, YieldFileName)
	if _, err := os.Stat(yieldPath); !os.IsNotExist(err) {
		t.Errorf("yield file still exists after ClearYieldFile")
	}
}

func TestClearYieldFile_NotExists(t *testing.T) {
	dir := t.TempDir()

	if err := ClearYieldFile(dir); err != nil {
		t.Errorf("ClearYieldFile on empty dir returned error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// IsYieldRequested tests
// ---------------------------------------------------------------------------

func TestIsYieldRequested_True(t *testing.T) {
	dir := t.TempDir()

	req := &YieldRequest{
		Reason:      "preempt",
		RequestedAt: time.Now(),
		RequestedBy: "daemon",
	}
	if err := WriteYieldFile(dir, req); err != nil {
		t.Fatalf("WriteYieldFile error: %v", err)
	}

	if !IsYieldRequested(dir) {
		t.Error("IsYieldRequested = false, want true when yield file exists")
	}
}

func TestIsYieldRequested_False(t *testing.T) {
	dir := t.TempDir()

	if IsYieldRequested(dir) {
		t.Error("IsYieldRequested = true, want false when no yield file")
	}
}

func TestIsYieldRequested_AfterClear(t *testing.T) {
	dir := t.TempDir()

	req := &YieldRequest{
		Reason:      "preempt",
		RequestedAt: time.Now(),
		RequestedBy: "daemon",
	}
	if err := WriteYieldFile(dir, req); err != nil {
		t.Fatalf("WriteYieldFile error: %v", err)
	}

	if err := ClearYieldFile(dir); err != nil {
		t.Fatalf("ClearYieldFile error: %v", err)
	}

	if IsYieldRequested(dir) {
		t.Error("IsYieldRequested = true, want false after ClearYieldFile")
	}
}

// ---------------------------------------------------------------------------
// RequestYield method tests
// ---------------------------------------------------------------------------

func TestDaemon_RequestYield_WritesFile(t *testing.T) {
	dir := t.TempDir()

	d := &Daemon{
		config: &DaemonConfig{Daemon: DaemonSettings{}},
	}
	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon"},
		worktreePath: dir,
	}

	if err := d.RequestYield(ap, "preempt"); err != nil {
		t.Fatalf("RequestYield error: %v", err)
	}

	yieldPath := filepath.Join(dir, YieldFileName)
	if _, err := os.Stat(yieldPath); os.IsNotExist(err) {
		t.Error("yield file was not created by RequestYield")
	}
}

func TestDaemon_RequestYield_Reason(t *testing.T) {
	dir := t.TempDir()

	d := &Daemon{
		config: &DaemonConfig{Daemon: DaemonSettings{}},
	}

	reasons := []string{"manual_stop", "higher_priority", "resource_pressure"}
	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			ap := &AgentProcess{
				entry:        AgentEntry{Worktree: "falcon"},
				worktreePath: dir,
			}

			if err := d.RequestYield(ap, reason); err != nil {
				t.Fatalf("RequestYield error: %v", err)
			}

			got, err := ReadYieldFile(dir)
			if err != nil {
				t.Fatalf("ReadYieldFile error: %v", err)
			}
			if got == nil {
				t.Fatal("ReadYieldFile returned nil")
			}
			if got.Reason != reason {
				t.Errorf("Reason = %q, want %q", got.Reason, reason)
			}
		})
	}
}

func TestDaemon_RequestYield_Timestamp(t *testing.T) {
	dir := t.TempDir()

	d := &Daemon{
		config: &DaemonConfig{Daemon: DaemonSettings{}},
	}
	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon"},
		worktreePath: dir,
	}

	before := time.Now().Add(-1 * time.Second)
	if err := d.RequestYield(ap, "preempt"); err != nil {
		t.Fatalf("RequestYield error: %v", err)
	}
	after := time.Now().Add(1 * time.Second)

	got, err := ReadYieldFile(dir)
	if err != nil {
		t.Fatalf("ReadYieldFile error: %v", err)
	}
	if got == nil {
		t.Fatal("ReadYieldFile returned nil")
	}
	if got.RequestedAt.Before(before) || got.RequestedAt.After(after) {
		t.Errorf("RequestedAt = %v, want between %v and %v", got.RequestedAt, before, after)
	}
}

// ---------------------------------------------------------------------------
// Control socket handler tests
// ---------------------------------------------------------------------------

func TestHandleAgentControlYield_Success(t *testing.T) {
	dir := t.TempDir()

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon"},
		worktreePath: dir,
		pid:          12345,
	}

	d := &Daemon{
		config: &DaemonConfig{Daemon: DaemonSettings{}},
		agents: []*AgentProcess{ap},
	}

	resp := d.handleAgentControlYield("falcon")
	if !resp.Success {
		t.Errorf("Success = false, want true; error: %s", resp.Error)
	}

	yieldPath := filepath.Join(dir, YieldFileName)
	if _, err := os.Stat(yieldPath); os.IsNotExist(err) {
		t.Error("yield file was not created by handleAgentControlYield")
	}
}

func TestHandleAgentControlYield_NotFound(t *testing.T) {
	d := &Daemon{
		config: &DaemonConfig{Daemon: DaemonSettings{}},
		agents: []*AgentProcess{},
	}

	resp := d.handleAgentControlYield("nonexistent")
	if resp.Success {
		t.Error("Success = true, want false for unknown agent")
	}
	if !strings.Contains(resp.Error, "not found") {
		t.Errorf("Error = %q, want it to contain 'not found'", resp.Error)
	}
}

func TestHandleAgentControlYield_NotRunning(t *testing.T) {
	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon"},
		worktreePath: t.TempDir(),
		pid:          0, // not running
	}

	d := &Daemon{
		config: &DaemonConfig{Daemon: DaemonSettings{}},
		agents: []*AgentProcess{ap},
	}

	resp := d.handleAgentControlYield("falcon")
	if resp.Success {
		t.Error("Success = true, want false for agent with pid=0")
	}
	if !strings.Contains(resp.Error, "not running") {
		t.Errorf("Error = %q, want it to contain 'not running'", resp.Error)
	}
}

func TestHandleAgentControlYield_EmptyName(t *testing.T) {
	d := &Daemon{
		config: &DaemonConfig{Daemon: DaemonSettings{}},
		agents: []*AgentProcess{},
	}

	resp := d.handleAgentControlYield("")
	if resp.Success {
		t.Error("Success = true, want false for empty name")
	}
	if !strings.Contains(resp.Error, "required") {
		t.Errorf("Error = %q, want it to contain 'required'", resp.Error)
	}
}

// ---------------------------------------------------------------------------
// buildCommand env var tests
// ---------------------------------------------------------------------------

func TestBuildCommand_YieldFileEnvVar(t *testing.T) {
	tmpDir := t.TempDir()

	d := &Daemon{
		config:     &DaemonConfig{Daemon: DaemonSettings{}},
		projectDir: tmpDir,
	}
	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "plan"},
		roleConfig:   RoleConfig{Description: "Built-in plan agent"},
		worktreePath: tmpDir,
	}

	cmd, err := d.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand error: %v", err)
	}

	wantEnv := "LOOM_YIELD_FILE=" + filepath.Join(tmpDir, YieldFileName)
	found := false
	for _, env := range cmd.Env {
		if env == wantEnv {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("LOOM_YIELD_FILE not found in cmd.Env; want %q", wantEnv)
	}
}

func TestBuildCommand_YieldFileAbsolutePath(t *testing.T) {
	tmpDir := t.TempDir()

	d := &Daemon{
		config:     &DaemonConfig{Daemon: DaemonSettings{}},
		projectDir: tmpDir,
	}
	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "hawk", Role: "task"},
		roleConfig:   RoleConfig{Description: "Built-in task agent"},
		worktreePath: tmpDir,
	}

	cmd, err := d.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand error: %v", err)
	}

	prefix := "LOOM_YIELD_FILE="
	var yieldPath string
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, prefix) {
			yieldPath = strings.TrimPrefix(env, prefix)
			break
		}
	}

	if yieldPath == "" {
		t.Fatal("LOOM_YIELD_FILE not found in cmd.Env")
	}
	if !filepath.IsAbs(yieldPath) {
		t.Errorf("LOOM_YIELD_FILE path %q is not absolute", yieldPath)
	}
}
