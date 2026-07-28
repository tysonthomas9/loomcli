package leadcontrol

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/olesho/harness-wrapper/pkg/chat"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	harnessTranscriptCaptureTimeout = 10 * time.Second
	harnessOutputCaptureLimit       = 4 << 20
	harnessNativeTranscriptRawLimit = 8 << 20

	transcriptSourceCauseHarnessCloseDrainUnavailable = "harness_close_drain_unavailable"
)

// boundedHarnessOutput retains the beginning of the PTY stream as a fallback
// for generic harnesses (OpenCode and Cursor), whose adapters do not expose a
// native transcript reader. The cap prevents an interactive TUI from growing
// Loom's memory without bound.
type boundedHarnessOutput struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
}

func newBoundedHarnessOutput(limit int) *boundedHarnessOutput {
	if limit <= 0 {
		limit = harnessOutputCaptureLimit
	}
	return &boundedHarnessOutput{limit: limit}
}

func (b *boundedHarnessOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		b.data = append(b.data, p[:remaining]...)
	}
	if remaining < len(p) {
		b.truncated = true
	}
	return len(p), nil
}

func (b *boundedHarnessOutput) text() string {
	return b.snapshot().text
}

type boundedHarnessOutputSnapshot struct {
	text       string
	truncated  bool
	limitBytes int
}

type harnessTranscriptTarget struct {
	workspace string
	sessionID string
	session   *domain.AgentSession
}

type harnessTranscriptCollection struct {
	events           []transcript.Event
	history          boundedHarnessHistory
	sourceTruncation transcriptSourceTruncation
}

