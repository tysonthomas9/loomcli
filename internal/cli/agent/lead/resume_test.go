package lead

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
)

func setResumeFlags(t *testing.T, resume string, cont bool) {
	t.Helper()
	origResume, origContinue := leadResume, leadContinue
	leadResume, leadContinue = resume, cont
	t.Cleanup(func() { leadResume, leadContinue = origResume, origContinue })
}

func TestLeadResumeRequestNotRequested(t *testing.T) {
	setResumeFlags(t, "", false)
	req, err := leadResumeRequest("/repo", "claude", nil)
	if err != nil || req != nil {
		t.Fatalf("req=%v err=%v, want (nil, nil) with neither flag set", req, err)
	}
}

// Decidable from the flags alone, so it is refused before any store access.
func TestLeadResumeRequestRejectsBothFlags(t *testing.T) {
	setResumeFlags(t, "lead-x", true)
	_, err := leadResumeRequest("/repo", "claude", nil)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("err = %v, want a usage error", err)
	}
}

func TestLeadResumeRequestBuildsRequest(t *testing.T) {
	setResumeFlags(t, "  lead-x  ", false)
	req, err := leadResumeRequest("/repo", "Claude", nil)
	if err != nil {
		t.Fatalf("leadResumeRequest: %v", err)
	}
	if req.Ref != "lead-x" || req.WorkDir != "/repo" || req.Backend != "Claude" {
		t.Fatalf("req = %+v", req)
	}
	if req.AgentID == "" {
		t.Fatal("AgentID must be set so refusals can name the agent")
	}
}

// The uncontrolled path is a plain interactive launch with no session plumbing,
// so it has nowhere to put a resume id. Refuse, and name the env var -- it is
// usually set in a shell profile and forgotten.
func TestLeadResumeRequestRefusesWhenLeadControlDisabled(t *testing.T) {
	t.Setenv("LOOM_LEAD_CONTROLLED", "0")
	setResumeFlags(t, "", true)
	_, err := leadResumeRequest("/repo", "claude", nil)
	if err == nil || !strings.Contains(err.Error(), "LOOM_LEAD_CONTROLLED") {
		t.Fatalf("err = %v, want a refusal naming the env var", err)
	}
}

// pflag takes an optional-value flag's value only as `--resume=<id>`, so the
// documented `--resume <id>` leaves the id as a positional. It must resolve to
// the same request, not to "unknown command".
func TestLeadResumeRequestAbsorbsThePositionalID(t *testing.T) {
	setResumeFlags(t, leadcontrol.ResumeLatestSentinel, false)
	req, err := leadResumeRequest("/repo", "claude", []string{"lead-x"})
	if err != nil {
		t.Fatalf("leadResumeRequest: %v", err)
	}
	if req.Ref != "lead-x" {
		t.Fatalf("Ref = %q, want the positional id", req.Ref)
	}
}

func TestLeadResumeRequestRejectsValueAndPositional(t *testing.T) {
	setResumeFlags(t, "lead-a", false)
	if _, err := leadResumeRequest("/repo", "claude", []string{"lead-b"}); err == nil {
		t.Fatal("--resume=lead-a lead-b must be rejected, not silently resolved")
	}
}

func TestLeadArgs(t *testing.T) {
	setResumeFlags(t, "", false)
	if err := leadArgs(nil, nil); err != nil {
		t.Fatalf("no args: %v", err)
	}
	if err := leadArgs(nil, []string{"stray"}); err == nil {
		t.Fatal("a positional without --resume must be rejected")
	}
	setResumeFlags(t, leadcontrol.ResumeLatestSentinel, false)
	if err := leadArgs(nil, []string{"lead-x"}); err != nil {
		t.Fatalf("--resume lead-x: %v", err)
	}
	if err := leadArgs(nil, []string{"lead-x", "extra"}); err == nil {
		t.Fatal("two positionals must be rejected")
	}
}

