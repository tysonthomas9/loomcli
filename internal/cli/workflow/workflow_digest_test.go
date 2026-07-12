package workflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/workflows"
)

func runDigestCommand(t *testing.T, args []string) string {
	t.Helper()
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runWorkflowDigest(cmd, args); err != nil {
		t.Fatalf("runWorkflowDigest(%v): %v", args, err)
	}
	return strings.TrimSpace(out.String())
}

// TestWorkflowDigestMatchesEmbeddedSelfHealDigest is the golden parity test
// for the unified digest recipe: the digest the register path stamps (this
// CLI command, called by the e2e stack scripts to feed
// `loom driver register --source-digest`) must equal the digest serve's
// builtin self-heal computes over the same source tree
// (workflows.SourceDigest(spec.Files) in EnsureBuiltinWorkflow). If these
// ever diverge, out-of-band registrations can never hit the self-heal's
// exact-match fast path and serve logs digest drift on every workflow run.
func TestWorkflowDigestMatchesEmbeddedSelfHealDigest(t *testing.T) {
	for _, name := range workflows.BuiltinWorkflowNames() {
		spec, ok := workflows.BuiltinWorkflow(name)
		if !ok {
			t.Fatalf("builtin %q missing", name)
		}
		want := workflows.SourceDigest(spec.Files)
		got := runDigestCommand(t, []string{name})
		if got != want {
			t.Fatalf("register-path digest for %q = %s, embedded self-heal digest = %s; recipes diverged", name, got, want)
		}
		if !strings.HasPrefix(got, "sha256:") || len(got) != len("sha256:")+64 {
			t.Fatalf("digest for %q has unexpected shape: %q", name, got)
		}
	}
}

// TestWorkflowDigestRecipePinned recomputes the canonical recipe by hand
// (normalized slash keys, ascending key order, key NUL content NUL framing,
// "sha256:"+hex) so an accidental recipe change in EITHER path fails loudly
// rather than silently re-introducing a register/self-heal mismatch.
func TestWorkflowDigestRecipePinned(t *testing.T) {
	spec, ok := workflows.BuiltinWorkflow(workflows.BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("epic-runner builtin missing")
	}
	keys := make([]string, 0, len(spec.Files))
	for key := range spec.Files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		hash.Write([]byte(key))
		hash.Write([]byte{0})
		hash.Write([]byte(spec.Files[key]))
		hash.Write([]byte{0})
	}
	want := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if got := runDigestCommand(t, []string{workflows.BuiltinEpicRunnerWorkflowName}); got != want {
		t.Fatalf("digest = %s, hand-computed canonical recipe = %s", got, want)
	}
}

func TestWorkflowDigestJSONOutput(t *testing.T) {
	workflowDigestJSON = true
	t.Cleanup(func() { workflowDigestJSON = false })
	raw := runDigestCommand(t, []string{workflows.BuiltinEpicRunnerWorkflowName})
	var payload struct {
		Workflow     string `json:"workflow"`
		SourceDigest string `json:"source_digest"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode --json output %q: %v", raw, err)
	}
	spec, _ := workflows.BuiltinWorkflow(workflows.BuiltinEpicRunnerWorkflowName)
	if payload.Workflow != workflows.BuiltinEpicRunnerWorkflowName || payload.SourceDigest != workflows.SourceDigest(spec.Files) {
		t.Fatalf("unexpected --json payload: %+v", payload)
	}
}

func TestWorkflowDigestUnknownWorkflowErrors(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	err := runWorkflowDigest(cmd, []string{"definitely-not-a-workflow"})
	if err == nil || !strings.Contains(err.Error(), "unknown built-in workflow") {
		t.Fatalf("err = %v, want unknown built-in workflow error", err)
	}
}
