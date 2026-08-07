package interactionchat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hwtranscript "github.com/olesho/harness-wrapper/pkg/transcript"
	"github.com/olesho/harness-wrapper/pkg/transcript/claudecode"

	leadcontrol "github.com/tysonthomas9/loomcli/internal/infra/interactionlead"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

const harnessTestClaudeSessionID = "aaaabbbb-cccc-4ddd-8eee-ffff00001111"

type errThenEventsReader struct {
	calls  int
	events []hwtranscript.Event
}

func (reader *errThenEventsReader) Read(
	context.Context,
	string,
	string,
) ([]hwtranscript.Event, error) {
	reader.calls++
	if reader.calls == 1 {
		return nil, errors.New("unexpected end of JSON input")
	}
	return reader.events, nil
}

type blockingHarnessReader struct {
	started chan struct{}
}

func (reader *blockingHarnessReader) Read(
	ctx context.Context,
	_,
	_ string,
) ([]hwtranscript.Event, error) {
	close(reader.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestHarnessReadRetriesOneTornTail(t *testing.T) {
	reader := &errThenEventsReader{
		events: []hwtranscript.Event{{
			Seq:  1,
			Role: hwtranscript.RoleAssistant,
			Type: hwtranscript.EventText,
			Text: "done",
			UUID: "a-1",
		}},
	}
	runtime := &Runtime{retryDelay: time.Millisecond}
	events, err := runtime.readHarnessTranscript(
		t.Context(),
		reader,
		"session",
		"/worktree",
	)
	if err != nil || len(events) != 1 {
		t.Fatalf("events/error = %+v/%v", events, err)
	}
	if reader.calls != 2 {
		t.Fatalf("read calls = %d, want 2", reader.calls)
	}
}

func TestHarnessReadCancellationInterruptsReader(t *testing.T) {
	st := memstore.New()
	seedRuntimeSession(t, st, map[string]string{
		leadcontrol.MetadataRuntimeProvider:  "claude",
		leadcontrol.MetadataHarnessSessionID: harnessTestClaudeSessionID,
		leadcontrol.MetadataRuntimeStatus:    leadcontrol.RuntimeStatusIdle,
	})
	reader := &blockingHarnessReader{started: make(chan struct{})}
	runtime := newTestRuntime(st, &inboxStub{}, &agentQueriesStub{})
	runtime.harnesses = map[string]harnessTranscriptReader{
		"claude": reader,
	}
	runtime.worktreeFor = func(string, string) (string, bool) {
		return "/worktree", true
	}

	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := runtime.ReadConversation(
			ctx,
			interaction.ConversationQuery{
				WorkspaceKey: "WS",
				AgentID:      "reviewer",
			},
		)
		result <- err
	}()
	select {
	case <-reader.started:
	case <-time.After(time.Second):
		t.Fatal("reader did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("conversation read ignored cancellation")
	}
}

func TestBoundedClaudeReaderRejectsOversizedTranscript(t *testing.T) {
	root := t.TempDir()
	worktree := t.TempDir()
	path := writeClaudeHarnessTranscript(
		t,
		root,
		worktree,
		harnessTestClaudeSessionID,
		strings.Repeat("x", 257),
	)
	reader := newBoundedClaudeReader(root)
	reader.maxBytes = 256
	_, err := reader.Read(
		t.Context(),
		harnessTestClaudeSessionID,
		worktree,
	)
	if !errors.Is(err, errHarnessTranscriptLimit) {
		t.Fatalf("read %s error = %v, want transcript limit", path, err)
	}
}

func TestBoundedGeminiReaderReadsCanonicalTranscript(t *testing.T) {
	root := t.TempDir()
	writeGeminiHarnessTranscript(
		t,
		root,
		harnessTestClaudeSessionID,
		`{"role":"model","parts":[{"text":"ready"}],"timestamp":"2026-07-10T00:00:00Z"}`+"\n",
	)
	events, err := newBoundedGeminiReader(root).Read(
		t.Context(),
		harnessTestClaudeSessionID,
		"/worktree",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 ||
		events[0].Role != hwtranscript.RoleAssistant ||
		events[0].Type != hwtranscript.EventText ||
		events[0].Text != "ready" {
		t.Fatalf("events = %+v", events)
	}
}

func TestBoundedGeminiReaderRejectsOversizedTranscript(t *testing.T) {
	root := t.TempDir()
	path := writeGeminiHarnessTranscript(
		t,
		root,
		harnessTestClaudeSessionID,
		strings.Repeat("x", 257),
	)
	reader := newBoundedGeminiReader(root)
	reader.maxBytes = 256
	_, err := reader.Read(
		t.Context(),
		harnessTestClaudeSessionID,
		"/worktree",
	)
	if !errors.Is(err, errHarnessTranscriptLimit) {
		t.Fatalf("read %s error = %v, want transcript limit", path, err)
	}
}

func TestOversizedClaudeConversationFailsClosed(t *testing.T) {
	st := memstore.New()
	seedRuntimeSession(t, st, map[string]string{
		leadcontrol.MetadataRuntimeProvider:  "claude",
		leadcontrol.MetadataHarnessSessionID: harnessTestClaudeSessionID,
		leadcontrol.MetadataRuntimeStatus:    leadcontrol.RuntimeStatusIdle,
	})
	root := t.TempDir()
	worktree := t.TempDir()
	writeClaudeHarnessTranscript(
		t,
		root,
		worktree,
		harnessTestClaudeSessionID,
		strings.Repeat("x", 257),
	)
	reader := newBoundedClaudeReader(root)
	reader.maxBytes = 256
	runtime := newTestRuntime(st, &inboxStub{}, &agentQueriesStub{})
	runtime.harnesses = map[string]harnessTranscriptReader{
		"claude": reader,
	}
	runtime.worktreeFor = func(string, string) (string, bool) {
		return worktree, true
	}
	conversation, err := runtime.ReadConversation(
		t.Context(),
		interaction.ConversationQuery{
			WorkspaceKey: "WS",
			AgentID:      "reviewer",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if conversation.State != interaction.ConversationFailed ||
		!strings.Contains(conversation.Detail, "bounded read limit") {
		t.Fatalf("conversation = %+v", conversation)
	}
}

func TestClaudeConversationUsesFreshRotatedSession(t *testing.T) {
	st := memstore.New()
	startedAt := time.Now().Add(-time.Minute).UTC()
	seedRuntimeSession(t, st, map[string]string{
		leadcontrol.MetadataRuntimeProvider:   "claude",
		leadcontrol.MetadataHarnessSessionID:  harnessTestClaudeSessionID,
		leadcontrol.MetadataRuntimeStatus:     leadcontrol.RuntimeStatusIdle,
		leadcontrol.MetadataHarnessStartedAt:  startedAt.Format(time.RFC3339Nano),
		leadcontrol.MetadataRuntimeControlled: "true",
	})
	root := t.TempDir()
	worktree := t.TempDir()
	stale := writeClaudeHarnessTranscript(
		t,
		root,
		worktree,
		"00000000-old-session",
		claudeAssistantLine("old", "stale review"),
	)
	old := startedAt.Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	writeClaudeHarnessTranscript(
		t,
		root,
		worktree,
		"99999999-rotated",
		claudeAssistantLine("new", "fresh review"),
	)

	runtime := newTestRuntime(st, &inboxStub{}, &agentQueriesStub{})
	runtime.harnesses = map[string]harnessTranscriptReader{
		"claude": newBoundedClaudeReader(root),
	}
	runtime.worktreeFor = func(string, string) (string, bool) {
		return worktree, true
	}
	conversation, err := runtime.ReadConversation(
		t.Context(),
		interaction.ConversationQuery{
			WorkspaceKey: "WS",
			AgentID:      "reviewer",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if conversation.State != interaction.ConversationIdle ||
		len(conversation.Messages) != 1 ||
		conversation.Messages[0].Text != "fresh review" {
		t.Fatalf("conversation = %+v", conversation)
	}
}

func TestClaudeConversationDoesNotRotateWithoutStartedAt(t *testing.T) {
	st := memstore.New()
	seedRuntimeSession(t, st, map[string]string{
		leadcontrol.MetadataRuntimeProvider:  "claude",
		leadcontrol.MetadataHarnessSessionID: harnessTestClaudeSessionID,
		leadcontrol.MetadataRuntimeStatus:    leadcontrol.RuntimeStatusIdle,
	})
	root := t.TempDir()
	worktree := t.TempDir()
	writeClaudeHarnessTranscript(
		t,
		root,
		worktree,
		"99999999-other",
		claudeAssistantLine("other", "wrong conversation"),
	)
	runtime := newTestRuntime(st, &inboxStub{}, &agentQueriesStub{})
	runtime.harnesses = map[string]harnessTranscriptReader{
		"claude": newBoundedClaudeReader(root),
	}
	runtime.worktreeFor = func(string, string) (string, bool) {
		return worktree, true
	}
	conversation, err := runtime.ReadConversation(
		t.Context(),
		interaction.ConversationQuery{
			WorkspaceKey: "WS",
			AgentID:      "reviewer",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if conversation.State != interaction.ConversationIdle ||
		len(conversation.Messages) != 0 {
		t.Fatalf("conversation = %+v", conversation)
	}
}

func TestConversationStateFromHarnessRuntimeStatus(t *testing.T) {
	tests := map[string]interaction.ConversationState{
		"":                                        interaction.ConversationStarting,
		leadcontrol.RuntimeStatusStarting:         interaction.ConversationStarting,
		leadcontrol.RuntimeStatusActive:           interaction.ConversationRunning,
		leadcontrol.RuntimeStatusWaitingApproval:  interaction.ConversationRunning,
		leadcontrol.RuntimeStatusIdle:             interaction.ConversationIdle,
		leadcontrol.RuntimeStatusWaitingUserInput: interaction.ConversationIdle,
		leadcontrol.RuntimeStatusDisconnected:     interaction.ConversationReconnecting,
		leadcontrol.RuntimeStatusFailed:           interaction.ConversationFailed,
		"future-status":                           interaction.ConversationFailed,
	}
	for status, want := range tests {
		if got := conversationStateFromRuntimeStatus(status); got != want {
			t.Errorf("status %q = %q, want %q", status, got, want)
		}
	}
}

func TestHarnessMessagesKeepDuplicateItemsAndGroupNativeTurn(t *testing.T) {
	events := []hwtranscript.Event{
		{
			Seq: 1, Role: hwtranscript.RoleAssistant,
			Type: hwtranscript.EventText, Text: "part one",
			UUID: "message-1",
		},
		{
			Seq: 2, Role: hwtranscript.RoleAssistant,
			Type: hwtranscript.EventText, Text: "part two",
			UUID: "message-1",
		},
		{
			Seq: 3, Role: hwtranscript.RoleTool,
			Type: hwtranscript.EventToolResult, Output: "hidden",
		},
	}
	messages := harnessMessages(
		"claude",
		harnessTestClaudeSessionID,
		events,
	)
	if len(messages) != 2 {
		t.Fatalf("messages = %+v", messages)
	}
	if messages[0].ItemID == messages[1].ItemID {
		t.Fatalf("duplicate item IDs = %q", messages[0].ItemID)
	}
	if messages[0].TurnID != messages[1].TurnID {
		t.Fatalf(
			"turn IDs = %q and %q, want one native turn",
			messages[0].TurnID,
			messages[1].TurnID,
		)
	}
	for _, message := range messages {
		if !strings.HasPrefix(
			message.ItemID,
			"claude/"+harnessTestClaudeSessionID[:8]+"/",
		) {
			t.Fatalf("item ID = %q", message.ItemID)
		}
	}
}

func writeClaudeHarnessTranscript(
	t *testing.T,
	root,
	worktree,
	sessionID,
	body string,
) string {
	t.Helper()
	directory := filepath.Join(root, claudecode.EncodedCWD(worktree))
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeGeminiHarnessTranscript(
	t *testing.T,
	root,
	sessionID,
	body string,
) string {
	t.Helper()
	directory := filepath.Join(root, "tmp", "project", "chats")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(
		directory,
		"session-2026-07-30-"+geminiSessionShort(sessionID)+".jsonl",
	)
	header := `{"sessionId":"` + sessionID +
		`","projectHash":"project","kind":"main"}` + "\n"
	if err := os.WriteFile(path, []byte(header+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func claudeAssistantLine(uuid, text string) string {
	return `{"type":"assistant","uuid":"` + uuid +
		`","message":{"role":"assistant","content":[{"type":"text","text":"` +
		text + `"}]},"timestamp":"2026-07-10T00:00:00Z"}` + "\n"
}