// Seeding the handle at create time (rather than waiting for the runtime
// watcher) is what makes resume chainable: the new row resolves for the NEXT
// --continue even if this process dies before anything is scraped.
func TestSeedResumeMetadataClaude(t *testing.T) {
	metadata := map[string]string{}
	seedResumeMetadata(metadata, &leadcontrol.ResumeTarget{
		Record:           leadcontrol.LeadSessionRecord{SessionID: "lead-prev"},
		HarnessSessionID: "abc-123",
	})
	if metadata[leadcontrol.MetadataHarnessSessionID] != "abc-123" {
		t.Fatalf("harness session id not seeded: %v", metadata)
	}
	if metadata[leadcontrol.MetadataLeadResumedFrom] != "lead-prev" {
		t.Fatalf("ancestry not recorded: %v", metadata)
	}
	if metadata[leadcontrol.MetadataLeadResumedHarnessID] != "abc-123" {
		t.Fatalf("resumed harness id not recorded separately: %v", metadata)
	}
}

func TestSeedResumeMetadataCodex(t *testing.T) {
	metadata := map[string]string{}
	seedResumeMetadata(metadata, &leadcontrol.ResumeTarget{
		Record:        leadcontrol.LeadSessionRecord{SessionID: "lead-prev"},
		CodexThreadID: "thread-9",
	})
	if metadata[leadcontrol.MetadataCodexThreadID] != "thread-9" {
		t.Fatalf("codex thread id not seeded: %v", metadata)
	}
	if metadata[leadcontrol.MetadataLeadResumedHarnessID] != "thread-9" {
		t.Fatalf("resumed handle not recorded: %v", metadata)
	}
}

// A fresh launch must leave the metadata untouched.
func TestSeedResumeMetadataNoResume(t *testing.T) {
	metadata := map[string]string{"actor": "oleh"}
	seedResumeMetadata(metadata, nil)
	if len(metadata) != 1 {
		t.Fatalf("metadata = %v, want it untouched for a fresh launch", metadata)
	}
}

// --last carries no id of its own, so nothing is seeded; the runtime's own
// thread discovery fills the new row in.
func TestSeedResumeMetadataCodexLastSeedsAncestryOnly(t *testing.T) {
	metadata := map[string]string{}
	seedResumeMetadata(metadata, &leadcontrol.ResumeTarget{
		Record:       leadcontrol.LeadSessionRecord{SessionID: "lead-prev"},
		UseCodexLast: true,
	})
	if metadata[leadcontrol.MetadataLeadResumedFrom] != "lead-prev" {
		t.Fatalf("ancestry not recorded: %v", metadata)
	}
	if _, ok := metadata[leadcontrol.MetadataCodexThreadID]; ok {
		t.Fatalf("--last has no thread id to seed: %v", metadata)
	}
}

func TestLeadRuntimeOptionsCarriesResume(t *testing.T) {
	opts := leadRuntimeOptions(
		leadSessionRegistration{Workspace: "WS", SessionID: "lead-new", AgentID: "nova"},
		"/repo", "prompt", "claude",
		&leadcontrol.ResumeTarget{HarnessSessionID: "abc-123"},
	)
	if opts.ResumeHarnessSessionID != "abc-123" || opts.Backend != "claude" || opts.SessionID != "lead-new" {
		t.Fatalf("opts = %+v", opts)
	}
	fresh := leadRuntimeOptions(leadSessionRegistration{Workspace: "WS"}, "/repo", "prompt", "claude", nil)
	if fresh.ResumeHarnessSessionID != "" || fresh.ResumeCodexThreadID != "" || fresh.ResumeLast {
		t.Fatalf("fresh opts = %+v, want no resume fields", fresh)
	}
}

// A bare --resume must mean exactly what --continue means, so the flag's
// NoOptDefVal has to be the sentinel the resolver recognizes.
func TestResumeFlagBareValueIsTheLatestSentinel(t *testing.T) {
	flag := leadCmd.Flags().Lookup("resume")
	if flag == nil {
		t.Fatal("--resume is not registered")
	}
	if flag.NoOptDefVal != leadcontrol.ResumeLatestSentinel {
		t.Fatalf("NoOptDefVal = %q, want %q", flag.NoOptDefVal, leadcontrol.ResumeLatestSentinel)
	}
	if leadCmd.Flags().Lookup("continue") == nil {
		t.Fatal("--continue is not registered")
	}
}
