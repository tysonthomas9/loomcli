//go:build e2e
// +build e2e

package cleanup

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildLoomForUsage builds the loom binary FROM THIS TREE. It deliberately
// does not use whatever `loom` is on PATH: that is the deployed build, and an
// e2e test that ran it would be reporting on someone else's binary.
//
// These tests live beside the command rather than in internal/cli (where the
// other *_e2e_test.go files sit) because that package's e2e-tagged build does
// not compile on v5 — a test added there could never be run.
func buildLoomForUsage(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "loom")
	cmd := exec.Command("go", "build", "-o", bin, "../../../cmd/loom")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build loom for e2e (%v):\n%s", err, out)
	}
	return bin
}

// runLoomUsage runs `loom usage` against a runtime dir holding the fixtures.
// stdout is returned separately from stderr: the binary logs to stderr on
// startup, and mixing the two would corrupt --format json output.
func runLoomUsage(t *testing.T, bin, runtimeDir string, args ...string) (stdout string, code int) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"usage"}, args...)...)
	cmd.Env = append(os.Environ(), "LOOM_WORKSPACE_RUNTIME_DIR="+runtimeDir)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		code = ee.ExitCode()
	case err != nil:
		t.Fatalf("run loom usage: %v\nstderr: %s", err, errBuf.String())
	}
	if code != 0 {
		t.Logf("loom usage stderr:\n%s", errBuf.String())
	}
	return outBuf.String(), code
}

// usageFixtureDir writes both ledgers with deliberately DISAGREEING data, so
// the test can tell which one `loom usage` actually read.
func usageFixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	sessDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	lines := []map[string]any{
		// One session written twice (running, then finalized) — the real
		// append-only shape. Only the finalized record must count.
		{
			"session_id": "e2e1", "agent_name": "sagent", "backend": "claude",
			"status": "running", "started_at": "2026-08-28T10:00:00Z", "input_tokens": 1,
		},
		{
			"session_id": "e2e1", "agent_name": "sagent", "backend": "claude",
			"task_id": "T-1", "status": "completed",
			"started_at": "2026-08-28T10:00:00Z", "ended_at": "2026-08-28T10:30:00Z",
			"duration_s": 1800, "input_tokens": 123000, "output_tokens": 4000,
		},
		{
			"session_id": "e2e2", "agent_name": "sagent", "backend": "claude",
			"task_id": "T-2", "status": "failed", "exit_code": 1,
			"started_at": "2026-08-29T09:00:00Z", "ended_at": "2026-08-29T09:00:05Z",
			"duration_s": 5, "input_tokens": 0, "output_tokens": 0,
		},
	}
	var sb strings.Builder
	for _, l := range lines {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatalf("marshal index line: %v", err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(sessDir, "index.jsonl"), []byte(sb.String()), 0o600); err != nil {
		t.Fatalf("write index.jsonl: %v", err)
	}

	legacy := `{"agent_name":"lagent","backend":"codex","input_tokens":7,` +
		`"output_tokens":9,"estimated_cost_usd":0,` +
		`"started_at":"2026-08-28T10:00:00Z","ended_at":"2026-08-28T10:05:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "usage.jsonl"), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write usage.jsonl: %v", err)
	}
	return dir
}

func TestUsageCmdE2E_DefaultsToSessionsLedger(t *testing.T) {
	bin := buildLoomForUsage(t)
	dir := usageFixtureDir(t)

	out, code := runLoomUsage(t, bin, dir)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}

	if !strings.Contains(out, "sagent") {
		t.Errorf("default source should read the session index, got:\n%s", out)
	}
	if strings.Contains(out, "lagent") {
		t.Errorf("default source must not read usage.jsonl, got:\n%s", out)
	}
	// 123,000 proves both that tokens are non-zero and that the duplicated
	// index entry was deduped (a double-count would read 246,000).
	if !strings.Contains(out, "123,000") {
		t.Errorf("expected the deduped input total 123,000, got:\n%s", out)
	}
	if !strings.Contains(out, "index.jsonl") {
		t.Errorf("header should name the ledger it read, got:\n%s", out)
	}
	if !strings.Contains(out, "2 sessions") {
		t.Errorf("header should carry the record count, got:\n%s", out)
	}
	if !strings.Contains(out, "not reported by backend") {
		t.Errorf("an all-zero cost set should say so, got:\n%s", out)
	}
	if strings.Contains(out, "$0.00") {
		t.Errorf("a cost nobody reported must not render as $0.00, got:\n%s", out)
	}
}

func TestUsageCmdE2E_SourceLegacy(t *testing.T) {
	bin := buildLoomForUsage(t)
	dir := usageFixtureDir(t)

	out, code := runLoomUsage(t, bin, dir, "--source", "legacy")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "lagent") {
		t.Errorf("--source legacy should read usage.jsonl, got:\n%s", out)
	}
	if strings.Contains(out, "sagent") {
		t.Errorf("--source legacy must not read the session index, got:\n%s", out)
	}
}

func TestUsageCmdE2E_StatusFilter(t *testing.T) {
	bin := buildLoomForUsage(t)
	dir := usageFixtureDir(t)

	out, code := runLoomUsage(t, bin, dir, "--status", "failed", "--format", "json")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	var resp struct {
		SessionCount int `json:"session_count"`
		Sessions     []struct {
			SessionID string `json:"session_id"`
			Status    string `json:"status"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal json output: %v\n%s", err, out)
	}
	if resp.SessionCount != 1 || resp.Sessions[0].SessionID != "e2e2" {
		t.Fatalf("--status failed returned %+v, want only e2e2", resp)
	}
	if resp.Sessions[0].Status != "failed" {
		t.Errorf("status field not carried through: %+v", resp.Sessions[0])
	}
}

func TestUsageCmdE2E_EmptyLedgerNamesTheFile(t *testing.T) {
	bin := buildLoomForUsage(t)
	dir := t.TempDir()

	out, code := runLoomUsage(t, bin, dir)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, filepath.Join("sessions", "index.jsonl")) {
		t.Errorf("empty message should name the ledger, got:\n%s", out)
	}
	if !strings.Contains(out, "--source legacy") {
		t.Errorf("empty message should suggest --source legacy, got:\n%s", out)
	}
}
