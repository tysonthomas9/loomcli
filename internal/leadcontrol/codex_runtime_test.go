package leadcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestNewestCodexThreadWaitsForThreadCreatedAfterRuntimeStart(t *testing.T) {
	startedAt := time.Date(2026, 5, 17, 6, 5, 36, 0, time.UTC)
	threads := []CodexThread{
		{
			ID:          "old-lead-thread",
			Cwd:         "/repo",
			CreatedAtMS: float64(startedAt.Add(-3 * time.Minute).UnixMilli()),
			UpdatedAtMS: float64(startedAt.Add(-1 * time.Second).UnixMilli()),
		},
		{
			ID:          "new-lead-thread",
			Cwd:         "/repo",
			CreatedAtMS: float64(startedAt.Add(500 * time.Millisecond).UnixMilli()),
			UpdatedAtMS: float64(startedAt.Add(2 * time.Second).UnixMilli()),
		},
	}

	got := newestCodexThread(threads, "/repo", startedAt)
	if got == nil || got.ID != "new-lead-thread" {
		t.Fatalf("newestCodexThread() = %+v, want new-lead-thread", got)
	}
}

func TestNewestCodexThreadReturnsNilUntilFreshLeadThreadExists(t *testing.T) {
	startedAt := time.Date(2026, 5, 17, 6, 5, 36, 0, time.UTC)
	threads := []CodexThread{{
		ID:          "old-lead-thread",
		Cwd:         "/repo",
		CreatedAtMS: float64(startedAt.Add(-3 * time.Minute).UnixMilli()),
		UpdatedAtMS: float64(startedAt.Add(5 * time.Second).UnixMilli()),
	}}

	got := newestCodexThread(threads, "/repo", startedAt)
	if got != nil {
		t.Fatalf("newestCodexThread() = %+v, want nil before fresh lead thread exists", got)
	}
}

func TestCodexAppServerTimeoutErrorIncludesProbeAndLogTail(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "app-server.log")
	logBody := strings.Repeat("x", int(codexAppServerLogTailBytes)+32) + "\nstartup detail\n"
	if err := os.WriteFile(logPath, []byte(logBody), 0600); err != nil {
		t.Fatalf("write app-server log: %v", err)
	}

	err := codexAppServerTimeoutError(
		"ws://127.0.0.1:62085",
		5*time.Second,
		errors.New("connection refused"),
		logPath,
	)
	got := err.Error()
	for _, want := range []string{
		"codex app-server did not become ready at ws://127.0.0.1:62085 within 5s",
		"last readiness probe: connection refused",
		"app-server log tail:",
		"startup detail",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("timeout error missing %q:\n%s", want, got)
		}
	}
}

func TestCodexAppServerTimeoutErrorOmitsMissingLogTail(t *testing.T) {
	t.Parallel()

	err := codexAppServerTimeoutError(
		"ws://127.0.0.1:62085",
		5*time.Second,
		nil,
		filepath.Join(t.TempDir(), "missing.log"),
	)
	got := err.Error()
	if strings.Contains(got, "app-server log tail:") {
		t.Fatalf("timeout error included missing log tail:\n%s", got)
	}
	if strings.Contains(got, "last readiness probe:") {
		t.Fatalf("timeout error included missing probe error:\n%s", got)
	}
}

func TestCodexAppServerLifetimeSurvivesParentCancellationUntilExplicitStop(t *testing.T) {
	type contextKey string
	const key contextKey = "runtime-value"
	parent, cancelParent := context.WithCancel(context.WithValue(context.Background(), key, "preserved"))
	appCtx, cancelApp := codexAppServerLifetimeContext(parent)
	cancelParent()

	if got := appCtx.Value(key); got != "preserved" {
		t.Fatalf("app-server context value = %v, want preserved", got)
	}
	select {
	case <-appCtx.Done():
		t.Fatal("app-server lifetime ended with parent context before transcript capture")
	default:
	}

	cancelApp()
	select {
	case <-appCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("app-server lifetime ignored explicit stop")
	}
}

