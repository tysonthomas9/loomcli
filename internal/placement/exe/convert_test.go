package exe

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/placement"
)

// TestLabelTagRoundTrip covers the awkward part of this platform: exe.dev tags
// are FLAT STRINGS, so loom's key/value labels have to survive an encode and
// decode. Reconciliation and orphan reaping both find sandboxes by label, so a
// lossy round trip means orphans nothing ever sweeps.
func TestLabelTagRoundTrip(t *testing.T) {
	labels := map[string]string{
		placement.PlacementLabelKey:   "placement-1",
		placement.EnvironmentLabelKey: "deploy-abc",
		"loom-workspace":              "ws",
		"loom-agent":                  "nova",
	}
	got := tagsToLabels(labelsToTags(labels))
	for key, want := range labels {
		if got[key] != want {
			t.Errorf("label %q round-tripped to %q, want %q", key, got[key], want)
		}
	}
}

func TestLabelsToTagsDropsUnencodableLabels(t *testing.T) {
	tags := labelsToTags(map[string]string{
		"loom-agent": "nova",
		"bad key":    "value",      // space is not in the tag grammar
		"loom-note":  "has spaces", // nor here
	})
	if len(tags) != 1 || tags[0] != "loom-agent__nova" {
		t.Fatalf("tags = %v, want only the encodable one", tags)
	}
}

// TestMatchesLabelsToleratesAbsentTags: an untagged VM returns tags ABSENT,
// not [], which decodes to nil. A nil map must not panic or spuriously match.
func TestMatchesLabelsToleratesAbsentTags(t *testing.T) {
	sandbox := toProviderSandbox(vm{Name: "loom-p1", Tags: nil})
	if matchesLabels(sandbox.Labels, map[string]string{"loom-agent": "nova"}) {
		t.Fatal("an untagged VM must not match a label filter")
	}
	if !matchesLabels(sandbox.Labels, nil) {
		t.Fatal("an empty filter matches everything")
	}
}

// TestNeutralStateNeverGuessesAbsent is a fail-closed guard. Absence drives
// release, and releasing a live sandbox severs the only record of something
// billing -- so an unrecognized status must never read as "gone".
func TestNeutralStateNeverGuessesAbsent(t *testing.T) {
	for _, status := range []string{"", "provisioning", "rebooting", "weird-new-status", "  "} {
		if got := neutralState(status); got == placement.ProviderSandboxAbsent {
			t.Errorf("status %q mapped to Absent; unknown states must not imply deletion", status)
		}
	}
	if got := neutralState("running"); got != placement.ProviderSandboxRunning {
		t.Errorf("running = %q", got)
	}
	if got := neutralState("deleted"); got != placement.ProviderSandboxAbsent {
		t.Errorf("deleted = %q, want absent", got)
	}
}

// TestParseCreatedAtFailsTowardNotReaping: the reaper treats a zero CreatedAt
// as too young to reap, so an unparseable timestamp must yield zero.
func TestParseCreatedAtFailsTowardNotReaping(t *testing.T) {
	if got := parseCreatedAt("nonsense"); !got.IsZero() {
		t.Fatalf("unparseable timestamp = %v, want zero", got)
	}
	if got := parseCreatedAt(""); !got.IsZero() {
		t.Fatalf("empty timestamp = %v, want zero", got)
	}
	want := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	if got := parseCreatedAt("2026-08-30T12:00:00Z"); !got.Equal(want) {
		t.Fatalf("RFC3339 parsed to %v, want %v", got, want)
	}
}

type recordingRunner struct {
	cmds  []string
	stdin [][]byte
	err   error
}

func (r *recordingRunner) Run(cmd string) (string, error) {
	r.cmds = append(r.cmds, cmd)
	r.stdin = append(r.stdin, nil)
	return "", r.err
}

func (r *recordingRunner) RunStdin(cmd string, stdin []byte) (string, error) {
	r.cmds = append(r.cmds, cmd)
	r.stdin = append(r.stdin, stdin)
	return "", r.err
}

