package cli

import (
	"os"
	"path/filepath"
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

	data, err := buildWorkspaceInfo()
	if err != nil {
		t.Fatalf("buildWorkspaceInfo() error: %v", err)
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

	data, err := buildWorkspaceInfo()
	if err != nil {
		t.Fatalf("buildWorkspaceInfo() error: %v", err)
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

	data, err := buildWorkspaceInfo()
	if err != nil {
		t.Fatalf("buildWorkspaceInfo() error: %v", err)
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

	data, err := buildWorkspaceInfo()
	if err != nil {
		t.Fatalf("buildWorkspaceInfo() error: %v", err)
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
