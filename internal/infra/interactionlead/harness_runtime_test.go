package leadcontrol

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/chat"
	transcriptclaude "github.com/olesho/harness-wrapper/pkg/transcript/claudecode"
	"github.com/olesho/harness-wrapper/pkg/wrapper"

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
	sessionRuntime := &storeBackedSessionRuntime{store: st}

	fake := newFakeHarnessConversation()
	fake.history = []chat.Turn{
		{ID: "user-1", Role: chat.RoleUser, State: chat.TurnStateComplete, Text: "lead prompt"},
	}
	fake.detachOutput = []byte("ready to lead")
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
			Runtime:          sessionRuntime,
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
	if _, err := DeliverLeadMessage(ctx, st, "WS", "nova", message, sessionRuntime); err != nil {
		t.Fatalf("DeliverLeadMessage() error = %v", err)
	}
	waitForCondition(t, func() bool {
		return string(fake.stdinBytes()) == message && len(fake.sentTexts()) == 1
	}, "session-owned inbox drain did not deliver the message")
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
	if fake.attachedOutputCount != 2 || fake.detachedOutputCount != 2 {
		t.Fatalf(
			"output sinks = attached %d, detached %d; want independent stdout and transcript sinks",
			fake.attachedOutputCount,
			fake.detachedOutputCount,
		)
	}
	ref := session.Metadata["transcript_ref"]
	if ref != "artifact://transcript-lead-session" {
		t.Fatalf("transcript_ref = %q, want durable session transcript", ref)
	}
	artifact, err := st.ArtifactQueries().GetArtifactRecord(ctx, "WS", "transcript-lead-session")
	if err != nil {
		t.Fatalf("get transcript artifact: %v", err)
	}
	if artifact.AgentID != "nova" || artifact.SessionID != "lead-session" ||
		artifact.OwnerType != "session" || artifact.OwnerID != "lead-session" ||
		artifact.Type != "transcript" {
		t.Fatalf("transcript artifact ownership = %+v", artifact)
	}
	content, err := st.ArtifactQueries().ReadArtifactContent(ctx, "WS", artifact.ArtifactID)
	if err != nil {
		t.Fatalf("read transcript content: %v", err)
	}
	if !strings.Contains(string(content), `"text":"ready to lead"`) {
		t.Fatalf("transcript content = %s", content)
	}
}

func TestRunHarnessLeadRuntimeTreatsStoreHistoryAsIncompleteWhenFinalEventIsLost(t *testing.T) {
	st := memstore.New()
	createAssignedLeadSessionWithBackend(t, st, "gemini")
	fake := newFakeHarnessConversation()
	fake.history = []chat.Turn{
		{ID: "user-1", Role: chat.RoleUser, State: chat.TurnStateComplete, Text: "lead prompt"},
		{
			ID: "assistant-1", Role: chat.RoleAssistant,
			State: chat.TurnStateComplete, Text: "earlier response",
		},
		{ID: "user-2", Role: chat.RoleUser, State: chat.TurnStateComplete, Text: "final question"},
		{ID: "assistant-2", Role: chat.RoleAssistant, State: chat.TurnStatePending},
	}
	fake.detachOutput = []byte("final response visible only in bounded terminal")

	origOpen := openHarnessConversation
	openHarnessConversation = func(context.Context, chat.Options) (harnessConversation, error) {
		return fake, nil
	}
	t.Cleanup(func() { openHarnessConversation = origOpen })

	var out bytes.Buffer
	runtimeErr := make(chan error, 1)
	go func() {
		runtimeErr <- RunHarnessLeadRuntime(t.Context(), HarnessLeadRuntimeConfig{
			Store:       st,
			Runtime:     testSessionRuntime(st),
			Workspace:   "WS",
			LeadName:    "nova",
			SessionID:   "lead-session",
			WorkDir:     "/repo",
			Prompt:      "lead prompt",
			Backend:     "gemini",
			HarnessName: HarnessNameGemini,
			Stdin:       strings.NewReader(""),
			Stdout:      &out,
			Stderr:      &out,
		})
	}()
	waitForCondition(t, func() bool { return lookupLeadConversation("lead-session") != nil },
		"conversation was not registered")
	close(fake.waitCh)

	select {
	case err := <-runtimeErr:
		if err != nil {
			t.Fatalf("RunHarnessLeadRuntime() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not finish after the final watcher event was lost")
	}

	artifact, err := st.ArtifactQueries().GetArtifactRecord(t.Context(), "WS", "transcript-lead-session")
	if err != nil {
		t.Fatalf("get transcript artifact: %v", err)
	}
	content, err := st.ArtifactQueries().ReadArtifactContent(t.Context(), "WS", artifact.ArtifactID)
	if err != nil {
		t.Fatalf("read transcript artifact: %v", err)
	}
	if !strings.Contains(string(content), "final response visible only in bounded terminal") {
		t.Fatalf("bounded terminal fallback missing after final event loss: %s", content)
	}
	if artifact.Metadata["transcript_truncated"] != "true" ||
		artifact.Metadata["transcript_source_truncation_cause"] !=
			transcriptSourceCauseHarnessTerminalBestEffort {
		t.Fatalf("store-backed completeness provenance = %#v", artifact.Metadata)
	}
}

func TestHarnessTranscriptEventsHandledHistoryDoesNotTreatIncompleteAssistantAsComplete(t *testing.T) {
	for _, state := range []chat.TurnState{
		chat.TurnStatePending,
		chat.TurnStateStreaming,
	} {
		t.Run(string(state), func(t *testing.T) {
			events, terminalFallbackRequired := harnessTranscriptEvents(
				"lead prompt",
				[]chat.Turn{
					{
						ID: "user-1", Role: chat.RoleUser,
						State: chat.TurnStateComplete, Text: "lead prompt",
					},
					{
						ID: "assistant-1", Role: chat.RoleAssistant,
						State: chat.TurnStateComplete, Text: "earlier complete response",
					},
					{
						ID: "user-2", Role: chat.RoleUser,
						State: chat.TurnStateComplete, Text: "final question",
					},
					{
						ID: "assistant-2", Role: chat.RoleAssistant,
						State: state, Text: "useful partial response",
					},
				},
				"bounded terminal completion",
				time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC),
				false,
			)
			if !terminalFallbackRequired {
				t.Fatal("handled history with incomplete assistant claimed completeness")
			}
			if len(events) != 5 ||
				events[len(events)-2].Text != "useful partial response" ||
				events[len(events)-1].Text != "bounded terminal completion" {
				t.Fatalf("incomplete assistant fallback events = %+v", events)
			}
		})
	}
}