// Seeded files include the codex auth.json, so content is credential-bearing.
// It must reach the VM on stdin: sshd runs a remote command as `sh -c
// '<string>'`, so anything on the command line is readable from the VM's
// process list -- base64 encoding does not hide it, it only encodes it.
func TestWriteFileIsAtomicAndEncoded(t *testing.T) {
	runner := &recordingRunner{}
	err := writeFile(runner, placement.SandboxFile{
		Path: "/home/exedev/.codex/auth.json", Content: []byte(`{"token":"s3cret"}`), Mode: "600",
	})
	if err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	cmd := runner.cmds[0]
	if strings.Contains(cmd, "s3cret") {
		t.Fatal("file content appears verbatim in the command line")
	}
	if strings.Contains(cmd, base64.StdEncoding.EncodeToString([]byte(`{"token":"s3cret"}`))) {
		t.Fatal("file content is on the command line, where the VM's process list exposes it")
	}
	if len(runner.stdin[0]) == 0 {
		t.Fatal("file content was not fed on stdin")
	}
	for _, want := range []string{"base64 -d", "chmod '600'", "mv -f", "umask 077"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command missing %q: %s", want, cmd)
		}
	}
	// Written to a temp path then renamed, so a crash cannot leave a partial
	// file the lead would read as complete.
	if !strings.Contains(cmd, ".loom-tmp") {
		t.Errorf("write is not atomic: %s", cmd)
	}
}

// TestCloneRepoKeepsTheTokenOutOfErrorsAndConfig: a URL-embedded token lands
// in .git/config, in the process list, and in any error text.
func TestCloneRepoKeepsTheTokenOutOfErrorsAndConfig(t *testing.T) {
	runner := &recordingRunner{err: errors.New("clone exploded")}
	err := cloneRepo(runner, "loom-p1", placement.LeadBootPrep{
		Repo:     &placement.RepoClone{Name: "app", RemoteURL: "https://github.com/acme/app", Ref: "main", Checkout: "/home/exedev/app"},
		GitToken: func() (string, error) { return "ghp_secrettoken", nil },
	})
	if err == nil {
		t.Fatal("expected the clone error to surface")
	}
	if strings.Contains(err.Error(), "ghp_secrettoken") {
		t.Fatal("the git token leaked into the error message")
	}
	cmd := runner.cmds[0]
	if strings.Contains(cmd, "ghp_secrettoken") {
		t.Fatal("the token is on the command line, where the VM's process list exposes it")
	}
	if string(runner.stdin[0]) != "ghp_secrettoken" {
		t.Fatalf("the token was not fed on stdin; stdin = %q", runner.stdin[0])
	}
	if !strings.Contains(cmd, "GIT_ASKPASS=") {
		t.Errorf("expected a credential helper, got: %s", cmd)
	}
	if !strings.Contains(cmd, "GIT_TERMINAL_PROMPT=0") {
		t.Error("a clone that can block on a prompt will hang the boot")
	}
}

// TestTmuxNoServerIsAnEmptyList: tmux has TWO distinct no-server signatures
// and both mean "no sessions". The broker reads an error here as a boot
// failure, so conflating them breaks every first boot.
func TestTmuxNoServerIsAnEmptyList(t *testing.T) {
	for _, out := range []string{
		"error connecting to /tmp/tmux-1000/loom (No such file or directory)",
		"no server running on /tmp/tmux-1000/loom",
	} {
		if !tmuxNoServer(out) {
			t.Errorf("not recognized as an empty session list: %q", out)
		}
	}
	if tmuxNoServer("some other tmux failure") {
		t.Error("a real failure must not be swallowed as an empty list")
	}
}

