package prreview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hwtranscript "github.com/olesho/harness-wrapper/pkg/transcript"
	"github.com/olesho/harness-wrapper/pkg/transcript/claudecode"

	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
)

const testClaudeSessionID = "aaaabbbb-cccc-4ddd-8eee-ffff00001111"

// fakeGitHubPAT assembles a token that matches the GitHub PAT shape the
// redactor must catch (ghp_ + 36 alphanumerics, high entropy — the detector
// has an entropy floor) without a credential-shaped literal ever appearing in
// this file, so repository secret scanners never flag it.
func fakeGitHubPAT() string {
	return "ghp" + "_" + "aB3xK9mQpL7vRt2wYc" + "5nHd8fXj4bGzU6ke1s"
}

// overrideHarnessReader swaps the transcript reader for one provider and
// restores it on cleanup.
func overrideHarnessReader(t *testing.T, provider string, reader harnessTranscriptReader) {
	t.Helper()
	orig, had := harnessTranscriptReaders[provider]
	harnessTranscriptReaders[provider] = reader
	t.Cleanup(func() {
		if had {
			harnessTranscriptReaders[provider] = orig
		} else {
			delete(harnessTranscriptReaders, provider)
		}
	})
}

// setupClaudeReviewer stands up a claude-backed reviewer: an agent, an
// orchestration session with claude runtime metadata, a remembered worktree
// (real temp dir with a .git entry), and a re-rooted REAL claudecode reader.
// Returns the worktree and the projects root for fixture writes.
func setupClaudeReviewer(t *testing.T, h *prReviewHarness, status string) (worktree, projectsRoot string) {
	t.Helper()
	agentName := createReviewerAgentForTest(t, h, "octocat", "hello", 7)
	createReviewerOrchestrationSessionForTest(t, h, agentName, map[string]string{
		leadcontrol.MetadataRuntimeProvider:  "claude",
		leadcontrol.MetadataRuntimeStatus:    status,
		leadcontrol.MetadataHarnessSessionID: testClaudeSessionID,
	})

	worktree = t.TempDir()
	if err := os.MkdirAll(filepath.Join(worktree, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	// LOOM_CONFIG_DIR is already a temp dir (harness setup), so this writes
	// to test-local bootstrap state.
	if err := localworkspace.RememberAgentWorktree(prReviewTestWorkspace, agentName, worktree); err != nil {
		t.Fatalf("RememberAgentWorktree: %v", err)
	}

	projectsRoot = t.TempDir()
	overrideHarnessReader(t, "claude", &claudecode.Reader{ProjectsRoot: projectsRoot})
	return worktree, projectsRoot
}

func writeClaudeTranscriptFixture(t *testing.T, projectsRoot, worktree string, lines []string) {
	t.Helper()
	dir := filepath.Join(projectsRoot, claudecode.EncodedCWD(worktree))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, testClaudeSessionID+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript fixture: %v", err)
	}
}

func decodeConversation(t *testing.T, raw []byte) (state, detail string, messages []reviewerStreamMessage) {
	t.Helper()
	var decoded struct {
		Data struct {
			State    string                  `json:"state"`
			Detail   string                  `json:"detail"`
			Messages []reviewerStreamMessage `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode conversation: %v (body %s)", err, raw)
	}
	return decoded.Data.State, decoded.Data.Detail, decoded.Data.Messages
}

func TestGetReviewerConversationClaudeTranscript(t *testing.T) {
	h := newPRReviewHarness(t, false)
	worktree, projectsRoot := setupClaudeReviewer(t, h, leadcontrol.RuntimeStatusIdle)
	writeClaudeTranscriptFixture(t, projectsRoot, worktree, []string{
		// The reviewer prompt bubble (claude's positional first turn) — trimmed.
		`{"type":"user","uuid":"u-prompt","message":{"role":"user","content":"## READ-ONLY PR REVIEWER\nreview the diff"},"timestamp":"2026-07-10T12:00:00Z"}`,
		// A tool call — never a chat bubble.
		`{"type":"assistant","uuid":"a-tool","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu-1","name":"Bash","input":{"command":"git diff"}}]},"timestamp":"2026-07-10T12:00:01Z"}`,
		// The review, carrying a secret that must not reach the client. The
		// token is assembled at runtime so no credential-shaped literal lands
		// in the repo (GitHub push protection pattern-matches file contents).
		`{"type":"assistant","uuid":"a-review","message":{"role":"assistant","content":[{"type":"text","text":"Found a leaked token ` + fakeGitHubPAT() + ` in config."}]},"timestamp":"2026-07-10T12:00:02Z"}`,
		// A follow-up from the human.
		`{"type":"user","uuid":"u-follow","message":{"role":"user","content":"is it exploitable?"},"timestamp":"2026-07-10T12:00:03Z"}`,
	})

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/conversation")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	state, _, msgs := decodeConversation(t, raw)
	if state != "idle" {
		t.Fatalf("state = %q, want idle (from runtime status)", state)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %+v, want 2 (prompt trimmed, tool skipped)", msgs)
	}
	if msgs[0].Role != "assistant" || strings.Contains(msgs[0].Text, "ghp_") || !strings.Contains(msgs[0].Text, "REDACTED") {
		t.Fatalf("review message = %+v, want redacted assistant text", msgs[0])
	}
	if msgs[1].Role != "user" || msgs[1].Text != "is it exploitable?" {
		t.Fatalf("follow-up = %+v", msgs[1])
	}
	for _, msg := range msgs {
		if !strings.HasPrefix(msg.ItemID, "claude/"+testClaudeSessionID[:8]+"/") {
			t.Fatalf("item id %q not namespaced by provider/session", msg.ItemID)
		}
		if msg.TurnID == "" {
			t.Fatalf("empty turn id: %+v", msg)
		}
	}
}

func TestGetReviewerConversationClaudeTranscriptNotYetWritten(t *testing.T) {
	h := newPRReviewHarness(t, false)
	setupClaudeReviewer(t, h, leadcontrol.RuntimeStatusStarting)
	// No fixture written — the exact transcript file does not exist yet.

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/conversation")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	state, _, msgs := decodeConversation(t, raw)
	if state != "starting" || len(msgs) != 0 {
		t.Fatalf("state/messages = %q/%d, want starting with no messages", state, len(msgs))
	}
}

func TestGetReviewerConversationClaudeMissingWorktree(t *testing.T) {
	h := newPRReviewHarness(t, false)
	agentName := createReviewerAgentForTest(t, h, "octocat", "hello", 7)
	createReviewerOrchestrationSessionForTest(t, h, agentName, map[string]string{
		leadcontrol.MetadataRuntimeProvider:  "claude",
		leadcontrol.MetadataRuntimeStatus:    leadcontrol.RuntimeStatusIdle,
		leadcontrol.MetadataHarnessSessionID: testClaudeSessionID,
	})
	// No remembered worktree.

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/conversation")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	state, detail, _ := decodeConversation(t, raw)
	if state != reviewerStateFailed || detail == "" {
		t.Fatalf("state/detail = %q/%q, want failed with detail", state, detail)
	}
}

func TestGetReviewerConversationUnsupportedBackend(t *testing.T) {
	for _, provider := range []string{"opencode", "cursor"} {
		h := newPRReviewHarness(t, false)
		agentName := createReviewerAgentForTest(t, h, "octocat", "hello", 7)
		createReviewerOrchestrationSessionForTest(t, h, agentName, map[string]string{
			leadcontrol.MetadataRuntimeProvider: provider,
			leadcontrol.MetadataRuntimeStatus:   leadcontrol.RuntimeStatusActive,
		})

		status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/conversation")
		if status != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200 (body %s)", provider, status, raw)
		}
		state, detail, _ := decodeConversation(t, raw)
		if state != reviewerStateUnsupported || !strings.Contains(detail, provider) {
			t.Fatalf("%s: state/detail = %q/%q, want unsupported naming the backend", provider, state, detail)
		}
	}
}

func TestGetReviewerConversationGeminiWithoutSessionID(t *testing.T) {
	h := newPRReviewHarness(t, false)
	agentName := createReviewerAgentForTest(t, h, "octocat", "hello", 7)
	createReviewerOrchestrationSessionForTest(t, h, agentName, map[string]string{
		leadcontrol.MetadataRuntimeProvider: "gemini",
		leadcontrol.MetadataRuntimeStatus:   leadcontrol.RuntimeStatusActive,
		// No lead_harness_session_id: gemini has no launch pin and no TUI
		// extraction, so the transcript is unreachable — not merely late.
	})

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/conversation")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	state, detail, _ := decodeConversation(t, raw)
	if state != reviewerStateUnsupported || !strings.Contains(detail, "gemini") {
		t.Fatalf("state/detail = %q/%q, want unsupported naming gemini", state, detail)
	}
}

func TestGetReviewerConversationClaudeRotatedSessionID(t *testing.T) {
	// Claude rotated its session id at boot (folder-trust dialog): the pinned
	// id names a file that will never exist, but a transcript recorded AFTER
	// the runtime started is present — the reconcile must pick it up. A stale
	// transcript from an older session (mtime before the runtime start) must
	// never be picked.
	h := newPRReviewHarness(t, false)
	agentName := createReviewerAgentForTest(t, h, "octocat", "hello", 7)
	startedAt := time.Now().Add(-time.Minute)
	createReviewerOrchestrationSessionForTest(t, h, agentName, map[string]string{
		leadcontrol.MetadataRuntimeProvider:  "claude",
		leadcontrol.MetadataRuntimeStatus:    leadcontrol.RuntimeStatusIdle,
		leadcontrol.MetadataHarnessSessionID: testClaudeSessionID, // pinned; never written
		leadcontrol.MetadataHarnessStartedAt: startedAt.UTC().Format(time.RFC3339Nano),
	})
	worktree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(worktree, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := localworkspace.RememberAgentWorktree(prReviewTestWorkspace, agentName, worktree); err != nil {
		t.Fatalf("RememberAgentWorktree: %v", err)
	}
	projectsRoot := t.TempDir()
	overrideHarnessReader(t, "claude", &claudecode.Reader{ProjectsRoot: projectsRoot})

	dir := filepath.Join(projectsRoot, claudecode.EncodedCWD(worktree))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	stale := filepath.Join(dir, "00000000-old-session.jsonl")
	if err := os.WriteFile(stale, []byte(`{"type":"assistant","uuid":"old","message":{"role":"assistant","content":[{"type":"text","text":"stale review"}]},"timestamp":"2026-07-01T00:00:00Z"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write stale transcript: %v", err)
	}
	old := startedAt.Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("age stale transcript: %v", err)
	}
	rotated := filepath.Join(dir, "99999999-rotated.jsonl")
	if err := os.WriteFile(rotated, []byte(`{"type":"assistant","uuid":"new","message":{"role":"assistant","content":[{"type":"text","text":"fresh review"}]},"timestamp":"2026-07-10T00:00:00Z"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write rotated transcript: %v", err)
	}

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/conversation")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	state, _, msgs := decodeConversation(t, raw)
	if state != "idle" || len(msgs) != 1 || msgs[0].Text != "fresh review" {
		t.Fatalf("state/messages = %q/%+v, want the rotated session's review", state, msgs)
	}
}

func TestGetReviewerConversationClaudeNoRotationWithoutStartedAt(t *testing.T) {
	// Without a launch timestamp there is no safe way to pick a fallback
	// transcript — the read must stay empty rather than risk showing a stale
	// session's conversation.
	h := newPRReviewHarness(t, false)
	agentName := createReviewerAgentForTest(t, h, "octocat", "hello", 7)
	createReviewerOrchestrationSessionForTest(t, h, agentName, map[string]string{
		leadcontrol.MetadataRuntimeProvider:  "claude",
		leadcontrol.MetadataRuntimeStatus:    leadcontrol.RuntimeStatusIdle,
		leadcontrol.MetadataHarnessSessionID: testClaudeSessionID,
	})
	worktree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(worktree, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := localworkspace.RememberAgentWorktree(prReviewTestWorkspace, agentName, worktree); err != nil {
		t.Fatalf("RememberAgentWorktree: %v", err)
	}
	projectsRoot := t.TempDir()
	overrideHarnessReader(t, "claude", &claudecode.Reader{ProjectsRoot: projectsRoot})
	dir := filepath.Join(projectsRoot, claudecode.EncodedCWD(worktree))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.jsonl"), []byte(`{"type":"assistant","uuid":"x","message":{"role":"assistant","content":[{"type":"text","text":"stale"}]},"timestamp":"2026-07-01T00:00:00Z"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/conversation")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	state, _, msgs := decodeConversation(t, raw)
	if state != "idle" || len(msgs) != 0 {
		t.Fatalf("state/messages = %q/%+v, want idle with no messages", state, msgs)
	}
}

func TestStreamReviewerEmitsClaudeMessages(t *testing.T) {
	h := newPRReviewHarness(t, false)
	worktree, projectsRoot := setupClaudeReviewer(t, h, leadcontrol.RuntimeStatusIdle)
	writeClaudeTranscriptFixture(t, projectsRoot, worktree, []string{
		`{"type":"user","uuid":"u-prompt","message":{"role":"user","content":"## READ-ONLY PR REVIEWER"},"timestamp":"2026-07-10T12:00:00Z"}`,
		`{"type":"assistant","uuid":"a-review","message":{"role":"assistant","content":[{"type":"text","text":"LGTM overall."}]},"timestamp":"2026-07-10T12:00:01Z"}`,
	})

	h.module.streamPollInterval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	status, raw := h.streamWithContext(t, ctx, "/api/workspaces/WS/pull-requests/octocat/hello/7/stream")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	body := string(raw)
	if !strings.Contains(body, `"state":"idle"`) {
		t.Fatalf("stream body = %q, want idle status", body)
	}
	// Exactly one message event despite many polls (seen-cursor dedupe), and
	// the prompt bubble is trimmed.
	if strings.Count(body, "event: message") != 1 || !strings.Contains(body, "LGTM overall.") {
		t.Fatalf("stream body = %q, want exactly one LGTM message event", body)
	}
	if strings.Contains(body, "READ-ONLY PR REVIEWER") {
		t.Fatalf("stream body leaked the prompt preamble: %q", body)
	}
}

// errThenEventsReader fails the first read and succeeds on the second,
// modeling a torn-tail parse failure during a concurrent append.
type errThenEventsReader struct {
	calls  int
	events []hwtranscript.Event
}

func (r *errThenEventsReader) Read(_, _ string) ([]hwtranscript.Event, error) {
	r.calls++
	if r.calls == 1 {
		return nil, errors.New("unexpected end of JSON input")
	}
	return r.events, nil
}

func TestHarnessReadRetriesTornTail(t *testing.T) {
	reader := &errThenEventsReader{events: []hwtranscript.Event{
		{Seq: 1, Role: hwtranscript.RoleAssistant, Type: hwtranscript.EventText, Text: "done", UUID: "a-1"},
	}}
	events, err := readHarnessTranscriptWithRetry(reader, "sess", "/wt")
	if err != nil || len(events) != 1 {
		t.Fatalf("events/err = %v/%v, want retry to succeed", events, err)
	}
	if reader.calls != 2 {
		t.Fatalf("calls = %d, want 2 (one retry)", reader.calls)
	}
}

func TestReviewerStateFromRuntimeStatus(t *testing.T) {
	cases := map[string]string{
		"":                                        "starting",
		leadcontrol.RuntimeStatusStarting:         "starting",
		leadcontrol.RuntimeStatusActive:           "running",
		leadcontrol.RuntimeStatusWaitingApproval:  "running",
		leadcontrol.RuntimeStatusIdle:             "idle",
		leadcontrol.RuntimeStatusWaitingUserInput: "idle",
		leadcontrol.RuntimeStatusDisconnected:     "reconnecting",
		leadcontrol.RuntimeStatusFailed:           reviewerStateFailed,
		"some-future-status":                      reviewerStateFailed,
	}
	for status, want := range cases {
		if got := reviewerStateFromRuntimeStatus(status); got != want {
			t.Errorf("reviewerStateFromRuntimeStatus(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestReviewerMessagesFromEventsDeduplicatesIDs(t *testing.T) {
	// Two identical events with zero timestamps: Event.ID()'s content-hash
	// fallback collides (the gemini case), so ordinals must disambiguate.
	dup := hwtranscript.Event{Role: hwtranscript.RoleUser, Type: hwtranscript.EventText, Text: "same"}
	msgs := reviewerMessagesFromEvents("gemini", "1234567890", []hwtranscript.Event{dup, dup})
	if len(msgs) != 2 {
		t.Fatalf("messages = %+v, want 2", msgs)
	}
	if msgs[0].ItemID == msgs[1].ItemID {
		t.Fatalf("duplicate events share item id %q", msgs[0].ItemID)
	}
	for _, msg := range msgs {
		if !strings.HasPrefix(msg.ItemID, "gemini/12345678/") {
			t.Fatalf("item id %q not namespaced", msg.ItemID)
		}
	}
}

func TestReviewerMessagesFromEventsGroupsBlocksByTurn(t *testing.T) {
	events := []hwtranscript.Event{
		{Seq: 1, Role: hwtranscript.RoleAssistant, Type: hwtranscript.EventText, Text: "part one", UUID: "msg-1"},
		{Seq: 2, Role: hwtranscript.RoleAssistant, Type: hwtranscript.EventText, Text: "part two", UUID: "msg-1"},
		{Seq: 3, Role: hwtranscript.RoleTool, Type: hwtranscript.EventToolResult, Output: "skipped"},
		{Seq: 4, Role: hwtranscript.RoleSystem, Type: hwtranscript.EventText, Text: "skipped"},
	}
	msgs := reviewerMessagesFromEvents("claude", testClaudeSessionID, events)
	if len(msgs) != 2 {
		t.Fatalf("messages = %+v, want the two assistant blocks only", msgs)
	}
	if msgs[0].TurnID != msgs[1].TurnID {
		t.Fatalf("blocks of one native message have different turn ids: %q vs %q", msgs[0].TurnID, msgs[1].TurnID)
	}
	if msgs[0].ItemID == msgs[1].ItemID {
		t.Fatalf("blocks of one native message share an item id: %q", msgs[0].ItemID)
	}
}
