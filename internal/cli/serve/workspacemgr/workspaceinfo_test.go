package workspacemgr

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLoomConfig writes a YAML config string to config.yaml in dir.
func writeLoomConfig(t *testing.T, dir, yamlContent string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestBuildWorkspaceInfo_DefaultSortAlphabetical(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	writeLoomConfig(t, dir, `
version: 1
default_workspace: gamma
workspaces:
  gamma:
    path: /tmp/gamma
    repos:
      - name: repo1
        path: /tmp/gamma/repo1
  alpha:
    path: /tmp/alpha
    repos:
      - name: repo2
        path: /tmp/alpha/repo2
  beta:
    path: /tmp/beta
    repos:
      - name: repo3
        path: /tmp/beta/repo3
`)

	data, err := BuildWorkspaceInfo()
	if err != nil {
		t.Fatalf("BuildWorkspaceInfo() error: %v", err)
	}
	if data == nil {
		t.Fatal("expected non-nil WorkspaceData")
	}

	if len(data.Workspaces) != 3 {
		t.Fatalf("expected 3 workspace summaries, got %d", len(data.Workspaces))
	}

	// With no WorkspaceOrder, workspaces should be sorted alphabetically
	expectedOrder := []string{"alpha", "beta", "gamma"}
	for i, name := range expectedOrder {
		if data.Workspaces[i].Name != name {
			t.Errorf("Workspaces[%d].Name = %q, want %q", i, data.Workspaces[i].Name, name)
		}
	}
}

func TestBuildWorkspaceInfo_CustomOrderAllNames(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	writeLoomConfig(t, dir, `
version: 1
default_workspace: alpha
workspace_order:
  - gamma
  - alpha
  - beta
workspaces:
  alpha:
    path: /tmp/alpha
    repos:
      - name: repo1
        path: /tmp/alpha/repo1
  beta:
    path: /tmp/beta
    repos:
      - name: repo2
        path: /tmp/beta/repo2
  gamma:
    path: /tmp/gamma
    repos:
      - name: repo3
        path: /tmp/gamma/repo3
`)

	data, err := BuildWorkspaceInfo()
	if err != nil {
		t.Fatalf("BuildWorkspaceInfo() error: %v", err)
	}
	if data == nil {
		t.Fatal("expected non-nil WorkspaceData")
	}

	if len(data.Workspaces) != 3 {
		t.Fatalf("expected 3 workspace summaries, got %d", len(data.Workspaces))
	}

	// WorkspaceOrder specifies all names, so the sort should match exactly
	expectedOrder := []string{"gamma", "alpha", "beta"}
	for i, name := range expectedOrder {
		if data.Workspaces[i].Name != name {
			t.Errorf("Workspaces[%d].Name = %q, want %q", i, data.Workspaces[i].Name, name)
		}
	}

	// WorkspaceOrder should be echoed back in the response
	if len(data.WorkspaceOrder) != 3 {
		t.Fatalf("expected WorkspaceOrder len 3, got %d", len(data.WorkspaceOrder))
	}
	for i, name := range expectedOrder {
		if data.WorkspaceOrder[i] != name {
			t.Errorf("WorkspaceOrder[%d] = %q, want %q", i, data.WorkspaceOrder[i], name)
		}
	}
}

func TestBuildWorkspaceInfo_PartialOrder(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	writeLoomConfig(t, dir, `
version: 1
default_workspace: alpha
workspace_order:
  - gamma
workspaces:
  alpha:
    path: /tmp/alpha
    repos:
      - name: repo1
        path: /tmp/alpha/repo1
  beta:
    path: /tmp/beta
    repos:
      - name: repo2
        path: /tmp/beta/repo2
  gamma:
    path: /tmp/gamma
    repos:
      - name: repo3
        path: /tmp/gamma/repo3
  delta:
    path: /tmp/delta
    repos:
      - name: repo4
        path: /tmp/delta/repo4
`)

	data, err := BuildWorkspaceInfo()
	if err != nil {
		t.Fatalf("BuildWorkspaceInfo() error: %v", err)
	}
	if data == nil {
		t.Fatal("expected non-nil WorkspaceData")
	}

	if len(data.Workspaces) != 4 {
		t.Fatalf("expected 4 workspace summaries, got %d", len(data.Workspaces))
	}

	// gamma is listed first (it's in the order), then the rest alphabetically
	if data.Workspaces[0].Name != "gamma" {
		t.Errorf("Workspaces[0].Name = %q, want %q (ordered item first)", data.Workspaces[0].Name, "gamma")
	}
	// Remaining: alpha, beta, delta in alphabetical order
	remaining := []string{"alpha", "beta", "delta"}
	for i, name := range remaining {
		if data.Workspaces[i+1].Name != name {
			t.Errorf("Workspaces[%d].Name = %q, want %q (unordered item alphabetical)", i+1, data.Workspaces[i+1].Name, name)
		}
	}
}

func TestBuildWorkspaceInfo_StaleOrderNames(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	writeLoomConfig(t, dir, `
version: 1
default_workspace: alpha
workspace_order:
  - deleted-ws
  - alpha
  - also-deleted
workspaces:
  alpha:
    path: /tmp/alpha
    repos:
      - name: repo1
        path: /tmp/alpha/repo1
  beta:
    path: /tmp/beta
    repos:
      - name: repo2
        path: /tmp/beta/repo2
  gamma:
    path: /tmp/gamma
    repos:
      - name: repo3
        path: /tmp/gamma/repo3
`)

	data, err := BuildWorkspaceInfo()
	if err != nil {
		t.Fatalf("BuildWorkspaceInfo() error: %v", err)
	}
	if data == nil {
		t.Fatal("expected non-nil WorkspaceData")
	}

	if len(data.Workspaces) != 3 {
		t.Fatalf("expected 3 workspace summaries, got %d", len(data.Workspaces))
	}

	// "alpha" is the only valid name in order, so it comes first.
	// "deleted-ws" and "also-deleted" are stale and don't affect sorting.
	// Remaining (beta, gamma) sorted alphabetically after.
	if data.Workspaces[0].Name != "alpha" {
		t.Errorf("Workspaces[0].Name = %q, want %q (only valid ordered item)", data.Workspaces[0].Name, "alpha")
	}
	if data.Workspaces[1].Name != "beta" {
		t.Errorf("Workspaces[1].Name = %q, want %q", data.Workspaces[1].Name, "beta")
	}
	if data.Workspaces[2].Name != "gamma" {
		t.Errorf("Workspaces[2].Name = %q, want %q", data.Workspaces[2].Name, "gamma")
	}
}

// captureSlog swaps the default slog logger for a text handler writing to buf
// for the duration of fn, then restores the original. Returns buf output.
func captureSlog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(orig)
	fn()
	return buf.String()
}

