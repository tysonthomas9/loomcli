package leadcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	codexTranscriptCaptureTimeout = 10 * time.Second

	// FleetDB accepts artifact content up to 64 MiB. Keep one MiB of headroom
	// so the canonical transcript, including a truncation marker, is always
	// safely below that persisted artifact ceiling.
	maxCanonicalTranscriptBytes = (64 << 20) - (1 << 20)
)

type canonicalTranscriptCapture struct {
	content           []byte
	truncated         bool
	truncationReason  string
	limitBytes        int
	sourceLimitBytes  int
	sourceLimitEvents int
	sourceLimitPages  int
	sourceCause       string
}

type transcriptSourceTruncation struct {
	truncated   bool
	limitBytes  int
	limitEvents int
	limitPages  int
	cause       string
}

const (
	transcriptTruncationSource    = "source_history_limit"
	transcriptTruncationCanonical = "canonical_output_limit"
	transcriptTruncationBoth      = "source_and_canonical_limits"

	transcriptSourceCauseCodexText                 = "codex_history_text_limit"
	transcriptSourceCauseCodexEvents               = "codex_history_event_limit"
	transcriptSourceCauseCodexPages                = "codex_history_page_limit"
	transcriptSourceCauseHarnessHistoryUnavailable = "harness_history_unavailable"
	transcriptSourceCauseHarnessNative             = "harness_native_history_raw_limit"
	transcriptSourceCauseHarnessTerminal           = "harness_terminal_output_limit"
	transcriptSourceCauseHarnessTerminalBestEffort = "harness_terminal_best_effort"
)

type codexTranscriptCaptureTarget struct {
	workspace string
	sessionID string
	agentID   string
	taskID    string
	runtime   CodexRuntimeMetadata
}

// captureCodexInteractiveTranscript reads the controlled Codex thread while
// its app-server is still alive, converts user/assistant messages to Loom's
// canonical transcript format, and stores the result as a durable
// session-owned artifact. Capture is best-effort at the runtime call site so a
// transcript persistence failure never changes the interactive agent's exit
// result.
func captureCodexInteractiveTranscript(
	ctx context.Context,
	cfg CodexLeadRuntimeConfig,
	runtime CodexRuntimeMetadata,
	runtimeStartedAt time.Time,
) error {
	target, err := resolveCodexTranscriptCaptureTarget(ctx, cfg, runtime, runtimeStartedAt)
	if err != nil || target == nil {
		return err
	}
	capture, err := readCodexTranscriptContent(ctx, target.runtime)
	if err != nil {
		return err
	}
	finalized, err := uploadCodexTranscriptArtifact(ctx, cfg, *target, capture)
	if err != nil {
		return err
	}
	return persistCodexTranscriptRef(ctx, cfg, *target, finalized.ArtifactID)
}

func resolveCodexTranscriptCaptureTarget(
	ctx context.Context,
	cfg CodexLeadRuntimeConfig,
	runtime CodexRuntimeMetadata,
	runtimeStartedAt time.Time,
) (*codexTranscriptCaptureTarget, error) {
	if cfg.Store == nil || cfg.Store.AgentSessions() == nil {
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
		return nil, fmt.Errorf("load codex interactive session: %w", err)
	}
	persisted := RuntimeMetadataFromSession(session)
	if runtime.Endpoint == "" {
		runtime.Endpoint = persisted.Endpoint
	}
	if runtime.ThreadID == "" {
		runtime.ThreadID = persisted.ThreadID
	}
	if runtime.Endpoint == "" {
		return nil, errors.New("capture codex transcript: app-server endpoint unavailable")
	}

	if runtime.ThreadID == "" {
		thread, findErr := findNewestCodexThread(ctx, runtime.Endpoint, cfg.WorkDir, runtimeStartedAt)
		if findErr != nil {
			return nil, fmt.Errorf("find codex transcript thread: %w", findErr)
		}
		if thread == nil {
			return nil, errors.New("capture codex transcript: thread unavailable")
		}
		runtime.ThreadID = thread.ID
	}
	return &codexTranscriptCaptureTarget{
		workspace: workspace,
		sessionID: sessionID,
		agentID:   session.AgentID,
		taskID:    session.TaskID,
		runtime:   runtime,
	}, nil
}

