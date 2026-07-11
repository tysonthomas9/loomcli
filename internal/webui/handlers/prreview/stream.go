package prreview

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/sessions/redact"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

const (
	reviewerStreamPollInterval      = time.Second
	reviewerStreamHeartbeatInterval = 15 * time.Second
	// reviewerPollTimeout bounds a single dial+read so a hung codex can't wedge
	// the SSE loop (blocking the heartbeat) or stall the first byte.
	reviewerPollTimeout = 10 * time.Second
)

// readReviewerThread dials the codex app-server, reads the thread WITH turns,
// and always closes the connection — under a per-call timeout. Shared by the
// SSE stream and the snapshot handler so both bound their codex calls.
func (m *Module) readReviewerThread(ctx context.Context, rt leadcontrol.CodexRuntimeMetadata) (*leadcontrol.CodexThread, error) {
	pollCtx, cancel := context.WithTimeout(ctx, reviewerPollTimeout)
	defer cancel()
	client, err := m.dialCodex(pollCtx, rt.Endpoint)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close("poll") }()
	return client.ReadThreadWithTurns(pollCtx, rt.ThreadID)
}

type reviewerStreamSession struct {
	ws        string
	agentName string
}

type reviewerStreamStatus struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

type reviewerStreamMessage struct {
	TurnID string `json:"turn_id"`
	ItemID string `json:"item_id"`
	Role   string `json:"role"`
	Text   string `json:"text"`
	Phase  string `json:"phase,omitempty"`
}

func (m *Module) streamReviewer(w http.ResponseWriter, r *http.Request) {
	session, ok := m.prepareReviewerStream(w, r)
	if !ok {
		return
	}
	sw, err := realtime.NewWriter(w)
	if err != nil {
		writePRReviewErrorCode(w, http.StatusInternalServerError, "internal", "streaming unsupported", false)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

	m.runReviewerStream(r.Context(), sw, session)
}

func (m *Module) prepareReviewerStream(w http.ResponseWriter, r *http.Request) (reviewerStreamSession, bool) {
	ws := r.PathValue("ws")
	params, ok := parsePullRequestPath(r.PathValue("owner"), r.PathValue("repo"), r.PathValue("number"))
	if !ok {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "invalid pull request path", false)
		return reviewerStreamSession{}, false
	}
	_, canonRepo, ok := m.authorizeRepo(w, r, ws, params.owner, params.repo)
	if !ok {
		return reviewerStreamSession{}, false
	}
	agentName := reviewerAgentName(canonRepo, params.number)
	if _, err := m.store.Agents().Get(r.Context(), ws, agentName); err != nil {
		writePRReviewErrorCode(w, http.StatusNotFound, "reviewer_not_started", "reviewer has not been started for this pull request", false)
		return reviewerStreamSession{}, false
	}
	return reviewerStreamSession{ws: ws, agentName: agentName}, true
}

func (m *Module) runReviewerStream(ctx context.Context, sw *realtime.Writer, session reviewerStreamSession) {
	seen := make(map[string]struct{})
	lastStatus := ""
	if !m.pollReviewerStream(ctx, sw, session, seen, &lastStatus) {
		return
	}
	pollInterval := m.streamPollInterval
	if pollInterval <= 0 {
		pollInterval = reviewerStreamPollInterval
	}
	heartbeatInterval := m.streamHeartbeatInterval
	if heartbeatInterval <= 0 {
		heartbeatInterval = reviewerStreamHeartbeatInterval
	}
	poll := time.NewTicker(pollInterval)
	defer poll.Stop()
	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			if !m.pollReviewerStream(ctx, sw, session, seen, &lastStatus) {
				return
			}
		case <-heartbeat.C:
			if sw.WriteComment("hb") != nil {
				return
			}
		}
	}
}

// readReviewerSnapshot resolves the reviewer's orchestration session and
// dispatches on its runtime provider: codex conversations are read live over
// the app-server socket; harness backends (claude, gemini) are read from the
// harness's own transcript on disk; backends with no readable conversation
// report "unsupported". Message text is redacted before it leaves this
// function — every serving path (snapshot and SSE) goes through here.
func (m *Module) readReviewerSnapshot(ctx context.Context, session reviewerStreamSession) (reviewerSnapshot, error) {
	sess, err := store.OrchestrationSessionFor(ctx, m.store, session.ws, session.agentName)
	if err != nil {
		return reviewerSnapshot{}, err
	}
	provider := ""
	if sess != nil {
		provider = strings.ToLower(strings.TrimSpace(sess.Metadata[leadcontrol.MetadataRuntimeProvider]))
	}
	var snap reviewerSnapshot
	switch provider {
	case "", leadcontrol.RuntimeProviderCodex:
		snap = m.readCodexReviewerSnapshot(ctx, sess)
	default:
		snap = m.readHarnessReviewerSnapshot(session, sess, provider)
	}
	for i := range snap.messages {
		snap.messages[i].Text = redact.String(snap.messages[i].Text)
	}
	return snap, nil
}