// TestBuildWorkspaceInfoForID_NameFallback verifies that when no workspace has
// a matching UUID, BuildWorkspaceInfoForID falls back to matching the map key
// (workspace name) and emits a warn-level slog entry.
func TestBuildWorkspaceInfoForID_NameFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	// Pre-UUID config: workspace "legacy" has no ID set, so UUID match fails.
	writeLoomConfig(t, dir, `
version: 1
default_workspace: legacy
workspaces:
  legacy:
    path: /tmp/legacy
    repos:
      - name: repo1
        path: /tmp/legacy/repo1
`)

	var data *ops_WorkspaceData_shim
	logOutput := captureSlog(t, func() {
		d, err := BuildWorkspaceInfoForID("legacy")
		if err != nil {
			t.Fatalf("BuildWorkspaceInfoForID(%q) error: %v", "legacy", err)
		}
		if d == nil {
			t.Fatal("expected non-nil WorkspaceData from name fallback")
		}
		// Capture fields we care about through a local shim alias to avoid
		// depending on the full ops package surface.
		data = &ops_WorkspaceData_shim{Name: d.Name}
	})

	if data == nil {
		t.Fatal("no data captured")
	}
	if data.Name != "legacy" {
		t.Errorf("data.Name = %q, want %q", data.Name, "legacy")
	}

	if !strings.Contains(logOutput, "resolved by name") {
		t.Errorf("expected warn log containing %q, got: %s", "resolved by name", logOutput)
	}
	if !strings.Contains(logOutput, "level=WARN") {
		t.Errorf("expected WARN level in log output, got: %s", logOutput)
	}
}

// TestBuildWorkspaceInfoForID_UUIDMatchPreferred verifies that the UUID match
// wins over a name match, and that no fallback warn log is emitted.
func TestBuildWorkspaceInfoForID_UUIDMatchPreferred(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	// "real-ws" has the UUID we look up. "aaaa-bbbb-cccc" is the map-key name
	// of a second workspace that matches the UUID string verbatim — the UUID
	// match on "real-ws" must still take priority over the name match.
	writeLoomConfig(t, dir, `
version: 1
default_workspace: real-ws
workspaces:
  real-ws:
    id: aaaa-bbbb-cccc
    path: /tmp/real-ws
    repos:
      - name: repo1
        path: /tmp/real-ws/repo1
  aaaa-bbbb-cccc:
    path: /tmp/trap
    repos:
      - name: repo2
        path: /tmp/trap/repo2
`)

	var capturedName string
	logOutput := captureSlog(t, func() {
		d, err := BuildWorkspaceInfoForID("aaaa-bbbb-cccc")
		if err != nil {
			t.Fatalf("BuildWorkspaceInfoForID(UUID) error: %v", err)
		}
		if d == nil {
			t.Fatal("expected non-nil WorkspaceData for UUID match")
		}
		capturedName = d.Name
	})

	if capturedName != "real-ws" {
		t.Errorf("workspace name = %q, want %q (UUID match should win over name match)",
			capturedName, "real-ws")
	}

	if strings.Contains(logOutput, "resolved by name") {
		t.Errorf("unexpected fallback warn log on UUID match: %s", logOutput)
	}
}

// TestBuildWorkspaceInfoForID_NotFound verifies the function returns an error
// when the target matches neither a UUID nor a workspace name.
func TestBuildWorkspaceInfoForID_NotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	writeLoomConfig(t, dir, `
version: 1
default_workspace: alpha
workspaces:
  alpha:
    id: 1111-2222-3333
    path: /tmp/alpha
    repos:
      - name: repo1
        path: /tmp/alpha/repo1
`)

	d, err := BuildWorkspaceInfoForID("does-not-exist")
	if err == nil {
		t.Fatalf("BuildWorkspaceInfoForID(nonexistent) returned nil error, data=%+v", d)
	}
	if d != nil {
		t.Errorf("expected nil data on not-found, got %+v", d)
	}
	if !strings.Contains(err.Error(), "workspace not found") {
		t.Errorf("error message = %q, want substring %q", err.Error(), "workspace not found")
	}
}

// ops_WorkspaceData_shim is a minimal struct used only to carry fields out of
// the captureSlog closure without importing ops into the test's local scope.
// (The real return type from BuildWorkspaceInfoForID is *ops.WorkspaceData;
// we only need .Name here.)
type ops_WorkspaceData_shim struct {
	Name string
}