func readCodexTranscriptContent(ctx context.Context, runtime CodexRuntimeMetadata) (canonicalTranscriptCapture, error) {
	client, err := dialCodexAppServerClient(ctx, runtime.Endpoint)
	if err != nil {
		return canonicalTranscriptCapture{}, fmt.Errorf("dial codex transcript app-server: %w", err)
	}
	defer func() { _ = client.Close("transcript capture complete") }()
	thread, err := client.ReadThreadTranscript(ctx, runtime.ThreadID)
	if err != nil {
		return canonicalTranscriptCapture{}, fmt.Errorf("read codex transcript thread: %w", err)
	}
	events := codexThreadTranscriptEvents(thread, time.Now().UTC())
	capture, err := marshalCanonicalTranscriptWithSourceState(
		events,
		transcriptSourceTruncation{
			truncated:   thread.TranscriptTruncated,
			limitBytes:  thread.TranscriptSourceLimitBytes,
			limitEvents: thread.TranscriptSourceLimitEvents,
			limitPages:  thread.TranscriptSourceLimitPages,
			cause:       thread.TranscriptTruncationCause,
		},
	)
	if err != nil {
		return canonicalTranscriptCapture{}, fmt.Errorf("marshal codex transcript: %w", err)
	}
	return capture, nil
}

func uploadCodexTranscriptArtifact(
	ctx context.Context,
	cfg CodexLeadRuntimeConfig,
	target codexTranscriptCaptureTarget,
	capture canonicalTranscriptCapture,
) (*domain.Artifact, error) {
	metadata := transcriptArtifactMetadata(map[string]string{
		"runtime": "interactive-codex",
		"backend": RuntimeProviderCodex,
	}, capture)
	finalized, err := store.UploadContentArtifact(ctx, cfg.Store.Artifacts(), store.ArtifactCreate{
		WorkspaceKey:  target.workspace,
		ArtifactID:    "transcript-" + target.sessionID,
		AgentID:       target.agentID,
		SessionID:     target.sessionID,
		TaskID:        target.taskID,
		OwnerType:     "session",
		OwnerID:       target.sessionID,
		Type:          "transcript",
		Summary:       "interactive Codex session transcript",
		MIMEType:      "application/x-ndjson",
		DurableStatus: "declared",
		Metadata:      metadata,
	}, capture.content)
	if err != nil {
		return nil, fmt.Errorf("upload codex transcript artifact: %w", err)
	}
	return finalized, nil
}

func persistCodexTranscriptRef(
	ctx context.Context,
	cfg CodexLeadRuntimeConfig,
	target codexTranscriptCaptureTarget,
	artifactID string,
) error {
	if cfg.Runtime == nil {
		return ErrSessionRuntimeUnavailable
	}
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return errors.New("persist codex transcript ref: artifact ID is required")
	}
	if err := cfg.Runtime.PatchSessionRuntimeContext(ctx, interaction.PatchSessionCommand{
		WorkspaceKey:         target.workspace,
		SessionID:            target.sessionID,
		MetadataUpserts:      map[string]string{MetadataCodexThreadID: target.runtime.ThreadID},
		TranscriptArtifactID: &artifactID,
	}); err != nil {
		return fmt.Errorf("persist codex transcript ref: %w", err)
	}
	return nil
}

func codexThreadTranscriptEvents(thread *CodexThread, capturedAt time.Time) []transcript.Event {
	if thread == nil {
		return []transcript.Event{}
	}
	if capturedAt.IsZero() {
		capturedAt = time.Now().UTC()
	} else {
		capturedAt = capturedAt.UTC()
	}
	events := make([]transcript.Event, 0)
	for _, turn := range thread.Turns {
		for _, item := range turn.Items {
			text := strings.TrimSpace(item.PlainText())
			if text == "" {
				continue
			}
			role := ""
			switch item.Type {
			case "userMessage":
				role = transcript.RoleUser
			case "agentMessage":
				role = transcript.RoleAssistant
			default:
				continue
			}
			events = append(events, transcript.Event{
				Seq:       len(events) + 1,
				Timestamp: capturedAt,
				Role:      role,
				Type:      transcript.EventText,
				Text:      text,
				UUID:      strings.TrimSpace(item.ID),
			})
		}
	}
	return events
}

func marshalCanonicalTranscript(events []transcript.Event) (canonicalTranscriptCapture, error) {
	return marshalCanonicalTranscriptWithinLimitAndSourceState(
		events,
		maxCanonicalTranscriptBytes,
		transcriptSourceTruncation{},
	)
}

func marshalCanonicalTranscriptWithSourceTruncation(
	events []transcript.Event,
	sourceTruncated bool,
) (canonicalTranscriptCapture, error) {
	return marshalCanonicalTranscriptWithinLimitAndSourceState(
		events,
		maxCanonicalTranscriptBytes,
		transcriptSourceTruncation{
			truncated:  sourceTruncated,
			limitBytes: codexTranscriptSourceTextLimit,
			cause:      transcriptSourceCauseCodexText,
		},
	)
}