func TestCodexLeadChildEnvUsesIsolatedHomeWithoutCopyingCredentials(t *testing.T) {
	sourceHome := t.TempDir()
	authPath := filepath.Join(sourceHome, "auth.json")
	configPath := filepath.Join(sourceHome, "config.toml")
	if err := os.WriteFile(authPath, []byte("credential bytes stay here"), 0600); err != nil {
		t.Fatalf("write auth fixture: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("model = \"test\"\n"), 0600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	runtimeHome := t.TempDir()

	env, err := codexLeadChildEnv(runtimeHome, []string{
		"PATH=/usr/bin",
		"CODEX_HOME=" + sourceHome,
		"CODEX_HOME=/must/not/survive",
	})
	if err != nil {
		t.Fatalf("codexLeadChildEnv() error = %v", err)
	}
	isolatedHome := filepath.Join(runtimeHome, "codex-home")
	wantEnv := "CODEX_HOME=" + isolatedHome
	count := 0
	for _, entry := range env {
		if entry == wantEnv {
			count++
		}
		if strings.HasPrefix(entry, "CODEX_HOME=") && entry != wantEnv {
			t.Fatalf("stale CODEX_HOME survived: %q", entry)
		}
	}
	if count != 1 {
		t.Fatalf("isolated CODEX_HOME count = %d, want 1: %#v", count, env)
	}
	for _, name := range []string{"auth.json", "config.toml"} {
		target := filepath.Join(isolatedHome, name)
		info, err := os.Lstat(target)
		if err != nil {
			t.Fatalf("lstat %s: %v", name, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s mode = %v, want symlink", name, info.Mode())
		}
		linked, err := os.Readlink(target)
		if err != nil {
			t.Fatalf("readlink %s: %v", name, err)
		}
		if linked != filepath.Join(sourceHome, name) {
			t.Fatalf("%s link = %q", name, linked)
		}
	}
}

func TestCodexLeadRuntimeBaseEnvUsesTrustedWorkspaceScope(t *testing.T) {
	env := codexLeadRuntimeBaseEnv(CodexLeadRuntimeConfig{
		Workspace: "  PROOF-WS  ",
		ConfigDir: "  /trusted/loom-data  ",
	}, []string{
		"PATH=/usr/bin",
		"LOOM_WORKSPACE=STALE",
		"LOOM_CONFIG_DIR=/forged/loom-data",
		"LOOM_FLEET_DB_API_KEY=secret",
		"LOOM_ARBITRARY=forged",
	})
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "PATH=/usr/bin") ||
		!strings.Contains(joined, "LOOM_WORKSPACE=PROOF-WS") ||
		!strings.Contains(joined, "LOOM_CONFIG_DIR=/trusted/loom-data") {
		t.Fatalf("runtime env missing trusted scope: %#v", env)
	}
	for _, forbidden := range []string{"LOOM_WORKSPACE=STALE", "/forged/loom-data", "secret", "forged"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("runtime env retained %q: %#v", forbidden, env)
		}
	}

	unscoped := codexLeadRuntimeBaseEnv(CodexLeadRuntimeConfig{}, []string{
		"PATH=/usr/bin",
		"LOOM_WORKSPACE=STALE",
		"LOOM_CONFIG_DIR=/forged/loom-data",
	})
	unscopedJoined := strings.Join(unscoped, "\n")
	if strings.Contains(unscopedJoined, "LOOM_WORKSPACE") || strings.Contains(unscopedJoined, "LOOM_CONFIG_DIR") {
		t.Fatalf("unscoped runtime inherited ambient Loom scope: %#v", unscoped)
	}
}

func TestCodexLeadChildEnvFailsClosedWithoutAuthentication(t *testing.T) {
	_, err := codexLeadChildEnv(t.TempDir(), []string{"CODEX_HOME=" + t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "auth.json") {
		t.Fatalf("codexLeadChildEnv() error = %v, want missing auth.json", err)
	}
}

func TestCodexLeadChildEnvRejectsCredentialLinkDrift(t *testing.T) {
	sourceHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceHome, "auth.json"), []byte("auth"), 0600); err != nil {
		t.Fatalf("write auth fixture: %v", err)
	}
	runtimeHome := t.TempDir()
	isolatedHome := filepath.Join(runtimeHome, "codex-home")
	if err := os.MkdirAll(isolatedHome, 0700); err != nil {
		t.Fatalf("mkdir isolated home: %v", err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "auth.json"), filepath.Join(isolatedHome, "auth.json")); err != nil {
		t.Fatalf("write drifted auth link: %v", err)
	}

	_, err := codexLeadChildEnv(runtimeHome, []string{"CODEX_HOME=" + sourceHome})
	if err == nil || !strings.Contains(err.Error(), "points outside") {
		t.Fatalf("codexLeadChildEnv() error = %v, want link drift rejection", err)
	}
}

