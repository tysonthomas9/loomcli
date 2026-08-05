package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// testDaemonState mirrors daemon.DaemonState for test serialization (avoids import cycle).
type testDaemonState struct {
	PID    int `json:"pid"`
	Agents []struct {
		Name   string `json:"name"`
		PID    int    `json:"pid"`
		Status string `json:"status"`
	} `json:"agents"`
}

func stubDaemonProcessIdentity(t *testing.T, match func(int) bool) {
	t.Helper()
	original := daemonProcessIdentityFn
	originalSupported := daemonProcessIdentitySupportedFn
	daemonProcessIdentityFn = match
	daemonProcessIdentitySupportedFn = func() bool { return true }
	t.Cleanup(func() {
		daemonProcessIdentityFn = original
		daemonProcessIdentitySupportedFn = originalSupported
	})
}

// ---------- IsProtectedRuntimePath ----------

func TestIsProtectedRuntimePath(t *testing.T) {
	tests := []struct {
		relPath string
		want    bool
	}{
		// Protected top-level dirs/files
		{".loom", true},
		{".loom/daemon.pid", true},
		{"sessions", true},
		{"sessions/abc", true},
		{"AGENTS.md", true},

		// Not protected
		{"leftover.txt", false},
		{"other/file.go", false},
		{"src/main.go", false},
		{"README.md", false},

		// Normalisation: leading "./" stripped
		{"./sessions", true},
		{"./leftover.txt", false},

		// Edge cases
		{"", false},
		{".", false},
		{"runtime", false},   // no leading dot
		{"sessionsx", false}, // suffix mismatch
		{".loomx", false},    // suffix mismatch
	}

	for _, tc := range tests {
		t.Run(tc.relPath, func(t *testing.T) {
			got := IsProtectedRuntimePath(tc.relPath)
			if got != tc.want {
				t.Errorf("IsProtectedRuntimePath(%q) = %v, want %v", tc.relPath, got, tc.want)
			}
		})
	}
}

// ---------- detectFromLockFile ----------

func TestDetectFromLockFile_NoFile(t *testing.T) {
	info, ok := detectFromLockFile("/nonexistent/daemon.lock")
	if ok {
		t.Errorf("expected ok=false for missing lock file, got true")
	}
	if info.Running {
		t.Errorf("expected Running=false")
	}
}

func TestDetectFromLockFile_NotLocked(t *testing.T) {
	// Lock file exists but nobody holds the flock → not running.
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "daemon.lock")
	if err := os.WriteFile(lockPath, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	info, ok := detectFromLockFile(lockPath)
	if ok {
		t.Errorf("expected ok=false when lock is not held")
	}
	if info.Running {
		t.Errorf("expected Running=false")
	}
}

func TestDetectFromLockFile_LockedLivePID(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "daemon.lock")

	livePID := os.Getpid()
	stubDaemonProcessIdentity(t, func(pid int) bool { return pid == livePID })
	li := lockfile.LockInfo{PID: livePID}
	data, err := json.Marshal(li)
	if err != nil {
		t.Fatal(err)
	}

	// Create file and hold an exclusive flock on it.
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer lockFile.Close()
	if err := lockfile.TryLockExclusive(lockFile); err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}
	// Write lock info after acquiring
	if _, err := lockFile.Write(data); err != nil {
		t.Fatal(err)
	}

	info, ok := detectFromLockFile(lockPath)
	if !ok {
		t.Fatal("expected ok=true for held lock with live PID")
	}
	if !info.Running {
		t.Error("expected Running=true")
	}
	if info.PID != livePID {
		t.Errorf("PID = %d, want %d", info.PID, livePID)
	}
	if info.Source != "lock" {
		t.Errorf("Source = %q, want %q", info.Source, "lock")
	}
}

func TestDetectFromLockFile_LockedDeadPID(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "daemon.lock")

	deadPID := 999999999
	li := lockfile.LockInfo{PID: deadPID}
	data, err := json.Marshal(li)
	if err != nil {
		t.Fatal(err)
	}

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer lockFile.Close()
	if err := lockfile.TryLockExclusive(lockFile); err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}
	if _, err := lockFile.Write(data); err != nil {
		t.Fatal(err)
	}

	info, ok := detectFromLockFile(lockPath)
	// The held flock is authoritative, but the unverified PID must never be
	// returned to a lifecycle command that could signal it.
	if !ok {
		t.Error("expected ok=true for held lock")
	}
	if !info.Running {
		t.Error("expected Running=true for held lock")
	}
	if info.PID != 0 {
		t.Errorf("PID = %d, want 0 for unverified metadata", info.PID)
	}
}