func marshalCanonicalTranscriptWithSourceState(
	events []transcript.Event,
	source transcriptSourceTruncation,
) (canonicalTranscriptCapture, error) {
	return marshalCanonicalTranscriptWithinLimitAndSourceState(
		events,
		maxCanonicalTranscriptBytes,
		source,
	)
}

func marshalCanonicalTranscriptWithinLimit(events []transcript.Event, maxBytes int) (canonicalTranscriptCapture, error) {
	return marshalCanonicalTranscriptWithinLimitAndSourceState(
		events,
		maxBytes,
		transcriptSourceTruncation{},
	)
}

func marshalCanonicalTranscriptWithinLimitAndSourceState(
	events []transcript.Event,
	maxBytes int,
	source transcriptSourceTruncation,
) (canonicalTranscriptCapture, error) {
	return marshalCanonicalTranscriptWithinLimitsAndSourceState(
		events,
		maxBytes,
		transcript.MaxCanonicalEvents,
		source,
	)
}

func marshalCanonicalTranscriptWithinLimitsAndState(
	events []transcript.Event,
	maxBytes, maxEvents int,
	sourceTruncated bool,
) (canonicalTranscriptCapture, error) {
	source := transcriptSourceTruncation{}
	if sourceTruncated {
		source = transcriptSourceTruncation{
			truncated:  true,
			limitBytes: codexTranscriptSourceTextLimit,
			cause:      transcriptSourceCauseCodexText,
		}
	}
	return marshalCanonicalTranscriptWithinLimitsAndSourceState(events, maxBytes, maxEvents, source)
}

func marshalCanonicalTranscriptWithinLimitsAndSourceState(
	events []transcript.Event,
	maxBytes, maxEvents int,
	source transcriptSourceTruncation,
) (canonicalTranscriptCapture, error) {
	if err := validateCanonicalTranscriptLimits(maxBytes, maxEvents); err != nil {
		return canonicalTranscriptCapture{}, err
	}
	capturedAt := time.Now().UTC().Truncate(time.Second)
	truncationMarker := func(seq int) transcript.Event {
		return canonicalTranscriptTruncationMarker(seq, capturedAt, maxBytes, source)
	}
	if err := validateCanonicalTranscriptMarkerFits(events, maxBytes, truncationMarker); err != nil {
		return canonicalTranscriptCapture{}, err
	}

	content, eventEnds, canonicalTruncated, err := marshalCanonicalEventsWithinLimits(
		events,
		maxBytes,
		maxEvents,
	)
	if err != nil {
		return canonicalTranscriptCapture{}, err
	}
	if !source.truncated && !canonicalTruncated {
		return canonicalTranscriptCapture{
			content:    content,
			limitBytes: maxBytes,
		}, nil
	}

	content, err = appendCanonicalTranscriptMarker(
		content,
		eventEnds,
		maxBytes,
		maxEvents,
		truncationMarker,
	)
	if err != nil {
		return canonicalTranscriptCapture{}, err
	}
	return canonicalTranscriptCapture{
		content:           content,
		truncated:         true,
		truncationReason:  canonicalTranscriptTruncationReason(source.truncated, canonicalTruncated),
		limitBytes:        maxBytes,
		sourceLimitBytes:  source.limitBytes,
		sourceLimitEvents: source.limitEvents,
		sourceLimitPages:  source.limitPages,
		sourceCause:       source.cause,
	}, nil
}

func validateCanonicalTranscriptLimits(maxBytes, maxEvents int) error {
	if maxBytes <= 0 {
		return errors.New("canonical transcript capture limit must be positive")
	}
	if maxEvents <= 0 {
		return errors.New("canonical transcript event limit must be positive")
	}
	return nil
}

func canonicalTranscriptTruncationMarker(
	seq int,
	capturedAt time.Time,
	maxBytes int,
	source transcriptSourceTruncation,
) transcript.Event {
	return transcript.Event{
		Seq:       seq,
		Timestamp: capturedAt,
		Role:      transcript.RoleSystem,
		Type:      transcript.EventSessionMeta,
		Text: fmt.Sprintf(
			"Transcript truncated by Loom because source history or canonical output exceeded Loom's bounded capture limits (%s).",
			canonicalTranscriptLimitDetail(maxBytes, source),
		),
	}
}

func canonicalTranscriptLimitDetail(maxBytes int, source transcriptSourceTruncation) string {
	if !source.truncated {
		return fmt.Sprintf("canonical limit: %d bytes", maxBytes)
	}
	return fmt.Sprintf(
		"source capture limit: %s (%s); canonical limit: %d bytes",
		transcriptSourceLimitDetail(source),
		source.cause,
		maxBytes,
	)
}

