package scout

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agentstate"
)

func TestScoutCommandsDefaultAgentFromCatalog(t *testing.T) {
	want := defaultScoutAgentID()
	if want != "scout" {
		t.Fatalf("catalog default scout ID = %q, want scout", want)
	}
	if got := scoutDiffCmd.Flags().Lookup("agent").DefValue; got != want {
		t.Fatalf("scout diff --agent default = %q, want %q", got, want)
	}
	if got := scoutApproveCmd.Flags().Lookup("agent").DefValue; got != want {
		t.Fatalf("scout approve --agent default = %q, want %q", got, want)
	}
}

func TestScoutDiffReadsSelectedAgentPendingFile(t *testing.T) {
	root := t.TempDir()
	out := withScoutFiles(t, root)
	scoutDiffAgentID = "scout-west"
	current := "human\n<!-- loom:agent:scout-west:begin -->\nold west\n<!-- loom:agent:scout-west:end -->\n"
	pending := strings.Replace(current, "old west", "new west", 1)
	writeScoutFile(t, filepath.Join(root, "agents.md"), current)
	writeScoutFile(t, agentstate.PendingAgentsPath(root, "scout-west"), pending)
	writeScoutFile(t, agentstate.PendingAgentsPath(root, "scout"), strings.Replace(current, "old west", "wrong default", 1))

	if err := runScoutDiff(nil, nil); err != nil {
		t.Fatalf("runScoutDiff: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "+new west") || strings.Contains(got, "wrong default") {
		t.Fatalf("diff output = %q, want selected agent pending content only", got)
	}
}

func TestScoutApproveMergesSelectedFencePreservingOtherInstanceAndClearsPending(t *testing.T) {
	root := t.TempDir()
	out := withScoutFiles(t, root)
	scoutApproveAgentID = "scout-west"
	westBegin, westEnd := agentstate.FenceMarkers("scout-west")
	eastBegin, eastEnd := agentstate.FenceMarkers("scout-east")
	other := eastBegin + "\nEAST\r\nbyte-exact\n" + eastEnd
	current := "human-prefix\n" + other + "\n" + westBegin + "\nold west\n" + westEnd +
		"\nduplicate-gap\n" + westBegin + "\nstale duplicate\n" + westEnd + "\nhuman-suffix\n"
	pending := "human-prefix\n" + other + "\n" + westBegin + "\nnew west\n" + westEnd + "\nhuman-suffix\n"
	currentPath := filepath.Join(root, "agents.md")
	pendingPath := agentstate.PendingAgentsPath(root, "scout-west")
	writeScoutFile(t, currentPath, current)
	writeScoutFile(t, pendingPath, pending)

	if err := runScoutApprove(nil, nil); err != nil {
		t.Fatalf("runScoutApprove: %v", err)
	}
	approved, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatalf("read approved agents.md: %v", err)
	}
	got := string(approved)
	if !strings.Contains(got, "new west") || strings.Contains(got, "old west") || strings.Contains(got, "stale duplicate") {
		t.Fatalf("approved agents.md = %q", got)
	}
	if strings.Count(got, westBegin) != 1 {
		t.Fatalf("west begin count = %d, want 1: %q", strings.Count(got, westBegin), got)
	}
	if !strings.Contains(got, other) {
		t.Fatalf("other instance region changed or missing: %q", got)
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("pending file still exists: %v", err)
	}
	if !strings.Contains(out.String(), "scout-west") {
		t.Fatalf("approve output = %q", out.String())
	}
}

func TestScoutApproveUsesDefaultAgentWhenFlagValueIsEmpty(t *testing.T) {
	root := t.TempDir()
	withScoutFiles(t, root)
	scoutApproveAgentID = ""
	begin, end := agentstate.FenceMarkers("scout")
	writeScoutFile(t, filepath.Join(root, "agents.md"), "human\n")
	writeScoutFile(t, agentstate.PendingAgentsPath(root, "scout"), "human\n"+begin+"\ndefault content\n"+end+"\n")

	if err := runScoutApprove(nil, nil); err != nil {
		t.Fatalf("runScoutApprove default: %v", err)
	}
	approved, _ := os.ReadFile(filepath.Join(root, "agents.md"))
	if !strings.Contains(string(approved), "default content") {
		t.Fatalf("approved agents.md = %q", approved)
	}
}

func withScoutFiles(t *testing.T, root string) *bytes.Buffer {
	t.Helper()
	oldDir, oldOutput := scoutWorkspaceDir, scoutOutput
	oldDiff, oldApprove := scoutDiffAgentID, scoutApproveAgentID
	out := &bytes.Buffer{}
	scoutWorkspaceDir = func() string { return root }
	scoutOutput = out
	scoutDiffAgentID, scoutApproveAgentID = defaultScoutAgentID(), defaultScoutAgentID()
	t.Cleanup(func() {
		scoutWorkspaceDir, scoutOutput = oldDir, oldOutput
		scoutDiffAgentID, scoutApproveAgentID = oldDiff, oldApprove
	})
	return out
}

func writeScoutFile(t *testing.T, target, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(target), err)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", target, err)
	}
}