func TestDetectFromLockFile_LockedNoContent(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "daemon.lock")

	// Create empty file and hold the lock
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer lockFile.Close()
	if err := lockfile.TryLockExclusive(lockFile); err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}

	info, ok := detectFromLockFile(lockPath)
	if !ok {
		t.Fatal("expected ok=true for held lock with no content")
	}
	if !info.Running {
		t.Error("expected Running=true")
	}
	if info.Source != "lock" {
		t.Errorf("Source = %q, want %q", info.Source, "lock")
	}
	if info.PID != 0 {
		t.Errorf("PID = %d, want 0 (unknown)", info.PID)
	}
}

// ---------- detectFromStateFile ----------

func TestDetectFromStateFile_NoFile(t *testing.T) {
	info, ok := detectFromStateFile("/nonexistent/daemon-agents.json")
	if ok {
		t.Error("expected ok=false for missing state file")
	}
	if info.Running {
		t.Error("expected Running=false")
	}
}

func TestDetectFromStateFile_LivePID(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "daemon-agents.json")

	livePID := os.Getpid()
	stubDaemonProcessIdentity(t, func(pid int) bool { return pid == livePID })
	state := testDaemonState{PID: livePID}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, data, 0644); err != nil {
		t.Fatal(err)
	}

	info, ok := detectFromStateFile(statePath)
	if !ok {
		t.Fatal("expected ok=true for state file with live PID")
	}
	if !info.Running {
		t.Error("expected Running=true")
	}
	if info.PID != 0 {
		t.Errorf("PID = %d, want 0 because state-only evidence cannot authorize signals", info.PID)
	}
	if info.Source != "state" {
		t.Errorf("Source = %q, want %q", info.Source, "state")
	}
}

func TestDetectFromStateFile_DeadPID(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "daemon-agents.json")

	state := testDaemonState{PID: 999999999}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, data, 0644); err != nil {
		t.Fatal(err)
	}

	info, ok := detectFromStateFile(statePath)
	if ok {
		t.Error("expected ok=false for state file with dead PID")
	}
	if info.Running {
		t.Error("expected Running=false")
	}
}

func TestDetectFromStateFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "daemon-agents.json")

	if err := os.WriteFile(statePath, []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}

	info, ok := detectFromStateFile(statePath)
	if ok {
		t.Error("expected ok=false for invalid JSON")
	}
	if info.Running {
		t.Error("expected Running=false")
	}
}

// ---------- DetectDaemonRuntime ----------

func TestDetectDaemonRuntime_NoFiles(t *testing.T) {
	dir := t.TempDir()
	// Create .loom dir but no files in it
	if err := os.MkdirAll(filepath.Join(dir, ".loom"), 0755); err != nil {
		t.Fatal(err)
	}

	info := DetectDaemonRuntime(dir)
	if info.Running {
		t.Error("expected Running=false when no files exist")
	}
	if info.Source != "" {
		t.Errorf("Source = %q, want empty", info.Source)
	}
}

func TestDetectDaemonRuntime_LockTakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	loomDir := filepath.Join(dir, ".loom")
	if err := os.MkdirAll(loomDir, 0755); err != nil {
		t.Fatal(err)
	}

	livePID := os.Getpid()
	stubDaemonProcessIdentity(t, func(pid int) bool { return pid == livePID })

	// Create lock file with held flock
	lockPath := filepath.Join(loomDir, "daemon.lock")
	li := lockfile.LockInfo{PID: livePID}
	data, _ := json.Marshal(li)

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer lockFile.Close()
	if err := lockfile.TryLockExclusive(lockFile); err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}
	if _, err := lockFile.Write(data); err != nil {
		t.Fatal(err)
	}

	// Also create state file with same live PID
	statePath := filepath.Join(loomDir, "daemon-agents.json")
	stateData, _ := json.Marshal(testDaemonState{PID: livePID})
	if err := os.WriteFile(statePath, stateData, 0644); err != nil {
		t.Fatal(err)
	}

	info := DetectDaemonRuntime(dir)
	if !info.Running {
		t.Fatal("expected Running=true")
	}
	if info.Source != "lock" {
		t.Errorf("expected Source=%q (lock takes precedence), got %q", "lock", info.Source)
	}
	if info.PID != livePID {
		t.Errorf("PID = %d, want held-lock owner %d", info.PID, livePID)
	}
}

