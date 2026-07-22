package leadcontrol

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

func TestCodexTUIArgsColdStartCarriesPrompt(t *testing.T) {
	cfg := CodexLeadRuntimeConfig{WorkDir: "/w/pr-7", Prompt: "review this pr"}

	args := codexTUIArgs(cfg, "ws://codex.test", "")

	if args[0] == "resume" {
		t.Fatalf("cold start must not use the resume subcommand: %v", args)
	}
	if got := args[len(args)-1]; got != "review this pr" {
		t.Fatalf("last arg = %q, want the role prompt", got)
	}
	if !strings.Contains(strings.Join(args, " "), "--remote ws://codex.test") {
		t.Fatalf("args did not carry the app-server endpoint: %v", args)
	}
}

// A resume must not re-send the role prompt: codex submits a resume prompt as a
// fresh user message once the session is configured, so carrying it would
// re-issue the role's opening request on every restart.
func TestCodexTUIArgsResumeDropsPromptAndCarriesThreadID(t *testing.T) {
	cfg := CodexLeadRuntimeConfig{WorkDir: "/w/pr-7", Prompt: "review this pr"}

	args := codexTUIArgs(cfg, "ws://codex.test", "thread-42")

	if args[0] != "resume" {
		t.Fatalf("args[0] = %q, want the resume subcommand: %v", args[0], args)
	}
	if got := args[len(args)-1]; got != "thread-42" {
		t.Fatalf("last arg = %q, want the thread id as SESSION_ID", got)
	}
	for _, arg := range args {
		if arg == cfg.Prompt {
			t.Fatalf("resume must not carry the role prompt: %v", args)
		}
	}
	if !strings.Contains(strings.Join(args, " "), "-C /w/pr-7") {
		t.Fatalf("args did not carry the work dir: %v", args)
	}
}

func TestPriorCodexThreadIDReadsSessionMetadata(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createAssignedLeadSession(t, st, "resume", nil)
	setCodexRuntimeMetadata(t, st, "WS", "lead-session", "ws://old.test", "thread-prior")

	cfg := CodexLeadRuntimeConfig{Store: st, Workspace: "WS", SessionID: "lead-session"}

	if got := priorCodexThreadID(ctx, cfg); got != "thread-prior" {
		t.Fatalf("priorCodexThreadID() = %q, want thread-prior", got)
	}
}

func TestPriorCodexThreadIDEmptyWhenSessionMissing(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()

	cfg := CodexLeadRuntimeConfig{Store: st, Workspace: "WS", SessionID: "absent"}

	if got := priorCodexThreadID(ctx, cfg); got != "" {
		t.Fatalf("priorCodexThreadID() = %q, want empty for a missing session", got)
	}
}

func TestClearCodexThreadIDRemovesStaleThread(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createAssignedLeadSession(t, st, "clear", nil)
	setCodexRuntimeMetadata(t, st, "WS", "lead-session", "ws://old.test", "thread-stale")

	if err := ClearCodexThreadID(ctx, st, "WS", "lead-session"); err != nil {
		t.Fatalf("ClearCodexThreadID() error = %v", err)
	}

	session, err := st.AgentSessions().Get(ctx, "WS", "lead-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got, ok := session.Metadata[MetadataCodexThreadID]; ok {
		t.Fatalf("thread id survived the clear: %q", got)
	}
	// Sibling runtime metadata must survive — only the thread binding is dropped.
	if got := session.Metadata[MetadataCodexEndpoint]; got != "ws://old.test" {
		t.Fatalf("endpoint = %q, want it preserved", got)
	}
}

func TestClearCodexThreadIDNoopWithoutThread(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createAssignedLeadSession(t, st, "noop", nil)

	if err := ClearCodexThreadID(ctx, st, "WS", "lead-session"); err != nil {
		t.Fatalf("ClearCodexThreadID() error = %v", err)
	}
}