func (b *boundedHarnessOutput) snapshot() boundedHarnessOutputSnapshot {
	if b == nil {
		return boundedHarnessOutputSnapshot{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	text := strings.TrimSpace(ansi.Strip(strings.ReplaceAll(string(b.data), "\r", "")))
	if b.truncated {
		text = strings.TrimSpace(text + "\n\n[terminal output truncated by Loom]")
	}
	return boundedHarnessOutputSnapshot{
		text:       text,
		truncated:  b.truncated,
		limitBytes: b.limit,
	}
}

func captureHarnessInteractiveTranscript(
	ctx context.Context,
	cfg HarnessLeadRuntimeConfig,
	conv harnessConversation,
	output *boundedHarnessOutput,
) error {
	return captureHarnessInteractiveTranscriptWithUnavailableSource(
		ctx,
		cfg,
		conv,
		output,
		"",
	)
}

func captureHarnessInteractiveTranscriptWithUnavailableSource(
	ctx context.Context,
	cfg HarnessLeadRuntimeConfig,
	conv harnessConversation,
	output *boundedHarnessOutput,
	unavailableSourceCause string,
) error {
	target, err := loadHarnessTranscriptTarget(ctx, cfg)
	if err != nil {
		return err
	}
	if target == nil {
		return nil
	}
	collection, err := collectHarnessTranscript(
		ctx,
		cfg,
		conv,
		output,
		unavailableSourceCause,
	)
	if err != nil {
		return err
	}
	capture, err := marshalCanonicalTranscriptWithSourceState(
		collection.events,
		collection.sourceTruncation,
	)
	if err != nil {
		return fmt.Errorf("marshal harness transcript: %w", err)
	}
	artifactID, err := uploadHarnessTranscriptArtifact(ctx, cfg, target, collection.history, capture)
	if err != nil {
		return err
	}
	return persistHarnessTranscriptReference(ctx, cfg, conv, target, artifactID)
}

func loadHarnessTranscriptTarget(
	ctx context.Context,
	cfg HarnessLeadRuntimeConfig,
) (*harnessTranscriptTarget, error) {
	if cfg.Store == nil || cfg.Store.AgentSessions() == nil || cfg.Store.Artifacts() == nil {
		return nil, nil
	}
	workspace := strings.TrimSpace(cfg.Workspace)
	sessionID := strings.TrimSpace(cfg.SessionID)
	if workspace == "" || sessionID == "" {
		return nil, nil
	}
	session, err := cfg.Store.AgentSessions().Get(ctx, workspace, sessionID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load harness interactive session: %w", err)
	}
	return &harnessTranscriptTarget{
		workspace: workspace,
		sessionID: sessionID,
		session:   session,
	}, nil
}

func collectHarnessTranscript(
	ctx context.Context,
	cfg HarnessLeadRuntimeConfig,
	conv harnessConversation,
	output *boundedHarnessOutput,
	unavailableSourceCause string,
) (harnessTranscriptCollection, error) {
	if unavailableSourceCause != "" {
		return collectUnavailableHarnessTranscript(
			cfg.Prompt,
			output,
			unavailableSourceCause,
		), nil
	}

	harnessSessionID := strings.TrimSpace(cfg.HarnessSessionID)
	if harnessSessionID == "" {
		harnessSessionID = strings.TrimSpace(conv.HarnessSessionID())
	}
	history, historyErr := conv.HistoryWithinRawLimit(
		ctx,
		harnessSessionID,
		harnessNativeTranscriptRawLimit,
	)
	if !history.handled {
		history.turns, historyErr = conv.History(ctx)
	}
	outputSnapshot := output.snapshot()
	events, terminalFallbackRequired := harnessTranscriptEvents(
		cfg.Prompt,
		history.turns,
		outputSnapshot.text,
		time.Now().UTC(),
		!history.handled,
	)
	if historyErr != nil && len(events) == 0 {
		return harnessTranscriptCollection{},
			fmt.Errorf("read harness transcript history: %w", historyErr)
	}
	return harnessTranscriptCollection{
		events:  events,
		history: history,
		sourceTruncation: harnessTranscriptSourceTruncation(
			history,
			historyErr,
			outputSnapshot,
			terminalFallbackRequired,
		),
	}, nil
}

func collectUnavailableHarnessTranscript(
	prompt string,
	output *boundedHarnessOutput,
	unavailableSourceCause string,
) harnessTranscriptCollection {
	outputSnapshot := output.snapshot()
	events, _ := harnessTranscriptEvents(
		prompt,
		nil,
		outputSnapshot.text,
		time.Now().UTC(),
		true,
	)
	source := addHarnessTerminalFallbackSource(
		transcriptSourceTruncation{
			truncated: true,
			cause:     unavailableSourceCause,
		},
		outputSnapshot,
	)
	return harnessTranscriptCollection{
		events:           events,
		sourceTruncation: source,
	}
}

func harnessTranscriptSourceTruncation(
	history boundedHarnessHistory,
	historyErr error,
	output boundedHarnessOutputSnapshot,
	terminalFallbackRequired bool,
) transcriptSourceTruncation {
	source := transcriptSourceTruncation{}
	if history.truncated {
		source = transcriptSourceTruncation{
			truncated:  true,
			limitBytes: history.limitBytes,
			cause:      history.sourceCause,
		}
	}
	if historyErr != nil {
		source.truncated = true
		source.cause = joinTranscriptSourceCauses(
			source.cause,
			transcriptSourceCauseHarnessHistoryUnavailable,
		)
	}
	if !terminalFallbackRequired {
		return source
	}
	return addHarnessTerminalFallbackSource(source, output)
}

func addHarnessTerminalFallbackSource(
	source transcriptSourceTruncation,
	output boundedHarnessOutputSnapshot,
) transcriptSourceTruncation {
	// harness-wrapper's chat AttachOutput API is intentionally best-effort:
	// its fixed-size fanout queue can drop a burst before this bounded writer
	// observes it. A transcript that requires terminal fallback must never
	// claim completeness, even when no terminal bytes reached the writer.
	source.truncated = true
	source.limitBytes = output.limitBytes
	terminalCause := transcriptSourceCauseHarnessTerminalBestEffort
	if output.truncated {
		terminalCause = joinTranscriptSourceCauses(
			terminalCause,
			transcriptSourceCauseHarnessTerminal,
		)
	}
	source.cause = joinTranscriptSourceCauses(source.cause, terminalCause)
	return source
}

func joinTranscriptSourceCauses(first, second string) string {
	if first == "" {
		return second
	}
	if second == "" {
		return first
	}
	return first + "_and_" + second
}

func uploadHarnessTranscriptArtifact(
	ctx context.Context,
	cfg HarnessLeadRuntimeConfig,
	target *harnessTranscriptTarget,
	history boundedHarnessHistory,
	capture canonicalTranscriptCapture,
) (string, error) {
	artifactMetadata := transcriptArtifactMetadata(map[string]string{
		"runtime": "interactive-harness",
		"backend": cfg.Backend,
		"harness": cfg.HarnessName,
	}, capture)
	if history.truncated && history.limitBytes > 0 {
		artifactMetadata["transcript_native_history_limit_bytes"] =
			strconv.Itoa(history.limitBytes)
	}
	finalized, err := store.UploadContentArtifact(ctx, cfg.Store.Artifacts(), store.ArtifactCreate{
		WorkspaceKey:  target.workspace,
		ArtifactID:    "transcript-" + target.sessionID,
		AgentID:       target.session.AgentID,
		SessionID:     target.sessionID,
		TaskID:        target.session.TaskID,
		OwnerType:     "session",
		OwnerID:       target.sessionID,
		Type:          "transcript",
		Summary:       fmt.Sprintf("interactive %s session transcript", cfg.Backend),
		MIMEType:      "application/x-ndjson",
		DurableStatus: "declared",
		Metadata:      artifactMetadata,
	}, capture.content)
	if err != nil {
		return "", fmt.Errorf("upload harness transcript artifact: %w", err)
	}
	return finalized.ArtifactID, nil
}

func persistHarnessTranscriptReference(
	ctx context.Context,
	cfg HarnessLeadRuntimeConfig,
	conv harnessConversation,
	target *harnessTranscriptTarget,
	artifactID string,
) error {
	// Re-read immediately before the metadata update so transcript capture does
	// not overwrite runtime state written by the watcher during finalization.
	session, err := cfg.Store.AgentSessions().Get(ctx, target.workspace, target.sessionID)
	if err != nil {
		return fmt.Errorf("reload harness interactive session: %w", err)
	}
	metadata := cloneMetadata(session.Metadata)
	metadata["transcript_ref"] = "artifact://" + artifactID
	harnessSessionID := strings.TrimSpace(cfg.HarnessSessionID)
	if harnessSessionID == "" {
		harnessSessionID = strings.TrimSpace(conv.HarnessSessionID())
	}
	if harnessSessionID != "" {
		metadata[MetadataHarnessSessionID] = harnessSessionID
	}
	if _, err := cfg.Store.AgentSessions().Update(
		ctx,
		target.workspace,
		target.sessionID,
		store.AgentSessionUpdate{Metadata: &metadata},
	); err != nil {
		return fmt.Errorf("persist harness transcript ref: %w", err)
	}
	return nil
}

type harnessTranscriptPresence struct {
	hasUser                bool
	hasAssistant           bool
	hasIncompleteAssistant bool
	hasInitialPrompt       bool
}

func harnessTranscriptEvents(
	prompt string,
	turns []chat.Turn,
	terminalOutput string,
	capturedAt time.Time,
	forceTerminalFallback bool,
) ([]transcript.Event, bool) {
	if capturedAt.IsZero() {
		capturedAt = time.Now().UTC()
	} else {
		capturedAt = capturedAt.UTC()
	}
	prompt = strings.TrimSpace(prompt)
	events, presence := harnessTurnTranscriptEvents(turns, prompt, capturedAt)
	if !presence.hasUser && prompt != "" {
		events = append([]transcript.Event{{
			Timestamp: capturedAt,
			Role:      transcript.RoleUser,
			Type:      transcript.EventText,
			Text:      prompt,
		}}, events...)
	}
	// The initial CLI prompt is passed as a process argument and therefore is
	// absent from the chat store fallback. If History lacks that prompt, retain
	// the PTY stream even when later injected turns produced assistant records;
	// otherwise OpenCode/Cursor/Gemini would lose their first response.
	terminalFallbackRequired := forceTerminalFallback ||
		!presence.hasAssistant ||
		presence.hasIncompleteAssistant ||
		(prompt != "" && !presence.hasInitialPrompt) ||
		lastHarnessConversationRole(events) == transcript.RoleUser
	if terminalFallbackRequired && strings.TrimSpace(terminalOutput) != "" {
		events = append(events, transcript.Event{
			Timestamp: capturedAt,
			Role:      transcript.RoleAssistant,
			Type:      transcript.EventText,
			Text:      strings.TrimSpace(terminalOutput),
		})
	}
	for i := range events {
		events[i].Seq = i + 1
	}
	return events, terminalFallbackRequired
}

func lastHarnessConversationRole(events []transcript.Event) string {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Role == transcript.RoleUser ||
			events[index].Role == transcript.RoleAssistant {
			return events[index].Role
		}
	}
	return ""
}

func harnessTurnTranscriptEvents(
	turns []chat.Turn,
	prompt string,
	capturedAt time.Time,
) ([]transcript.Event, harnessTranscriptPresence) {
	events := make([]transcript.Event, 0, len(turns)+2)
	var presence harnessTranscriptPresence
	for _, turn := range turns {
		if turn.Role == chat.RoleAssistant &&
			(turn.State == chat.TurnStatePending || turn.State == chat.TurnStateStreaming) {
			presence.hasIncompleteAssistant = true
		}
		event, ok := harnessTurnTranscriptEvent(turn, capturedAt)
		if !ok {
			continue
		}
		switch event.Role {
		case transcript.RoleUser:
			presence.hasUser = true
			if prompt != "" && event.Text == prompt {
				presence.hasInitialPrompt = true
			}
		case transcript.RoleAssistant:
			presence.hasAssistant = true
		}
		events = append(events, event)
	}
	return events, presence
}

func harnessTurnTranscriptEvent(
	turn chat.Turn,
	capturedAt time.Time,
) (transcript.Event, bool) {
	text := strings.TrimSpace(turn.Text)
	role := string(turn.Role)
	if text == "" ||
		(role != transcript.RoleUser &&
			role != transcript.RoleAssistant &&
			role != transcript.RoleSystem) {
		return transcript.Event{}, false
	}
	at := turn.StartedAt
	if at.IsZero() {
		at = turn.CompletedAt
	}
	if at.IsZero() {
		at = capturedAt
	}
	return transcript.Event{
		Timestamp: at.UTC(),
		Role:      role,
		Type:      transcript.EventText,
		Text:      text,
		UUID:      strings.TrimSpace(turn.ID),
	}, true
}