func transcriptSourceLimitDetail(source transcriptSourceTruncation) string {
	switch {
	case source.limitBytes > 0:
		return fmt.Sprintf("%d bytes", source.limitBytes)
	case source.limitEvents > 0:
		return fmt.Sprintf("%d events", source.limitEvents)
	case source.limitPages > 0:
		return fmt.Sprintf("%d pages", source.limitPages)
	default:
		return "bounded source capture"
	}
}

func validateCanonicalTranscriptMarkerFits(
	events []transcript.Event,
	maxBytes int,
	truncationMarker func(int) transcript.Event,
) error {
	// Validate that even the largest possible marker sequence fits before
	// serializing any native events.
	largestMarker, err := json.Marshal(truncationMarker(len(events) + 1))
	if err != nil {
		return err
	}
	if len(largestMarker)+1 > maxBytes {
		return fmt.Errorf(
			"canonical transcript capture limit %d cannot fit truncation evidence",
			maxBytes,
		)
	}
	return nil
}

func marshalCanonicalEventsWithinLimits(
	events []transcript.Event,
	maxBytes, maxEvents int,
) ([]byte, []int, bool, error) {
	content := make([]byte, 0, min(maxBytes, 64<<10))
	eventEnds := make([]int, 0, min(len(events), maxEvents))
	for _, event := range events {
		if len(eventEnds) >= maxEvents {
			return content, eventEnds, true, nil
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			return nil, nil, false, err
		}
		encoded = append(encoded, '\n')
		if len(content)+len(encoded) > maxBytes {
			return content, eventEnds, true, nil
		}
		content = append(content, encoded...)
		eventEnds = append(eventEnds, len(content))
	}
	return content, eventEnds, false, nil
}

func appendCanonicalTranscriptMarker(
	content []byte,
	eventEnds []int,
	maxBytes, maxEvents int,
	truncationMarker func(int) transcript.Event,
) ([]byte, error) {
	marker, err := marshalCanonicalTranscriptMarker(truncationMarker, len(eventEnds)+1)
	if err != nil {
		return nil, err
	}
	for !canonicalTranscriptMarkerFits(content, eventEnds, marker, maxBytes, maxEvents) {
		eventEnds = eventEnds[:len(eventEnds)-1]
		content = truncateCanonicalTranscriptContent(content, eventEnds)
		marker, err = marshalCanonicalTranscriptMarker(truncationMarker, len(eventEnds)+1)
		if err != nil {
			return nil, err
		}
	}
	return append(content, marker...), nil
}

func marshalCanonicalTranscriptMarker(
	truncationMarker func(int) transcript.Event,
	seq int,
) ([]byte, error) {
	marker, err := json.Marshal(truncationMarker(seq))
	if err != nil {
		return nil, err
	}
	return append(marker, '\n'), nil
}

func canonicalTranscriptMarkerFits(
	content []byte,
	eventEnds []int,
	marker []byte,
	maxBytes, maxEvents int,
) bool {
	return len(content)+len(marker) <= maxBytes && len(eventEnds)+1 <= maxEvents
}

func truncateCanonicalTranscriptContent(content []byte, eventEnds []int) []byte {
	if len(eventEnds) == 0 {
		return content[:0]
	}
	return content[:eventEnds[len(eventEnds)-1]]
}

func canonicalTranscriptTruncationReason(sourceTruncated, canonicalTruncated bool) string {
	switch {
	case sourceTruncated && canonicalTruncated:
		return transcriptTruncationBoth
	case canonicalTruncated:
		return transcriptTruncationCanonical
	default:
		return transcriptTruncationSource
	}
}

func transcriptArtifactMetadata(
	metadata map[string]string,
	capture canonicalTranscriptCapture,
) map[string]string {
	if capture.truncated {
		metadata["transcript_truncated"] = "true"
		metadata["transcript_capture_limit_bytes"] = strconv.Itoa(capture.limitBytes)
		metadata["transcript_truncation_reason"] = capture.truncationReason
		if capture.truncationReason == transcriptTruncationSource ||
			capture.truncationReason == transcriptTruncationBoth {
			if capture.sourceLimitBytes > 0 {
				metadata["transcript_source_limit_bytes"] = strconv.Itoa(capture.sourceLimitBytes)
			}
			if capture.sourceLimitEvents > 0 {
				metadata["transcript_source_limit_events"] = strconv.Itoa(capture.sourceLimitEvents)
			}
			if capture.sourceLimitPages > 0 {
				metadata["transcript_source_limit_pages"] = strconv.Itoa(capture.sourceLimitPages)
			}
			if capture.sourceCause != "" {
				metadata["transcript_source_truncation_cause"] = capture.sourceCause
			}
		}
	}
	return metadata
}