func TestCodexThreadTranscriptEventsCanonicalizesMessages(t *testing.T) {
	capturedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	events := codexThreadTranscriptEvents(&CodexThread{
		ID: "thread-1",
		Turns: []CodexTurn{{
			ID: "turn-1",
			Items: []CodexTurnItem{
				{
					Type: "userMessage",
					ID:   "user-1",
					Content: []CodexContentBlock{
						{Type: "text", Text: "Please review this."},
						{Type: "image", Text: "ignored"},
					},
				},
				{Type: "reasoning", ID: "reasoning-1", Text: "ignored"},
				{Type: "agentMessage", ID: "agent-1", Text: "Review complete."},
			},
		}},
	}, capturedAt)

	if len(events) != 2 {
		t.Fatalf("events = %+v, want user and assistant messages", events)
	}
	if events[0].Seq != 1 || events[0].Role != transcript.RoleUser ||
		events[0].Type != transcript.EventText || events[0].Text != "Please review this." ||
		events[0].UUID != "user-1" || !events[0].Timestamp.Equal(capturedAt) {
		t.Fatalf("user event = %+v", events[0])
	}
	if events[1].Seq != 2 || events[1].Role != transcript.RoleAssistant ||
		events[1].Text != "Review complete." || events[1].UUID != "agent-1" {
		t.Fatalf("assistant event = %+v", events[1])
	}
}

