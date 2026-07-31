package workflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	workflows "github.com/tysonthomas9/loomcli/internal/infra/workflowdistribution"
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

func mustWorkflowSourceDigest(t testing.TB, files map[string]string) string {
	t.Helper()
	digest, err := workflows.SourceDigest(files)
	if err != nil {
		t.Fatalf("SourceDigest: %v", err)
	}
	return digest
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
		want := mustWorkflowSourceDigest(t, spec.Files)
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
	if payload.Workflow != workflows.BuiltinEpicRunnerWorkflowName || payload.SourceDigest != mustWorkflowSourceDigest(t, spec.Files) {
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

// stageSpecFiles writes every spec source to dir and returns --file pairs
// mapping each spec key to its staged copy (mirroring what the e2e stack
// scripts stage into the container before registering).
func stageSpecFiles(t *testing.T, spec workflows.Spec) []string {
	t.Helper()
	dir := t.TempDir()
	pairs := make([]string, 0, len(spec.Files))
	i := 0
	for key, content := range spec.Files {
		path := filepath.Join(dir, fmt.Sprintf("staged-%d.mjs", i))
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("stage %s: %v", key, err)
		}
		pairs = append(pairs, key+"="+path)
		i++
	}
	return pairs
}

// TestWorkflowDigestFileOverridesMatchEmbedded: staged bytes identical to the
// embedded sources must produce the embedded digest — the register flow's
// stamped digest then hits the self-heal's exact-match fast path.
func TestWorkflowDigestFileOverridesMatchEmbedded(t *testing.T) {
	spec, ok := workflows.BuiltinWorkflow(workflows.BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("epic-runner builtin missing")
	}
	workflowDigestFiles = stageSpecFiles(t, spec)
	t.Cleanup(func() { workflowDigestFiles = nil })
	got := runDigestCommand(t, []string{workflows.BuiltinEpicRunnerWorkflowName})
	if want := mustWorkflowSourceDigest(t, spec.Files); got != want {
		t.Fatalf("staged digest = %s, embedded = %s; identical bytes must produce identical digests", got, want)
	}
}

// TestWorkflowDigestFileOverridesAttestStagedBytes: modified staged content
// must produce a DIFFERENT digest than the embedded sources — the stamped
// digest attests what is actually shipped, never mislabeling modified sources
// as the embedded version.
func TestWorkflowDigestFileOverridesAttestStagedBytes(t *testing.T) {
	spec, ok := workflows.BuiltinWorkflow(workflows.BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("epic-runner builtin missing")
	}
	pairs := stageSpecFiles(t, spec)
	// Tamper with the first staged file.
	path := strings.SplitN(pairs[0], "=", 2)[1]
	if err := os.WriteFile(path, []byte("// modified\n"), 0o644); err != nil {
		t.Fatalf("modify staged file: %v", err)
	}
	workflowDigestFiles = pairs
	t.Cleanup(func() { workflowDigestFiles = nil })
	got := runDigestCommand(t, []string{workflows.BuiltinEpicRunnerWorkflowName})
	if want := mustWorkflowSourceDigest(t, spec.Files); got == want {
		t.Fatalf("staged digest equals embedded digest %s despite modified content; digest must attest staged bytes", want)
	}
}

// TestWorkflowDigestFileOverridesRequireFullSourceSet: the --file key set must
// exactly cover the workflow's source set — missing or unknown keys would
// re-introduce the file-set drift the canonical recipe exists to prevent.
func TestWorkflowDigestFileOverridesRequireFullSourceSet(t *testing.T) {
	spec, ok := workflows.BuiltinWorkflow(workflows.BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("epic-runner builtin missing")
	}
	pairs := stageSpecFiles(t, spec)

	runErr := func(files []string) error {
		workflowDigestFiles = files
		t.Cleanup(func() { workflowDigestFiles = nil })
		cmd := &cobra.Command{}
		cmd.SetOut(&bytes.Buffer{})
		return runWorkflowDigest(cmd, []string{workflows.BuiltinEpicRunnerWorkflowName})
	}

	if err := runErr(pairs[:1]); err == nil || !strings.Contains(err.Error(), "missing:") {
		t.Fatalf("subset --file set must error naming the missing keys, got %v", err)
	}
	if err := runErr([]string{"workflows/not-a-source.ts=" + strings.SplitN(pairs[0], "=", 2)[1]}); err == nil || !strings.Contains(err.Error(), "not part of this workflow's source set") {
		t.Fatalf("unknown --file key must error, got %v", err)
	}
	if err := runErr([]string{"malformed-pair"}); err == nil || !strings.Contains(err.Error(), "--file must be") {
		t.Fatalf("malformed --file pair must error, got %v", err)
	}
	if err := runErr(append(append([]string{}, pairs...), pairs[0])); err == nil || !strings.Contains(err.Error(), "duplicate --file key") {
		t.Fatalf("duplicate --file key must error, got %v", err)
	}
}
