package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// contractFixture mirrors the shape of the real workspace contract: several
// repos with local_integration, one without, and one whose declared worktree
// does not exist on disk.
const contractFixture = `# PUPPET integration contract.
defaults:
  target_branch: main
  push: branch

repos:
  loomcli:
    target_branch: v5
    gate_command: make check
    local_integration:
      branch: local/union
      base: origin/v5
      clone: CLONE_A
      worktree: WT_A
      deployed: true
  fleet-db:
    target_branch: main
    local_integration:
      branch: local/union
      clone: CLONE_B
      worktree: WT_B
  harness-wrapper:
    target_branch: main
    local_integration:
      branch: local/union
      worktree: MISSING
  meta-harness:
    target_branch: main
`

func writeContract(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "integration.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write contract: %v", err)
	}
	return path
}

func TestSharedWorktreesParsesDeclaredEntries(t *testing.T) {
	wtA := t.TempDir()
	wtB := t.TempDir()
	body := strings.NewReplacer(
		"WT_A", wtA, "WT_B", wtB,
		"MISSING", filepath.Join(t.TempDir(), "gone"),
		"CLONE_A", "/clones/loomcli", "CLONE_B", "/clones/fleet-db",
	).Replace(contractFixture)

	got, err := sharedWorktreesFrom(writeContract(t, body))
	if err != nil {
		t.Fatalf("sharedWorktreesFrom: %v", err)
	}
	// harness-wrapper is dropped (worktree missing on disk); meta-harness has
	// no local_integration at all.
	if len(got) != 2 {
		t.Fatalf("got %d shared worktrees, want 2: %+v", len(got), got)
	}
	// Sorted by repo name: fleet-db before loomcli.
	if got[0].Repo != "fleet-db" || got[1].Repo != "loomcli" {
		t.Fatalf("unexpected order: %+v", got)
	}
	if got[1].Path != wtA || got[1].Branch != "local/union" || got[1].Clone != "/clones/loomcli" {
		t.Fatalf("loomcli entry wrong: %+v", got[1])
	}
	if got[0].Path != wtB {
		t.Fatalf("fleet-db path = %q, want %q", got[0].Path, wtB)
	}
}

// An install with no contract has no shared worktrees. That is a fact, not a
// failure — every caller is a health check that must stay silent there.
func TestSharedWorktreesAbsentFileIsNotAnError(t *testing.T) {
	got, err := sharedWorktreesFrom(filepath.Join(t.TempDir(), "integration.yaml"))
	if err != nil {
		t.Fatalf("absent contract returned an error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestSharedWorktreesMalformedYAMLIsAnError(t *testing.T) {
	path := writeContract(t, "repos:\n  loomcli:\n   - this is not a mapping\n\t\tbad indent\n")
	if _, err := sharedWorktreesFrom(path); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestSharedWorktreesSkipsEmptyWorktreeField(t *testing.T) {
	path := writeContract(t, "repos:\n  loomcli:\n    local_integration:\n      branch: local/union\n      worktree: \"\"\n")
	got, err := sharedWorktreesFrom(path)
	if err != nil {
		t.Fatalf("sharedWorktreesFrom: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no entries, got %+v", got)
	}
}

func TestContractPathHonorsEnvOverride(t *testing.T) {
	t.Setenv("LOOM_INTEGRATION_CONTRACT", "/tmp/contract470.yaml")
	if got := ContractPath(); got != "/tmp/contract470.yaml" {
		t.Fatalf("ContractPath() = %q", got)
	}
	t.Setenv("LOOM_INTEGRATION_CONTRACT", "")
	if got := ContractPath(); !strings.HasSuffix(got, "integration.yaml") {
		t.Fatalf("ContractPath() = %q, want a path ending in integration.yaml", got)
	}
}

// SharedWorktrees is the exported entry point the doctor check and the
// supervisor hook call; it must honor the override end to end.
func TestSharedWorktreesUsesContractPath(t *testing.T) {
	wt := t.TempDir()
	body := strings.NewReplacer("WT_A", wt, "WT_B", wt,
		"MISSING", filepath.Join(t.TempDir(), "gone"),
		"CLONE_A", "a", "CLONE_B", "b").Replace(contractFixture)
	t.Setenv("LOOM_INTEGRATION_CONTRACT", writeContract(t, body))

	got, err := SharedWorktrees()
	if err != nil {
		t.Fatalf("SharedWorktrees: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
}