func TestMarshalCanonicalTranscriptWithinLimitAddsTruncationEvidence(t *testing.T) {
	const captureLimit = 512
	capturedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	capture, err := marshalCanonicalTranscriptWithinLimit([]transcript.Event{
		{
			Seq:       1,
			Timestamp: capturedAt,
			Role:      transcript.RoleUser,
			Type:      transcript.EventText,
			Text:      "short event remains visible",
		},
		{
			Seq:       2,
			Timestamp: capturedAt,
			Role:      transcript.RoleAssistant,
			Type:      transcript.EventText,
			Text:      strings.Repeat("x", captureLimit),
		},
	}, captureLimit)
	if err != nil {
		t.Fatalf("marshalCanonicalTranscriptWithinLimit() error = %v", err)
	}
	if !capture.truncated {
		t.Fatal("capture was not marked truncated")
	}
	if len(capture.content) > captureLimit {
		t.Fatalf("capture length = %d, exceeds %d", len(capture.content), captureLimit)
	}

	lines := strings.Split(strings.TrimSpace(string(capture.content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("capture lines = %d, want retained event plus truncation marker: %s", len(lines), capture.content)
	}
	var retained transcript.Event
	if err := json.Unmarshal([]byte(lines[0]), &retained); err != nil {
		t.Fatalf("decode retained event: %v", err)
	}
	if retained.Text != "short event remains visible" {
		t.Fatalf("retained event = %+v", retained)
	}
	var marker transcript.Event
	if err := json.Unmarshal([]byte(lines[1]), &marker); err != nil {
		t.Fatalf("decode truncation marker: %v", err)
	}
	if marker.Seq != 2 || marker.Role != transcript.RoleSystem ||
		marker.Type != transcript.EventSessionMeta ||
		!strings.Contains(marker.Text, "Transcript truncated by Loom") {
		t.Fatalf("truncation marker = %+v", marker)
	}

	metadata := transcriptArtifactMetadata(map[string]string{"backend": "codex"}, capture)
	if metadata["transcript_truncated"] != "true" ||
		metadata["transcript_capture_limit_bytes"] != "512" ||
		metadata["transcript_truncation_reason"] != transcriptTruncationCanonical {
		t.Fatalf("truncation metadata = %#v", metadata)
	}
}

func TestMarshalCanonicalTranscriptEnforcesSharedEventLimit(t *testing.T) {
	capturedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	events := []transcript.Event{
		{Seq: 1, Timestamp: capturedAt, Role: transcript.RoleUser, Type: transcript.EventText, Text: "one"},
		{Seq: 2, Timestamp: capturedAt, Role: transcript.RoleAssistant, Type: transcript.EventText, Text: "two"},
		{Seq: 3, Timestamp: capturedAt, Role: transcript.RoleAssistant, Type: transcript.EventText, Text: "three"},
	}

	atLimit, err := marshalCanonicalTranscriptWithinLimitsAndState(events[:2], 4096, 2, false)
	if err != nil {
		t.Fatalf("marshal exactly at event limit: %v", err)
	}
	if atLimit.truncated {
		t.Fatal("transcript exactly at event limit was marked truncated")
	}
	if lines := strings.Split(strings.TrimSpace(string(atLimit.content)), "\n"); len(lines) != 2 {
		t.Fatalf("exact-limit lines = %d, want 2", len(lines))
	}

	overLimit, err := marshalCanonicalTranscriptWithinLimitsAndState(events, 4096, 2, false)
	if err != nil {
		t.Fatalf("marshal over event limit: %v", err)
	}
	if !overLimit.truncated || overLimit.truncationReason != transcriptTruncationCanonical {
		t.Fatalf("over-limit capture state = %+v", overLimit)
	}
	lines := strings.Split(strings.TrimSpace(string(overLimit.content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("over-limit lines = %d, want retained event plus marker", len(lines))
	}
	var marker transcript.Event
	if err := json.Unmarshal([]byte(lines[1]), &marker); err != nil {
		t.Fatalf("decode event-limit marker: %v", err)
	}
	if marker.Seq != 2 || marker.Role != transcript.RoleSystem ||
		marker.Type != transcript.EventSessionMeta ||
		!strings.Contains(marker.Text, "Transcript truncated by Loom") {
		t.Fatalf("event-limit marker = %+v", marker)
	}
}

func TestMarshalCanonicalTranscriptPropagatesSourcePaginationTruncation(t *testing.T) {
	capture, err := marshalCanonicalTranscriptWithSourceTruncation([]transcript.Event{{
		Seq:       1,
		Timestamp: time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC),
		Role:      transcript.RoleAssistant,
		Type:      transcript.EventText,
		Text:      "retained prefix",
	}}, true)
	if err != nil {
		t.Fatalf("marshalCanonicalTranscriptWithSourceTruncation() error = %v", err)
	}
	if !capture.truncated || capture.truncationReason != transcriptTruncationSource {
		t.Fatalf("capture state = %+v", capture)
	}
	lines := strings.Split(strings.TrimSpace(string(capture.content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("capture lines = %d, want event plus source truncation marker", len(lines))
	}
	var marker transcript.Event
	if err := json.Unmarshal([]byte(lines[1]), &marker); err != nil {
		t.Fatalf("decode source truncation marker: %v", err)
	}
	if marker.Role != transcript.RoleSystem || marker.Type != transcript.EventSessionMeta ||
		!strings.Contains(marker.Text, "Transcript truncated by Loom") {
		t.Fatalf("source truncation marker = %+v", marker)
	}
	metadata := transcriptArtifactMetadata(map[string]string{"backend": "codex"}, capture)
	if metadata["transcript_truncation_reason"] != transcriptTruncationSource ||
		metadata["transcript_source_limit_bytes"] != "50331648" ||
		metadata["transcript_source_truncation_cause"] != transcriptSourceCauseCodexText {
		t.Fatalf("source truncation metadata = %#v", metadata)
	}
}

func TestMarshalCanonicalTranscriptPreservesTypedSourceLimitEvidence(t *testing.T) {
	tests := []struct {
		name         string
		source       transcriptSourceTruncation
		metadataKey  string
		metadataWant string
		markerWant   string
	}{
		{
			name: "canonical events",
			source: transcriptSourceTruncation{
				truncated: true, limitEvents: 100_000,
				cause: transcriptSourceCauseCodexEvents,
			},
			metadataKey:  "transcript_source_limit_events",
			metadataWant: "100000",
			markerWant:   "100000 events",
		},
		{
			name: "scanned pages",
			source: transcriptSourceTruncation{
				truncated: true, limitPages: 100_000,
				cause: transcriptSourceCauseCodexPages,
			},
			metadataKey:  "transcript_source_limit_pages",
			metadataWant: "100000",
			markerWant:   "100000 pages",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capture, err := marshalCanonicalTranscriptWithSourceState([]transcript.Event{{
				Seq: 1, Timestamp: time.Now().UTC(),
				Role: transcript.RoleAssistant, Type: transcript.EventText, Text: "retained",
			}}, test.source)
			if err != nil {
				t.Fatalf("marshal typed source truncation: %v", err)
			}
			metadata := transcriptArtifactMetadata(map[string]string{}, capture)
			if metadata[test.metadataKey] != test.metadataWant ||
				metadata["transcript_source_truncation_cause"] != test.source.cause ||
				metadata["transcript_source_limit_bytes"] != "" {
				t.Fatalf("typed truncation metadata = %#v", metadata)
			}
			if !strings.Contains(string(capture.content), test.markerWant) {
				t.Fatalf("typed truncation marker = %s, want %q", capture.content, test.markerWant)
			}
		})
	}
}

func TestCaptureCodexInteractiveTranscriptPersistsSessionArtifactAndRef(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-session",
		AgentID:      "local-review",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
		Metadata:     map[string]string{"assignment": "preserve-me"},
	}); err != nil {
		t.Fatalf("create interactive session: %v", err)
	}
	if err := UpdateCodexRuntimeMetadata(ctx, testSessionRuntime(st), "WS", "lead-session", CodexRuntimeMetadata{
		Endpoint:   "ws://codex.test",
		ThreadID:   "thread-1",
		Status:     RuntimeStatusIdle,
		Controlled: true,
	}); err != nil {
		t.Fatalf("persist runtime metadata: %v", err)
	}

	fake := &transcriptCodexClient{thread: &CodexThread{
		ID:  "thread-1",
		Cwd: "/repo",
		Turns: []CodexTurn{{
			ID: "turn-1",
			Items: []CodexTurnItem{
				{
					Type:    "userMessage",
					ID:      "user-1",
					Content: []CodexContentBlock{{Type: "text", Text: "Review the branch."}},
				},
				{Type: "agentMessage", ID: "agent-1", Text: "No issues found."},
			},
		}},
	}}
	originalDial := dialCodexAppServerClient
	dialCodexAppServerClient = func(context.Context, string) (codexAppServerClient, error) {
		return fake, nil
	}
	t.Cleanup(func() { dialCodexAppServerClient = originalDial })

	err := captureCodexInteractiveTranscript(ctx, CodexLeadRuntimeConfig{
		Runtime: testSessionRuntime(st), Workspace: "WS", LeadName: "local-review",
		SessionID: "lead-session", WorkDir: "/repo",
	}, CodexRuntimeMetadata{Endpoint: "ws://127.0.0.1:1", ThreadID: "thread-1"}, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("captureCodexInteractiveTranscript() error = %v", err)
	}

	session, err := st.AgentSessions().Get(ctx, "WS", "lead-session")
	if err != nil {
		t.Fatalf("get captured session: %v", err)
	}
	if session.Metadata["transcript_ref"] != "artifact://transcript-lead-session" {
		t.Fatalf("transcript_ref = %q", session.Metadata["transcript_ref"])
	}
	if session.Metadata["assignment"] != "preserve-me" {
		t.Fatalf("concurrent metadata was lost: %#v", session.Metadata)
	}

	artifact, err := st.Artifacts().Get(ctx, "WS", "transcript-lead-session")
	if err != nil {
		t.Fatalf("get transcript artifact: %v", err)
	}
	if artifact.OwnerType != "session" || artifact.OwnerID != "lead-session" ||
		artifact.SessionID != "lead-session" || artifact.AgentID != "local-review" ||
		artifact.Type != "transcript" || artifact.MIMEType != "application/x-ndjson" ||
		artifact.DurableStatus != "finalized" {
		t.Fatalf("transcript artifact = %+v", artifact)
	}
	if artifact.Metadata["transcript_truncated"] != "" {
		t.Fatalf("small transcript incorrectly marked truncated: %#v", artifact.Metadata)
	}
	reader, ok := st.Artifacts().(store.ArtifactContentReader)
	if !ok {
		t.Fatal("memstore artifacts do not implement ArtifactContentReader")
	}
	content, err := reader.ReadContent(ctx, "WS", artifact.ArtifactID)
	if err != nil {
		t.Fatalf("read transcript content: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("transcript lines = %q, want two", content)
	}
	var got transcript.Event
	if err := json.Unmarshal([]byte(lines[1]), &got); err != nil {
		t.Fatalf("decode assistant event: %v", err)
	}
	if got.Seq != 2 || got.Role != transcript.RoleAssistant || got.Text != "No issues found." {
		t.Fatalf("assistant event = %+v", got)
	}
}

type transcriptCodexClient struct {
	thread *CodexThread
}

func (f *transcriptCodexClient) Close(string) error { return nil }

func (f *transcriptCodexClient) ListThreads(context.Context, string, int) ([]CodexThread, error) {
	if f.thread == nil {
		return nil, nil
	}
	return []CodexThread{*f.thread}, nil
}

func (f *transcriptCodexClient) ReadThread(context.Context, string) (*CodexThread, error) {
	return f.thread, nil
}

func (f *transcriptCodexClient) ReadThreadWithTurns(context.Context, string) (*CodexThread, error) {
	return f.thread, nil
}

func (f *transcriptCodexClient) ReadThreadTranscript(context.Context, string) (*CodexThread, error) {
	return f.thread, nil
}

func (f *transcriptCodexClient) StartTurn(context.Context, string, string) error { return nil }