func TestDetectDaemonRuntime_StateFallback(t *testing.T) {
	dir := t.TempDir()
	loomDir := filepath.Join(dir, ".loom")
	if err := os.MkdirAll(loomDir, 0755); err != nil {
		t.Fatal(err)
	}

	livePID := os.Getpid()
	stubDaemonProcessIdentity(t, func(pid int) bool { return pid == livePID })

	// No lock file — state file with live PID
	statePath := filepath.Join(loomDir, "daemon-agents.json")
	stateData, _ := json.Marshal(testDaemonState{PID: livePID})
	if err := os.WriteFile(statePath, stateData, 0644); err != nil {
		t.Fatal(err)
	}

	info := DetectDaemonRuntime(dir)
	if !info.Running {
		t.Fatal("expected Running=true via state file")
	}
	if info.Source != "state" {
		t.Errorf("Source = %q, want %q", info.Source, "state")
	}
	if info.PID != 0 {
		t.Errorf("PID = %d, want 0 because state-only evidence cannot authorize signals", info.PID)
	}
}

func TestDetectDaemonRuntime_UnlockedLockRejectsStaleStatePID(t *testing.T) {
	dir := t.TempDir()
	loomDir := filepath.Join(dir, ".loom")
	if err := os.MkdirAll(loomDir, 0755); err != nil {
		t.Fatal(err)
	}

	// An existing unlocked lock is authoritative not-running evidence.
	lockPath := filepath.Join(loomDir, "daemon.lock")
	if err := os.WriteFile(lockPath, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	// State file with live PID
	livePID := os.Getpid()
	statePath := filepath.Join(loomDir, "daemon-agents.json")
	stateData, _ := json.Marshal(testDaemonState{PID: livePID})
	if err := os.WriteFile(statePath, stateData, 0644); err != nil {
		t.Fatal(err)
	}

	stubDaemonProcessIdentity(t, func(pid int) bool { return pid == livePID })
	info := DetectDaemonRuntime(dir)
	if info.Running {
		t.Fatal("unlocked lock fell through to stale state PID")
	}
	if info.PID != 0 || info.Source != "" {
		t.Fatalf("info = %+v, want authoritative not-running result", info)
	}
}

func TestDetectFromStateFileRejectsForeignLivePID(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "daemon-agents.json")
	stateData, _ := json.Marshal(testDaemonState{PID: os.Getpid()})
	if err := os.WriteFile(statePath, stateData, 0o644); err != nil {
		t.Fatal(err)
	}
	stubDaemonProcessIdentity(t, func(int) bool { return false })

	info, ok := detectFromStateFile(statePath)
	if ok || info.Running || info.PID != 0 {
		t.Fatalf("foreign live PID was accepted from state: info=%+v ok=%v", info, ok)
	}
}

func TestDetectDaemonRuntime_DeadStateNotRunning(t *testing.T) {
	dir := t.TempDir()
	loomDir := filepath.Join(dir, ".loom")
	if err := os.MkdirAll(loomDir, 0755); err != nil {
		t.Fatal(err)
	}

	deadPID := 999999999

	// State file with dead PID
	statePath := filepath.Join(loomDir, "daemon-agents.json")
	stateData, _ := json.Marshal(testDaemonState{PID: deadPID})
	if err := os.WriteFile(statePath, stateData, 0644); err != nil {
		t.Fatal(err)
	}

	info := DetectDaemonRuntime(dir)
	if info.Running {
		t.Fatal("expected Running=false for dead state")
	}
	if info.Source != "" {
		t.Errorf("Source = %q, want empty", info.Source)
	}
}

func TestDetectDaemonRuntime_AllDeadNotRunning(t *testing.T) {
	dir := t.TempDir()
	loomDir := filepath.Join(dir, ".loom")
	if err := os.MkdirAll(loomDir, 0755); err != nil {
		t.Fatal(err)
	}

	deadPID := 999999999

	// Stale lock file (not held)
	lockPath := filepath.Join(loomDir, "daemon.lock")
	if err := os.WriteFile(lockPath, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	// State file with dead PID
	statePath := filepath.Join(loomDir, "daemon-agents.json")
	stateData, _ := json.Marshal(testDaemonState{PID: deadPID})
	if err := os.WriteFile(statePath, stateData, 0644); err != nil {
		t.Fatal(err)
	}

	info := DetectDaemonRuntime(dir)
	if info.Running {
		t.Error("expected Running=false when all sources have dead PID")
	}
	if info.Source != "" {
		t.Errorf("Source = %q, want empty", info.Source)
	}
}
