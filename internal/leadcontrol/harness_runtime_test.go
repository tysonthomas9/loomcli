package leadcontrol

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/chat"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

func TestHarnessNameForBackend(t *testing.T) {
	cases := map[string]string{
		"claude":   HarnessNameClaudeCode,
		"Claude":   HarnessNameClaudeCode,
		"codex":    HarnessNameCodex,
		"gemini":   HarnessNameGemini,
		"opencode": HarnessNameGeneric,
		"cursor":   HarnessNameGeneric,
		"":         HarnessNameGeneric,
	}
	for backend, want := range cases {
		if got := HarnessNameForBackend(backend); got != want {
			t.Errorf("HarnessNameForBackend(%q) = %q, want %q", backend, got, want)
		}
	}
}

func TestRunHarnessLeadRuntimeLifecycle(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createHarnessLeadSession(t, st)

	fake := newFakeHarnessConversation()
	origOpen := openHarnessConversation
	var gotOpts chat.Options
	openHarnessConversation = func(_ context.Context, opts chat.Options) (harnessConversation, error) {
		gotOpts = opts
		return fake, nil
	}
	t.Cleanup(func() { openHarnessConversation = origOpen })

	var out bytes.Buffer
	runtimeErr := make(chan error, 1)
	go func() {
		runtimeErr <- RunHarnessLeadRuntime(ctx, HarnessLeadRuntimeConfig{
			Store:            st,
			Workspace:        "WS",
			LeadName:         "nova",
			SessionID:        "lead-session",
			WorkDir:          "/repo",
			Prompt:           "lead prompt",
			Backend:          "claude",
			HarnessSessionID: "11111111-2222-4333-8444-555555555555",
			Stdin:            strings.NewReader(""),
			Stdout:           &out,
			Stderr:           &out,
		})
	}()

	// Runtime metadata is persisted and the conversation registered.
	waitForCondition(t, func() bool { return lookupLeadConversation("lead-session") != nil },
		"conversation was not registered")
	session := getLeadSession(t, st)
	if got := session.Metadata[MetadataRuntimeProvider]; got != "claude" {
		t.Fatalf("runtime provider = %q, want claude", got)
	}
	if got := session.Metadata[MetadataRuntimeControlled]; got != "true" {
		t.Fatalf("runtime controlled = %q, want true", got)
	}
	if got := session.Metadata[MetadataHarnessName]; got != HarnessNameClaudeCode {
		t.Fatalf("harness name = %q, want claude-code", got)
	}
	if got := session.Metadata[MetadataHarnessChatSessionID]; got != "chat-1" {
		t.Fatalf("chat session id = %q, want chat-1", got)
	}
	if got := session.Metadata[MetadataHarnessPID]; got != "42" {
		t.Fatalf("harness pid = %q, want 42", got)
	}
	// A launch-assigned harness session id is persisted with the starting
	// metadata — readers can locate the transcript before any TUI scrape.
	if got := session.Metadata[MetadataHarnessSessionID]; got != "11111111-2222-4333-8444-555555555555" {
		t.Fatalf("harness session id = %q, want the launch-assigned uuid", got)
	}
	if gotOpts.Harness != HarnessNameClaudeCode {
		t.Fatalf("opened harness = %q, want claude-code", gotOpts.Harness)
	}
	if len(gotOpts.Args) == 0 || gotOpts.Args[len(gotOpts.Args)-1] != "lead prompt" {
		t.Fatalf("prompt not appended to args: %#v", gotOpts.Args)
	}

	// The status watcher promotes the runtime out of "starting" on its first
	// snapshot poll; delivery is gated until then.
	waitForCondition(t, func() bool {
		return getLeadSession(t, st).Metadata[MetadataRuntimeStatus] != RuntimeStatusStarting
	}, "runtime status never left starting")

	// The registered handle delivers queued messages in-process.
	const message = "Task TASK-1 completed."
	if _, err := DeliverLeadMessage(ctx, st, "WS", "nova", message); err != nil {
		t.Fatalf("DeliverLeadMessage() error = %v", err)
	}
	if got := string(fake.stdinBytes()); got != message {
		t.Fatalf("staged stdin = %q, want delivered message", got)
	}
	if got := fake.sentTexts(); len(got) != 1 || got[0] != "" {
		t.Fatalf("sent texts = %#v, want one empty submit Send", got)
	}

	// Harness exit: unregister, close, persist disconnected.
	close(fake.waitCh)
	select {
	case err := <-runtimeErr:
		if err != nil {
			t.Fatalf("RunHarnessLeadRuntime() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("runtime did not exit after harness wait returned")
	}
	if lookupLeadConversation("lead-session") != nil {
		t.Fatalf("conversation still registered after runtime exit")
	}
	session = getLeadSession(t, st)
	if got := session.Metadata[MetadataRuntimeStatus]; got != RuntimeStatusDisconnected {
		t.Fatalf("runtime status after exit = %q, want disconnected", got)
	}
	if !fake.closed {
		t.Fatalf("conversation was not closed on runtime exit")
	}
}

func waitForCondition(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}