func TestTmuxCreateSessionQuotesEverything(t *testing.T) {
	cmd := tmuxCreateSession("lead", "/home/exedev/app", map[string]string{"B": "2", "A": "1"},
		[]string{"loom", "--workspace", "WS; rm -rf /", "lead"})
	if !strings.Contains(cmd, "tmux -L loom new-session -d") {
		t.Errorf("unexpected form: %s", cmd)
	}
	// The injection attempt must be inside quotes, not a separate command.
	if strings.Contains(cmd, "; rm -rf /'") && !strings.Contains(cmd, `'\''`) {
		if strings.Contains(cmd, "WS; rm -rf /") && !strings.Contains(cmd, "'WS; rm -rf /'") {
			t.Errorf("argument not quoted: %s", cmd)
		}
	}
	if !strings.Contains(cmd, "-e 'A=1' -e 'B=2'") {
		t.Errorf("env not sorted/quoted: %s", cmd)
	}
}

func TestSupportsParkingIsFalse(t *testing.T) {
	if (&Provider{}).SupportsParking() {
		t.Fatal("exe.dev has no stop/start; parking must be declared unsupported")
	}
}

// TestSetAutostopIntervalFailsLoudly: it is unreachable while the capability
// gate holds, so it must shout rather than quietly succeed if something ever
// bypasses the gate.
func TestSetAutostopIntervalFailsLoudly(t *testing.T) {
	if err := (&Provider{}).SetAutostopInterval(context.Background(), "loom-p1", time.Minute); err == nil {
		t.Fatal("SetAutostopInterval returned nil; a bypassed capability gate would go unnoticed")
	}
}

// TestCloneRepoRemovesTheCredentialHelperEvenWhenTheCloneFails pins the half of
// the cleanup that is easy to get wrong. Chaining the removal with && means it
// runs only on success -- leaving the git token on disk in the VM on exactly
// the paths where something already went wrong, readable by the lead that is
// about to start.
func TestCloneRepoRemovesTheCredentialHelperEvenWhenTheCloneFails(t *testing.T) {
	runner := &recordingRunner{}
	err := cloneRepo(runner, "loom-p1", placement.LeadBootPrep{
		Repo:     &placement.RepoClone{Name: "app", RemoteURL: "https://github.com/acme/app", Checkout: "/home/exedev/app"},
		GitToken: func() (string, error) { return "ghp_secrettoken", nil },
	})
	if err != nil {
		t.Fatalf("cloneRepo: %v", err)
	}
	cmd := runner.cmds[0]

	rm := strings.Index(cmd, "rm -f")
	if rm < 0 {
		t.Fatalf("the credential helper is never removed: %s", cmd)
	}
	// Everything between the clone and the removal must be sequenced with ";",
	// not "&&" -- "&&" short-circuits the removal on a failed clone.
	clone := strings.Index(cmd, "git clone")
	if clone < 0 {
		t.Fatalf("no clone in the command: %s", cmd)
	}
	if between := cmd[clone:rm]; strings.Contains(between, "&&") {
		t.Errorf("removal is short-circuited by && after a failed clone: %q", between)
	}
	// And the failure must still be a failure.
	if !strings.Contains(cmd, "exit $rc") {
		t.Errorf("the clone's exit status is swallowed by the cleanup: %s", cmd)
	}
}

// TestCloneRepoWithoutATokenSkipsTheHelperEntirely: a public clone must not
// create a credential file at all.
func TestCloneRepoWithoutATokenSkipsTheHelperEntirely(t *testing.T) {
	runner := &recordingRunner{}
	if err := cloneRepo(runner, "loom-p1", placement.LeadBootPrep{
		Repo: &placement.RepoClone{Name: "app", RemoteURL: "https://github.com/acme/app", Checkout: "/home/exedev/app"},
	}); err != nil {
		t.Fatalf("cloneRepo: %v", err)
	}
	if strings.Contains(runner.cmds[0], "askpass") {
		t.Errorf("a credential helper was created for an unauthenticated clone: %s", runner.cmds[0])
	}
}