func TestRunHarnessLeadRuntimeUsesValidatedRotatedSessionIDEverywhere(t *testing.T) {
	const (
		pinnedID  = "11111111-2222-4333-8444-555555555555"
		rotatedID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	)
	st := memstore.New()
	createAssignedLeadSessionWithBackend(t, st, "claude")
	fake := newFakeHarnessConversation()
	fake.harnessSessionID = rotatedID
	fake.boundedHistory = boundedHarnessHistory{
		handled: true,
		turns: []chat.Turn{
			{ID: "user-1", Role: chat.RoleUser, State: chat.TurnStateComplete, Text: "lead prompt"},
			{
				ID: "assistant-1", Role: chat.RoleAssistant,
				State: chat.TurnStateComplete, Text: "response from rotated transcript",
			},
		},
	}

	workDir := t.TempDir()
	configDir := filepath.Join(t.TempDir(), "claude-config")
	rotatedPath := filepath.Join(
		configDir,
		"projects",
		transcriptclaude.EncodedCWD(workDir),
		rotatedID+".jsonl",
	)
	preLaunchTime := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	rotatedMTime := preLaunchTime.Add(500 * time.Millisecond)
	postOpenTime := preLaunchTime.Add(3 * time.Second)
	clockTime := preLaunchTime
	originalNow := harnessRuntimeNow
	harnessRuntimeNow = func() time.Time { return clockTime }
	t.Cleanup(func() { harnessRuntimeNow = originalNow })

	origOpen := openHarnessConversation
	openHarnessConversation = func(context.Context, chat.Options) (harnessConversation, error) {
		if err := os.MkdirAll(filepath.Dir(rotatedPath), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(rotatedPath, []byte("{}\n"), 0o600); err != nil {
			return nil, err
		}
		if err := os.Chtimes(rotatedPath, rotatedMTime, rotatedMTime); err != nil {
			return nil, err
		}
		// Model metadata creation after chat.Open. The file belongs to this
		// launch but predates the post-open time by more than mtime slop.
		clockTime = postOpenTime
		return fake, nil
	}
	t.Cleanup(func() { openHarnessConversation = origOpen })

	var out bytes.Buffer
	runtimeErr := make(chan error, 1)
	go func() {
		runtimeErr <- RunHarnessLeadRuntime(t.Context(), HarnessLeadRuntimeConfig{
			Store:            st,
			Runtime:          testSessionRuntime(st),
			Workspace:        "WS",
			LeadName:         "nova",
			SessionID:        "lead-session",
			WorkDir:          workDir,
			Prompt:           "lead prompt",
			Backend:          "claude",
			HarnessName:      HarnessNameClaudeCode,
			HarnessSessionID: pinnedID,
			Env:              []string{"CLAUDE_CONFIG_DIR=" + configDir},
			Stdin:            strings.NewReader(""),
			Stdout:           &out,
			Stderr:           &out,
		})
	}()
	waitForCondition(t, func() bool { return lookupLeadConversation("lead-session") != nil },
		"conversation was not registered")
	startingSession := getLeadSession(t, st)
	if got := startingSession.Metadata[MetadataHarnessStartedAt]; got != preLaunchTime.Format(time.RFC3339Nano) {
		t.Fatalf("runtime freshness baseline = %q, want pre-launch %s", got, preLaunchTime)
	}
	close(fake.waitCh)

	select {
	case err := <-runtimeErr:
		if err != nil {
			t.Fatalf("RunHarnessLeadRuntime() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not finish after rotated transcript appeared")
	}
	if fake.boundedHistorySession != rotatedID {
		t.Fatalf("bounded capture session = %q, want rotated %q", fake.boundedHistorySession, rotatedID)
	}
	session := getLeadSession(t, st)
	if got := session.Metadata[MetadataHarnessSessionID]; got != rotatedID {
		t.Fatalf("final harness session id = %q, want rotated %q", got, rotatedID)
	}
	if got := session.Metadata["transcript_ref"]; got != "artifact://transcript-lead-session" {
		t.Fatalf("transcript_ref = %q", got)
	}
	artifact, err := st.ArtifactQueries().GetArtifactRecord(t.Context(), "WS", "transcript-lead-session")
	if err != nil {
		t.Fatalf("get transcript artifact: %v", err)
	}
	content, err := st.ArtifactQueries().ReadArtifactContent(t.Context(), "WS", artifact.ArtifactID)
	if err != nil {
		t.Fatalf("read transcript artifact: %v", err)
	}
	if !strings.Contains(string(content), "response from rotated transcript") {
		t.Fatalf("rotated transcript content = %s", content)
	}
}

func TestResolvePostCloseHarnessSessionIDRejectsStaleOrUnnecessaryRotation(t *testing.T) {
	const (
		pinnedID  = "11111111-2222-4333-8444-555555555555"
		rotatedID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	)
	workDir := t.TempDir()
	configDir := filepath.Join(t.TempDir(), "claude-config")
	projectDir := filepath.Join(
		configDir,
		"projects",
		transcriptclaude.EncodedCWD(workDir),
	)
	rotatedPath := filepath.Join(projectDir, rotatedID+".jsonl")
	writeHarnessTranscriptFixture(t, rotatedPath, "{}\n")
	startedAt := time.Now()
	staleTime := startedAt.Add(-time.Minute)
	if err := os.Chtimes(rotatedPath, staleTime, staleTime); err != nil {
		t.Fatalf("age rotated transcript: %v", err)
	}
	fake := newFakeHarnessConversation()
	fake.harnessSessionID = rotatedID
	cfg := HarnessLeadRuntimeConfig{
		WorkDir:     workDir,
		HarnessName: HarnessNameClaudeCode,
		Env:         []string{"CLAUDE_CONFIG_DIR=" + configDir},
	}
	runtime := HarnessRuntimeMetadata{
		HarnessSessionID: pinnedID,
		StartedAt:        startedAt,
	}
	if got := resolvePostCloseHarnessSessionID(t.Context(), cfg, fake, runtime); got != pinnedID {
		t.Fatalf("stale rotated id resolved to %q, want pinned %q", got, pinnedID)
	}

	freshTime := startedAt.Add(time.Minute)
	if err := os.Chtimes(rotatedPath, freshTime, freshTime); err != nil {
		t.Fatalf("refresh rotated transcript: %v", err)
	}
	pinnedPath := filepath.Join(projectDir, pinnedID+".jsonl")
	writeHarnessTranscriptFixture(t, pinnedPath, "{}\n")
	if got := resolvePostCloseHarnessSessionID(t.Context(), cfg, fake, runtime); got != pinnedID {
		t.Fatalf("rotation replaced existing pinned transcript with %q", got)
	}
}

func TestRunHarnessLeadRuntimePersistsBestEffortTranscriptAfterCloseDrainFailure(t *testing.T) {
	originalTimeout := harnessConversationCloseTimeout
	harnessConversationCloseTimeout = 25 * time.Millisecond
	t.Cleanup(func() { harnessConversationCloseTimeout = originalTimeout })

	tests := []struct {
		name        string
		exitCode    int
		wantExitErr bool
		configure   func(*fakeHarnessConversation)
	}{
		{
			name: "close error",
			configure: func(fake *fakeHarnessConversation) {
				fake.closeErr = errors.New("close failed")
			},
		},
		{
			name: "event drain timeout",
			// The close/drain failure must not replace the harness result.
			exitCode:    23,
			wantExitErr: true,
			configure: func(fake *fakeHarnessConversation) {
				fake.closeLeavesEventsOpen = true
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			st := memstore.New()
			createAssignedLeadSessionWithBackend(t, st, "opencode")
			fake := newFakeHarnessConversation()
			fake.detachOutput = []byte("bounded terminal survives close failure")
			fake.waitResult = wrapper.Result{ExitCode: test.exitCode}
			test.configure(fake)
			close(fake.waitCh)

			originalOpen := openHarnessConversation
			openHarnessConversation = func(context.Context, chat.Options) (harnessConversation, error) {
				return fake, nil
			}
			t.Cleanup(func() { openHarnessConversation = originalOpen })

			var out bytes.Buffer
			err := RunHarnessLeadRuntime(t.Context(), HarnessLeadRuntimeConfig{
				Store:       st,
				Runtime:     testSessionRuntime(st),
				Workspace:   "WS",
				LeadName:    "nova",
				SessionID:   "lead-session",
				WorkDir:     "/repo",
				Prompt:      "lead prompt",
				Backend:     "opencode",
				HarnessName: HarnessNameGeneric,
				Stdin:       strings.NewReader(""),
				Stdout:      &out,
				Stderr:      &out,
			})
			if test.wantExitErr {
				if err == nil || !strings.Contains(err.Error(), "exited with status 23") {
					t.Fatalf("RunHarnessLeadRuntime() exit error = %v, want original status", err)
				}
			} else if err != nil {
				t.Fatalf("RunHarnessLeadRuntime() changed successful exit semantics: %v", err)
			}
			if fake.boundedHistoryCalls != 0 || fake.historyCalls != 0 {
				t.Fatalf(
					"unsafe history read after close/drain failure: bounded=%d history=%d",
					fake.boundedHistoryCalls,
					fake.historyCalls,
				)
			}

			session := getLeadSession(t, st)
			if session.Metadata[MetadataRuntimeStatus] != RuntimeStatusDisconnected ||
				session.Metadata["transcript_ref"] != "artifact://transcript-lead-session" {
				t.Fatalf("final session metadata = %#v", session.Metadata)
			}
			artifact, err := st.ArtifactQueries().GetArtifactRecord(t.Context(), "WS", "transcript-lead-session")
			if err != nil {
				t.Fatalf("get transcript artifact: %v", err)
			}
			wantCause := transcriptSourceCauseHarnessCloseDrainUnavailable +
				"_and_" + transcriptSourceCauseHarnessTerminalBestEffort
			if artifact.Metadata["transcript_truncated"] != "true" ||
				artifact.Metadata["transcript_source_truncation_cause"] != wantCause {
				t.Fatalf("close/drain failure provenance = %#v", artifact.Metadata)
			}
			content, err := st.ArtifactQueries().ReadArtifactContent(t.Context(), "WS", artifact.ArtifactID)
			if err != nil {
				t.Fatalf("read transcript artifact: %v", err)
			}
			if !strings.Contains(string(content), `"text":"lead prompt"`) ||
				!strings.Contains(string(content), "bounded terminal survives close failure") ||
				!strings.Contains(string(content), transcriptSourceCauseHarnessCloseDrainUnavailable) {
				t.Fatalf("close/drain best-effort transcript = %s", content)
			}
		})
	}
}

func TestCaptureHarnessInteractiveTranscriptPersistsEveryControlledBackend(t *testing.T) {
	for _, backend := range []string{"claude", "gemini", "opencode", "cursor"} {
		t.Run(backend, func(t *testing.T) {
			st := memstore.New()
			createAssignedLeadSessionWithBackend(t, st, backend)
			fake := newFakeHarnessConversation()
			output := newBoundedHarnessOutput(1024)
			if backend == "claude" {
				fake.boundedHistory = boundedHarnessHistory{
					handled: true,
					turns: []chat.Turn{
						{ID: backend + "-user", Role: chat.RoleUser, State: chat.TurnStateComplete, Text: "document this"},
						{ID: backend + "-assistant", Role: chat.RoleAssistant, State: chat.TurnStateComplete, Text: backend + " native reply"},
					},
				}
			} else if backend == "gemini" {
				fake.history = []chat.Turn{
					{ID: backend + "-user", Role: chat.RoleUser, State: chat.TurnStateComplete, Text: "document this"},
					{ID: backend + "-assistant", Role: chat.RoleAssistant, State: chat.TurnStateComplete, Text: backend + " store reply"},
				}
				_, _ = output.Write([]byte("\x1b[32m" + backend + " terminal reply\x1b[0m\r\n"))
			} else {
				fake.history = []chat.Turn{
					{ID: backend + "-later-user", Role: chat.RoleUser, State: chat.TurnStateComplete, Text: "later question"},
					{ID: backend + "-later-assistant", Role: chat.RoleAssistant, State: chat.TurnStateComplete, Text: "later answer"},
				}
				_, _ = output.Write([]byte("\x1b[32m" + backend + " terminal reply\x1b[0m\r\n"))
			}
			cfg := HarnessLeadRuntimeConfig{
				Store:       st,
				Runtime:     testSessionRuntime(st),
				Workspace:   "WS",
				LeadName:    "nova",
				SessionID:   "lead-session",
				Prompt:      "document this",
				Backend:     backend,
				HarnessName: HarnessNameForBackend(backend),
			}
			if err := captureHarnessInteractiveTranscript(t.Context(), cfg, fake, output); err != nil {
				t.Fatalf("captureHarnessInteractiveTranscript() error = %v", err)
			}
			session := getLeadSession(t, st)
			if got := session.Metadata["transcript_ref"]; got != "artifact://transcript-lead-session" {
				t.Fatalf("transcript_ref = %q", got)
			}
			artifact, err := st.ArtifactQueries().GetArtifactRecord(t.Context(), "WS", "transcript-lead-session")
			if err != nil {
				t.Fatalf("get transcript artifact: %v", err)
			}
			if artifact.AgentID != "nova" || artifact.SessionID != "lead-session" ||
				artifact.OwnerType != "session" || artifact.OwnerID != "lead-session" {
				t.Fatalf("artifact ownership = %+v", artifact)
			}
			content, err := st.ArtifactQueries().ReadArtifactContent(t.Context(), "WS", artifact.ArtifactID)
			if err != nil {
				t.Fatalf("read transcript: %v", err)
			}
			wantOutput := backend + " native reply"
			if backend != "claude" {
				wantOutput = backend + " terminal reply"
			}
			if !strings.Contains(string(content), wantOutput) {
				t.Fatalf("transcript does not contain %q: %s", wantOutput, content)
			}
			if strings.Contains(string(content), "\x1b[") {
				t.Fatalf("transcript retained ANSI control bytes: %q", content)
			}
			if backend != "claude" {
				if artifact.Metadata["transcript_truncated"] != "true" ||
					artifact.Metadata["transcript_source_limit_bytes"] != "1024" ||
					artifact.Metadata["transcript_source_truncation_cause"] !=
						transcriptSourceCauseHarnessTerminalBestEffort ||
					!strings.Contains(string(content), "harness_terminal_best_effort") {
					t.Fatalf("terminal fallback completeness evidence = metadata %#v, content %s", artifact.Metadata, content)
				}
			} else if artifact.Metadata["transcript_truncated"] != "" {
				t.Fatalf("native transcript incorrectly marked truncated: %#v", artifact.Metadata)
			}
			wantHistoryCalls := 1
			if backend == "claude" {
				wantHistoryCalls = 0
			}
			if fake.boundedHistoryCalls != 1 || fake.historyCalls != wantHistoryCalls {
				t.Fatalf(
					"history calls = bounded %d, fallback %d; want fallback %d",
					fake.boundedHistoryCalls,
					fake.historyCalls,
					wantHistoryCalls,
				)
			}
		})
	}
}

func TestReadBoundedNativeHarnessHistoryUsesExactCodexAndClaudePaths(t *testing.T) {
	const (
		limit      = 1024
		workingDir = "/workspace/review"
	)
	tests := []struct {
		name        string
		harnessName string
		sessionID   string
		write       func(t *testing.T, root string)
		env         func(root string) []string
	}{
		{
			name:        "claude",
			harnessName: HarnessNameClaudeCode,
			sessionID:   "11111111-2222-4333-8444-555555555555",
			write: func(t *testing.T, root string) {
				t.Helper()
				path := filepath.Join(
					root,
					"claude-config",
					"projects",
					transcriptclaude.EncodedCWD(workingDir),
					"11111111-2222-4333-8444-555555555555.jsonl",
				)
				writeHarnessTranscriptFixture(t, path, strings.Repeat("x", limit+1))
			},
			env: func(root string) []string {
				return []string{
					"HOME=" + filepath.Join(root, "ignored-home"),
					"CLAUDE_CONFIG_DIR=" + filepath.Join(root, "claude-config"),
				}
			},
		},
		{
			name:        "codex",
			harnessName: HarnessNameCodex,
			sessionID:   "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
			write: func(t *testing.T, root string) {
				t.Helper()
				path := filepath.Join(
					root,
					"codex-home",
					"sessions",
					"2026",
					"07",
					"28",
					"rollout-2026-07-28T00-00-00-aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee.jsonl",
				)
				writeHarnessTranscriptFixture(t, path, strings.Repeat("x", limit+1))
			},
			env: func(root string) []string {
				return []string{
					"HOME=" + filepath.Join(root, "ignored-home"),
					"CODEX_HOME=" + filepath.Join(root, "codex-home"),
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			test.write(t, root)
			turns, truncated, err := readBoundedNativeHarnessHistory(
				t.Context(),
				test.harnessName,
				test.sessionID,
				workingDir,
				test.env(root),
				limit,
			)
			if err != nil {
				t.Fatalf("readBoundedNativeHarnessHistory() error = %v", err)
			}
			if !truncated {
				t.Fatal("oversized native transcript was not rejected at the raw-byte limit")
			}
			if len(turns) != 0 {
				t.Fatalf("oversized native transcript produced %d parsed turns", len(turns))
			}
		})
	}
}

func TestHistoryWithinRawLimitSeedsNativeCauseOnlyForActualTruncation(t *testing.T) {
	const (
		limit      = 1024
		workingDir = "/workspace/review"
		sessionID  = "11111111-2222-4333-8444-555555555555"
	)
	configDir := filepath.Join(t.TempDir(), "claude-config")
	path := filepath.Join(
		configDir,
		"projects",
		transcriptclaude.EncodedCWD(workingDir),
		sessionID+".jsonl",
	)
	writeHarnessTranscriptFixture(t, path, `{"type":"user","message":{"role":"user","content":"hello"},"timestamp":"2026-05-14T12:00:00Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}]},"timestamp":"2026-05-14T12:00:05Z"}
`)
	conv := &chatHarnessConversation{opts: chat.Options{
		Harness:    HarnessNameClaudeCode,
		WorkingDir: workingDir,
		Env:        []string{"CLAUDE_CONFIG_DIR=" + configDir},
	}}

	complete, err := conv.HistoryWithinRawLimit(t.Context(), sessionID, limit)
	if err != nil {
		t.Fatalf("read complete native history: %v", err)
	}
	if !complete.handled || complete.truncated || complete.limitBytes != 0 ||
		complete.sourceCause != "" || len(complete.turns) == 0 {
		t.Fatalf("complete native history state = %+v", complete)
	}

	writeHarnessTranscriptFixture(t, path, strings.Repeat("x", limit+1))
	truncated, err := conv.HistoryWithinRawLimit(t.Context(), sessionID, limit)
	if err != nil {
		t.Fatalf("read oversized native history: %v", err)
	}
	if !truncated.handled || !truncated.truncated ||
		truncated.limitBytes != limit ||
		truncated.sourceCause != transcriptSourceCauseHarnessNative {
		t.Fatalf("truncated native history state = %+v", truncated)
	}

	unavailable, err := conv.HistoryWithinRawLimit(
		t.Context(),
		"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
		limit,
	)
	if err == nil {
		t.Fatal("missing native history returned no error")
	}
	if !unavailable.handled || unavailable.truncated ||
		unavailable.limitBytes != 0 || unavailable.sourceCause != "" {
		t.Fatalf("unavailable native history incorrectly claimed a raw limit: %+v", unavailable)
	}
}

func TestHistoryWithinRawLimitFailsClosedForExternallyIdentifiedGeminiSession(t *testing.T) {
	conv := &chatHarnessConversation{opts: chat.Options{Harness: HarnessNameGemini}}
	history, err := conv.HistoryWithinRawLimit(
		t.Context(),
		"11111111-2222-4333-8444-555555555555",
		harnessNativeTranscriptRawLimit,
	)
	if err == nil || !strings.Contains(err.Error(), "bounded gemini") {
		t.Fatalf("known Gemini native history error = %v, want bounded-reader failure", err)
	}
	if !history.handled || history.truncated ||
		history.limitBytes != 0 || history.sourceCause != "" {
		t.Fatalf("known Gemini native history state = %+v", history)
	}
}

func TestCaptureHarnessInteractiveTranscriptPublishesWithoutLegacyStore(t *testing.T) {
	runtime := &storeBackedSessionRuntime{}
	fake := newFakeHarnessConversation()
	fake.history = []chat.Turn{
		{ID: "user-1", Role: chat.RoleUser, State: chat.TurnStateComplete, Text: "document this"},
		{ID: "assistant-1", Role: chat.RoleAssistant, State: chat.TurnStateComplete, Text: "done"},
	}
	cfg := HarnessLeadRuntimeConfig{
		Runtime: runtime, Workspace: "WS", SessionID: "lead-session",
		Prompt: "document this", Backend: "gemini", HarnessName: HarnessNameGemini,
	}
	if err := captureHarnessInteractiveTranscript(
		t.Context(), cfg, fake, newBoundedHarnessOutput(1024),
	); err != nil {
		t.Fatal(err)
	}
	if len(runtime.published) != 1 {
		t.Fatalf("published transcript commands = %d, want 1", len(runtime.published))
	}
	command := runtime.published[0]
	if command.WorkspaceKey != "WS" || command.SessionID != "lead-session" ||
		!strings.Contains(string(command.Content), `"text":"done"`) ||
		command.Metadata["runtime"] != "interactive-harness" || command.Metadata["backend"] != "gemini" {
		t.Fatalf("published transcript command = %#v", command)
	}
}

func TestCaptureHarnessInteractiveTranscriptNativeLimitSkipsUnboundedHistory(t *testing.T) {
	st := memstore.New()
	createAssignedLeadSessionWithBackend(t, st, "claude")
	fake := newFakeHarnessConversation()
	fake.boundedHistory = boundedHarnessHistory{
		handled:     true,
		truncated:   true,
		limitBytes:  harnessNativeTranscriptRawLimit,
		sourceCause: transcriptSourceCauseHarnessNative,
	}
	output := newBoundedHarnessOutput(harnessOutputCaptureLimit)
	_, _ = output.Write([]byte("bounded terminal fallback"))

	cfg := HarnessLeadRuntimeConfig{
		Store:            st,
		Runtime:          testSessionRuntime(st),
		Workspace:        "WS",
		LeadName:         "nova",
		SessionID:        "lead-session",
		WorkDir:          "/workspace/review",
		Prompt:           "document this",
		Backend:          "claude",
		HarnessName:      HarnessNameClaudeCode,
		HarnessSessionID: "11111111-2222-4333-8444-555555555555",
	}
	if err := captureHarnessInteractiveTranscript(t.Context(), cfg, fake, output); err != nil {
		t.Fatalf("captureHarnessInteractiveTranscript() error = %v", err)
	}
	if fake.boundedHistoryCalls != 1 || fake.historyCalls != 0 {
		t.Fatalf(
			"history calls = bounded %d, unbounded %d; oversized native history reached History",
			fake.boundedHistoryCalls,
			fake.historyCalls,
		)
	}
	if fake.boundedHistorySession != "11111111-2222-4333-8444-555555555555" {
		t.Fatalf("bounded native history session = %q, want launch-pinned session", fake.boundedHistorySession)
	}

	artifact, err := st.ArtifactQueries().GetArtifactRecord(t.Context(), "WS", "transcript-lead-session")
	if err != nil {
		t.Fatalf("get transcript artifact: %v", err)
	}
	if artifact.Metadata["transcript_truncated"] != "true" ||
		artifact.Metadata["transcript_truncation_reason"] != transcriptTruncationSource ||
		artifact.Metadata["transcript_source_limit_bytes"] != "4194304" ||
		artifact.Metadata["transcript_native_history_limit_bytes"] != "8388608" ||
		artifact.Metadata["transcript_source_truncation_cause"] !=
			transcriptSourceCauseHarnessNative+"_and_"+transcriptSourceCauseHarnessTerminalBestEffort {
		t.Fatalf("native truncation metadata = %#v", artifact.Metadata)
	}
	content, err := st.ArtifactQueries().ReadArtifactContent(t.Context(), "WS", artifact.ArtifactID)
	if err != nil {
		t.Fatalf("read transcript artifact: %v", err)
	}
	if !strings.Contains(string(content), "bounded terminal fallback") ||
		!strings.Contains(string(content), `"type":"session_meta"`) ||
		!strings.Contains(string(content), "source capture limit: 4194304 bytes") ||
		!strings.Contains(string(content), transcriptSourceCauseHarnessTerminalBestEffort) {
		t.Fatalf("bounded fallback/truncation marker missing from transcript: %s", content)
	}
}

func TestCaptureHarnessInteractiveTranscriptPersistsTerminalOutputTruncation(t *testing.T) {
	st := memstore.New()
	createAssignedLeadSessionWithBackend(t, st, "opencode")
	fake := newFakeHarnessConversation()
	const outputLimit = 64
	output := newBoundedHarnessOutput(outputLimit)
	_, _ = output.Write([]byte(strings.Repeat("terminal-output-", 20)))

	cfg := HarnessLeadRuntimeConfig{
		Store:       st,
		Runtime:     testSessionRuntime(st),
		Workspace:   "WS",
		LeadName:    "nova",
		SessionID:   "lead-session",
		Prompt:      "document this",
		Backend:     "opencode",
		HarnessName: HarnessNameGeneric,
	}
	if err := captureHarnessInteractiveTranscript(t.Context(), cfg, fake, output); err != nil {
		t.Fatalf("captureHarnessInteractiveTranscript() error = %v", err)
	}

	artifact, err := st.ArtifactQueries().GetArtifactRecord(t.Context(), "WS", "transcript-lead-session")
	if err != nil {
		t.Fatalf("get transcript artifact: %v", err)
	}
	if artifact.Metadata["transcript_source_limit_bytes"] != "64" ||
		artifact.Metadata["transcript_source_truncation_cause"] !=
			transcriptSourceCauseHarnessTerminalBestEffort+"_and_"+transcriptSourceCauseHarnessTerminal {
		t.Fatalf("terminal truncation metadata = %#v", artifact.Metadata)
	}
	content, err := st.ArtifactQueries().ReadArtifactContent(t.Context(), "WS", artifact.ArtifactID)
	if err != nil {
		t.Fatalf("read transcript artifact: %v", err)
	}
	if !strings.Contains(string(content), "[terminal output truncated by Loom]") ||
		!strings.Contains(string(content), `"type":"session_meta"`) ||
		!strings.Contains(string(content), "source capture limit: 64 bytes") {
		t.Fatalf("terminal truncation evidence missing from transcript: %s", content)
	}
}

func TestCaptureHarnessInteractiveTranscriptMarksRequiredEmptyTerminalFallback(t *testing.T) {
	st := memstore.New()
	createAssignedLeadSessionWithBackend(t, st, "opencode")
	fake := newFakeHarnessConversation()
	output := newBoundedHarnessOutput(1024)

	cfg := HarnessLeadRuntimeConfig{
		Store:       st,
		Runtime:     testSessionRuntime(st),
		Workspace:   "WS",
		LeadName:    "nova",
		SessionID:   "lead-session",
		Prompt:      "document this",
		Backend:     "opencode",
		HarnessName: HarnessNameGeneric,
	}
	if err := captureHarnessInteractiveTranscript(t.Context(), cfg, fake, output); err != nil {
		t.Fatalf("captureHarnessInteractiveTranscript() error = %v", err)
	}

	artifact, err := st.ArtifactQueries().GetArtifactRecord(t.Context(), "WS", "transcript-lead-session")
	if err != nil {
		t.Fatalf("get transcript artifact: %v", err)
	}
	if artifact.Metadata["transcript_truncated"] != "true" ||
		artifact.Metadata["transcript_source_limit_bytes"] != "1024" ||
		artifact.Metadata["transcript_source_truncation_cause"] !=
			transcriptSourceCauseHarnessTerminalBestEffort {
		t.Fatalf("empty terminal fallback evidence = %#v", artifact.Metadata)
	}
	content, err := st.ArtifactQueries().ReadArtifactContent(t.Context(), "WS", artifact.ArtifactID)
	if err != nil {
		t.Fatalf("read transcript artifact: %v", err)
	}
	if !strings.Contains(string(content), `"text":"document this"`) ||
		!strings.Contains(string(content), transcriptSourceCauseHarnessTerminalBestEffort) {
		t.Fatalf("prompt-only fallback lacks best-effort marker: %s", content)
	}
}

func TestHarnessTranscriptHistoryUnavailableDoesNotMintNativeLimitEvidence(t *testing.T) {
	source := harnessTranscriptSourceTruncation(
		boundedHarnessHistory{
			handled:     true,
			truncated:   false,
			limitBytes:  harnessNativeTranscriptRawLimit,
			sourceCause: transcriptSourceCauseHarnessNative,
		},
		errors.New("native transcript unavailable"),
		boundedHarnessOutputSnapshot{},
		false,
	)
	if !source.truncated ||
		source.cause != transcriptSourceCauseHarnessHistoryUnavailable ||
		source.limitBytes != 0 {
		t.Fatalf("history-unavailable source evidence = %+v", source)
	}
}

func TestCaptureHarnessInteractiveTranscriptMarksHistoryUnavailable(t *testing.T) {
	st := memstore.New()
	createAssignedLeadSessionWithBackend(t, st, "gemini")
	fake := newFakeHarnessConversation()
	fake.historyErr = errors.New("history store unavailable")
	output := newBoundedHarnessOutput(1024)

	cfg := HarnessLeadRuntimeConfig{
		Store:       st,
		Runtime:     testSessionRuntime(st),
		Workspace:   "WS",
		LeadName:    "nova",
		SessionID:   "lead-session",
		Prompt:      "document this",
		Backend:     "gemini",
		HarnessName: HarnessNameGemini,
	}
	if err := captureHarnessInteractiveTranscript(t.Context(), cfg, fake, output); err != nil {
		t.Fatalf("captureHarnessInteractiveTranscript() error = %v", err)
	}

	artifact, err := st.ArtifactQueries().GetArtifactRecord(t.Context(), "WS", "transcript-lead-session")
	if err != nil {
		t.Fatalf("get transcript artifact: %v", err)
	}
	if artifact.Metadata["transcript_truncated"] != "true" ||
		artifact.Metadata["transcript_source_truncation_cause"] !=
			transcriptSourceCauseHarnessHistoryUnavailable+"_and_"+
				transcriptSourceCauseHarnessTerminalBestEffort ||
		artifact.Metadata["transcript_source_limit_bytes"] != "1024" ||
		artifact.Metadata["transcript_native_history_limit_bytes"] != "" {
		t.Fatalf("history-unavailable evidence = %#v", artifact.Metadata)
	}
	content, err := st.ArtifactQueries().ReadArtifactContent(t.Context(), "WS", artifact.ArtifactID)
	if err != nil {
		t.Fatalf("read transcript artifact: %v", err)
	}
	if !strings.Contains(string(content), `"text":"document this"`) ||
		!strings.Contains(string(content), transcriptSourceCauseHarnessHistoryUnavailable) ||
		!strings.Contains(string(content), transcriptSourceCauseHarnessTerminalBestEffort) ||
		strings.Contains(string(content), transcriptSourceCauseHarnessNative) {
		t.Fatalf("history-unavailable marker missing from transcript: %s", content)
	}
}

func writeHarnessTranscriptFixture(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create transcript fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write transcript fixture: %v", err)
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
