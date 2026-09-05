package backends

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The fresh-launch argv is a GOLDEN: resume must not perturb it. A regression
// here is not cosmetic -- --session-id is what makes the transcript location
// knowable from boot, so losing it loses the next --continue.
func TestHarnessLeadInvocationFreshClaudeArgsUnchanged(t *testing.T) {
	clearSafetyEnv(t)
	inv, ok, err := harnessLeadInvocation("claude", "/repo", "")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v, want a handled claude launch", ok, err)
	}
	if len(inv.args) != 3 || inv.args[0] != "--session-id" || inv.args[2] != "--dangerously-skip-permissions" {
		t.Fatalf("fresh args = %#v, want [--session-id <uuid> --dangerously-skip-permissions]", inv.args)
	}
	if uuid.Validate(inv.args[1]) != nil {
		t.Fatalf("fresh --session-id = %q, want a UUID", inv.args[1])
	}
	if inv.harnessSessionID != inv.args[1] {
		t.Fatalf("harnessSessionID = %q, want the --session-id value %q", inv.harnessSessionID, inv.args[1])
	}
}

// --session-id and --resume are mutually exclusive; claude refuses a launch
// carrying both, so the resume argv must drop the pin entirely.
func TestHarnessLeadInvocationClaudeResumeArgs(t *testing.T) {
	clearSafetyEnv(t)
	const id = "11111111-2222-3333-4444-555555555555"
	inv, ok, err := harnessLeadInvocation("claude", "/repo", id)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v, want a handled claude launch", ok, err)
	}
	want := []string{"--resume", id, "--dangerously-skip-permissions"}
	if !slices.Equal(inv.args, want) {
		t.Fatalf("resume args = %#v, want %#v", inv.args, want)
	}
	if slices.Contains(inv.args, "--session-id") {
		t.Fatalf("resume args carry --session-id, which claude refuses alongside --resume: %#v", inv.args)
	}
	if inv.harnessSessionID != id {
		t.Fatalf("harnessSessionID = %q, want the resumed id %q", inv.harnessSessionID, id)
	}
}

// The safety knobs ride the resume path too -- an interactive role carrying
// read_only must not shed the restriction just because it resumed.
func TestHarnessLeadInvocationClaudeResumeKeepsSafetyKnobs(t *testing.T) {
	clearSafetyEnv(t)
	t.Setenv("LOOM_READ_ONLY", "1")
	inv, _, err := harnessLeadInvocation("claude", "/repo", "abc-123")
	if err != nil {
		t.Fatalf("harnessLeadInvocation: %v", err)
	}
	if joined := strings.Join(inv.args, " "); !strings.Contains(joined, "--disallowedTools Write,Edit,NotebookEdit,Bash") {
		t.Fatalf("resume args = %q, want the read_only deny-set", joined)
	}
}

func TestHarnessLeadInvocationNonClaudeResumeRefuses(t *testing.T) {
	clearSafetyEnv(t)
	for _, backend := range []string{"gemini", "opencode", "cursor"} {
		if _, _, err := harnessLeadInvocation(backend, "/repo", "abc-123"); err == nil {
			t.Fatalf("%s: resume must be refused, not silently started fresh", backend)
		} else if !strings.Contains(err.Error(), backend) {
			t.Fatalf("%s: err = %v, want it to name the backend", backend, err)
		}
		// ... and the same backend without a resume id still launches.
		if _, ok, err := harnessLeadInvocation(backend, "/repo", ""); err != nil || !ok {
			t.Fatalf("%s: fresh launch ok=%v err=%v", backend, ok, err)
		}
	}
}

// A refusal must be reported as HANDLED. Not-handled sends the caller to the
// plain interactive fallback, which is a fresh conversation -- the exact
// silent data loss the refusal exists to prevent.
func TestRunControlledLeadRuntimeResumeRefusalIsHandled(t *testing.T) {
	clearSafetyEnv(t)
	installFakeHarnessLead(t)
	handled, err := RunControlledLeadRuntime(context.Background(), ControlledLeadOptions{
		Workspace: "WS", LeadName: "nova", SessionID: "s", WorkDir: "/repo", Prompt: "p",
		Backend: "gemini", ResumeHarnessSessionID: "abc-123",
	})
	if err == nil {
		t.Fatal("resume on gemini must refuse")
	}
	if !handled {
		t.Fatal("a resume refusal must be handled, or the caller falls back to a fresh session")
	}
}

// An unknown backend has no controlled runtime at all: without a resume it
// falls back (handled=false), with one it must refuse rather than fall back.
func TestRunControlledLeadRuntimeUnknownBackendResumeRefuses(t *testing.T) {
	clearSafetyEnv(t)
	installFakeHarnessLead(t)
	handled, err := RunControlledLeadRuntime(context.Background(), ControlledLeadOptions{
		Workspace: "WS", LeadName: "nova", SessionID: "s", WorkDir: "/repo", Prompt: "p",
		Backend: "my-external-plugin", ResumeHarnessSessionID: "abc-123",
	})
	if err == nil || !handled {
		t.Fatalf("handled=%v err=%v, want a handled refusal", handled, err)
	}
}

func TestRunControlledLeadRuntimeClaudeCarriesResumeIntoConfig(t *testing.T) {
	clearSafetyEnv(t)
	captured := installFakeHarnessLead(t)
	const id = "11111111-2222-3333-4444-555555555555"
	handled, err := RunControlledLeadRuntime(context.Background(), ControlledLeadOptions{
		Workspace: "WS", LeadName: "nova", SessionID: "lead-session", WorkDir: "/repo", Prompt: "p",
		Backend: "claude", ResumeHarnessSessionID: id,
	})
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if captured.ResumedFromSessionID != id {
		t.Fatalf("ResumedFromSessionID = %q, want %q", captured.ResumedFromSessionID, id)
	}
	if captured.HarnessSessionID != id {
		t.Fatalf("HarnessSessionID = %q, want the resumed id", captured.HarnessSessionID)
	}
}

func TestLeadControlDisabledMirrorsEnv(t *testing.T) {
	if LeadControlDisabled() {
		t.Fatal("LeadControlDisabled() = true with the env unset")
	}
	t.Setenv(envLeadControlled, "0")
	if !LeadControlDisabled() {
		t.Fatal("LeadControlDisabled() = false with LOOM_LEAD_CONTROLLED=0")
	}
}