func (m *Module) readCodexReviewerSnapshot(ctx context.Context, sess *domain.AgentSession) reviewerSnapshot {
	rt := leadcontrol.RuntimeMetadataFromSession(sess)
	if sess == nil || rt.Endpoint == "" || rt.ThreadID == "" {
		// Reviewer's codex hasn't booted yet (terminal not attached / thread
		// not discovered) — an empty conversation in the "starting" state.
		return reviewerSnapshot{state: "starting"}
	}
	thread, err := m.readReviewerThread(ctx, rt)
	if err != nil {
		return reviewerSnapshot{state: "reconnecting"}
	}
	return reviewerSnapshot{
		state:    reviewerThreadState(thread),
		messages: flattenReviewerMessages(thread),
	}
}

func (m *Module) pollReviewerStream(ctx context.Context, sw *realtime.Writer, session reviewerStreamSession, seen map[string]struct{}, lastStatus *string) bool {
	snap, err := m.readReviewerSnapshot(ctx, session)
	if err != nil {
		return false
	}
	if !writeReviewerStatus(sw, lastStatus, snap) {
		return false
	}
	for _, msg := range snap.messages {
		cursor := msg.TurnID + "/" + msg.ItemID
		if _, ok := seen[cursor]; ok {
			continue
		}
		data, err := json.Marshal(msg)
		if err != nil {
			return false
		}
		if sw.WriteEventID(msg.ItemID, "message", string(data)) != nil {
			return false
		}
		seen[cursor] = struct{}{}
	}
	return true
}

type reviewerConversation struct {
	State    string                  `json:"state"`
	Detail   string                  `json:"detail,omitempty"`
	Messages []reviewerStreamMessage `json:"messages"`
}

// getReviewerConversation is the POLL target: a single snapshot of the whole
// reviewer conversation. The frontend polls this (the SSE stream above only
// works over loopback — EventSource can't send the auth Bearer header, so under
// auth the UI must poll).
func (m *Module) getReviewerConversation(w http.ResponseWriter, r *http.Request) {
	session, ok := m.prepareReviewerStream(w, r)
	if !ok {
		return
	}
	snap, err := m.readReviewerSnapshot(r.Context(), session)
	if err != nil {
		writePRReviewErrorCode(w, http.StatusBadGateway, "upstream_error", "failed to resolve reviewer session", true)
		return
	}
	msgs := snap.messages
	if msgs == nil {
		msgs = []reviewerStreamMessage{}
	}
	writeJSON(w, reviewerConversation{State: snap.state, Detail: snap.detail, Messages: msgs})
}

func writeReviewerStatus(sw *realtime.Writer, lastStatus *string, snap reviewerSnapshot) bool {
	if lastStatus != nil && *lastStatus == snap.state {
		return true
	}
	data, err := json.Marshal(reviewerStreamStatus{State: snap.state, Detail: snap.detail})
	if err != nil {
		return false
	}
	if sw.WriteEventID(snap.state, "status", string(data)) != nil {
		return false
	}
	if lastStatus != nil {
		*lastStatus = snap.state
	}
	return true
}

func reviewerThreadState(thread *leadcontrol.CodexThread) string {
	if thread != nil && thread.Status.CanStartTurn() {
		return "idle"
	}
	return "running"
}

func flattenReviewerMessages(thread *leadcontrol.CodexThread) []reviewerStreamMessage {
	if thread == nil {
		return nil
	}
	var out []reviewerStreamMessage
	for _, turn := range thread.Turns {
		for _, item := range turn.Items {
			msg := reviewerMessageFromItem(turn.ID, item)
			if msg == nil {
				continue
			}
			out = append(out, *msg)
		}
	}
	return trimReviewerPreamble(out)
}

// reviewerPromptMarker is the first line of the reviewer prompt
// (prompts/pr-review.md). Codex is launched with that prompt as its positional
// argument, i.e. its FIRST turn, so it comes back as the leading `user` message.
// It's a heading no human types, so trimming it can't hide a real user message.
// Kept in sync with pr-review.md's first line.
const reviewerPromptMarker = "## READ-ONLY PR REVIEWER"

// trimReviewerPreamble drops the leading prompt bubble so the chat opens on the
// actual review. It only skips leading `user` messages matching the prompt
// marker and stops at the first non-match (in particular the first assistant
// reply), so a real user message is never hidden.
func trimReviewerPreamble(msgs []reviewerStreamMessage) []reviewerStreamMessage {
	i := 0
	for i < len(msgs) {
		m := msgs[i]
		if m.Role == "user" && strings.HasPrefix(strings.TrimSpace(m.Text), reviewerPromptMarker) {
			i++
			continue
		}
		break
	}
	return msgs[i:]
}

func reviewerMessageFromItem(turnID string, item leadcontrol.CodexTurnItem) *reviewerStreamMessage {
	msg := reviewerStreamMessage{
		TurnID: turnID,
		ItemID: item.ID,
		Text:   item.PlainText(),
		Phase:  item.Phase,
	}
	switch item.Type {
	case "userMessage":
		msg.Role = "user"
	case "agentMessage":
		msg.Role = "assistant"
	default:
		return nil
	}
	// Skip empty bubbles (e.g. a non-final-phase agent item with no text yet).
	if strings.TrimSpace(msg.Text) == "" {
		return nil
	}
	return &msg
}
