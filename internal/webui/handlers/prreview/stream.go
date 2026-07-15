package prreview

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/sessions/redact"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// reviewerPollTimeout bounds a single live codex read so a hung app-server
// can't stall the conversation snapshot handler.
const reviewerPollTimeout = 10 * time.Second

func (m *Module) reviewerReaderEntry(endpoint string) *reviewerReaderPoolEntry {
	m.reviewerReadersMu.Lock()
	defer m.reviewerReadersMu.Unlock()
	if m.reviewerReaders == nil {
		m.reviewerReaders = make(map[string]*reviewerReaderPoolEntry)
	}
	if entry := m.reviewerReaders[endpoint]; entry != nil {
		return entry
	}
	entry := &reviewerReaderPoolEntry{}
	m.reviewerReaders[endpoint] = entry
	return entry
}

// dropReviewerReaderEntry is called while entry.mu is held.
func (m *Module) dropReviewerReaderEntry(endpoint string, entry *reviewerReaderPoolEntry, reason string) {
	client := entry.client
	entry.client = nil
	entry.dropped = true

	m.reviewerReadersMu.Lock()
	if m.reviewerReaders != nil && m.reviewerReaders[endpoint] == entry {
		delete(m.reviewerReaders, endpoint)
	}
	m.reviewerReadersMu.Unlock()

	if client != nil {
		_ = client.Close(reason)
	}
}

// readReviewerThread reuses one codex app-server connection per endpoint.
// CodexClient has no response demux or internal call lock, so every use of a
// pooled client is serialized by the endpoint entry mutex.
func (m *Module) readReviewerThread(ctx context.Context, rt leadcontrol.CodexRuntimeMetadata) (*leadcontrol.CodexThread, error) {
	endpoint := strings.TrimSpace(rt.Endpoint)
	for {
		entry := m.reviewerReaderEntry(endpoint)
		entry.mu.Lock()
		if entry.dropped {
			entry.mu.Unlock()
			continue
		}
		thread, err := m.readReviewerThreadLocked(ctx, endpoint, strings.TrimSpace(rt.ThreadID), entry)
		entry.mu.Unlock()
		return thread, err
	}
}

func (m *Module) readReviewerThreadLocked(ctx context.Context, endpoint, threadID string, entry *reviewerReaderPoolEntry) (*leadcontrol.CodexThread, error) {
	if entry.client == nil {
		if err := ctx.Err(); err != nil {
			m.dropReviewerReaderEntry(endpoint, entry, "poll canceled")
			return nil, err
		}
		dialCtx, cancelDial := context.WithTimeout(context.WithoutCancel(ctx), reviewerPollTimeout)
		client, err := m.dialCodex(dialCtx, endpoint)
		cancelDial()
		if err != nil {
			m.dropReviewerReaderEntry(endpoint, entry, "dial failed")
			return nil, err
		}
		entry.client = client
	}

	pollCtx, cancelPoll := context.WithTimeout(ctx, reviewerPollTimeout)
	defer cancelPoll()
	if err := pollCtx.Err(); err != nil {
		return nil, err
	}
	thread, err := entry.client.ReadThreadWithTurns(pollCtx, threadID)
	if err != nil {
		m.dropReviewerReaderEntry(endpoint, entry, "poll error")
		return nil, err
	}
	return thread, nil
}

type reviewerStreamSession struct {
	ws        string
	agentName string
}

type reviewerStreamMessage struct {
	TurnID string `json:"turn_id"`
	ItemID string `json:"item_id"`
	Role   string `json:"role"`
	Text   string `json:"text"`
	Phase  string `json:"phase,omitempty"`
}

func (m *Module) prepareReviewerStream(w http.ResponseWriter, r *http.Request) (reviewerStreamSession, bool) {
	ws := r.PathValue("ws")
	params, ok := parsePullRequestPath(r.PathValue("owner"), r.PathValue("repo"), r.PathValue("number"))
	if !ok {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "invalid pull request path", false)
		return reviewerStreamSession{}, false
	}
	canonOwner, canonRepo, ok := m.authorizeRepo(w, r, ws, params.owner, params.repo)
	if !ok {
		return reviewerStreamSession{}, false
	}
	agentName := reviewerAgentName(canonOwner, canonRepo, params.number)
	if _, err := m.store.Agents().Get(r.Context(), ws, agentName); err != nil {
		writePRReviewErrorCode(w, http.StatusNotFound, "reviewer_not_started", "reviewer has not been started for this pull request", false)
		return reviewerStreamSession{}, false
	}
	return reviewerStreamSession{ws: ws, agentName: agentName}, true
}

// readReviewerSnapshot resolves the reviewer's orchestration session and
// dispatches on its runtime provider: codex conversations are read live over
// the app-server socket; harness backends (claude, gemini) are read from the
// harness's own transcript on disk; backends with no readable conversation
// report "unsupported". Message text is redacted before it leaves this
// function — the snapshot handler goes through here.
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

type reviewerConversation struct {
	State    string                  `json:"state"`
	Detail   string                  `json:"detail,omitempty"`
	Messages []reviewerStreamMessage `json:"messages"`
	// Cursor is the opaque identity of the LAST message in the underlying list
	// (empty when the list is empty). Reset tells the client whether Messages
	// is a full snapshot to replace (true) or an incremental tail to append
	// (false). See buildReviewerConversation for the cursor contract.
	Cursor string `json:"cursor"`
	Reset  bool   `json:"reset"`
}

// getReviewerConversation is the POLL target: a snapshot of the reviewer
// conversation. Clients may pass an opaque `after` cursor (the identity of the
// last message they already hold) to receive only newer messages; omitting it
// returns the full snapshot. The frontend polls this because EventSource can't
// send the auth Bearer header, so under auth the UI can't open a raw SSE stream.
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
	after := strings.TrimSpace(r.URL.Query().Get("after"))
	writeJSON(w, buildReviewerConversation(snap, after))
}

// reviewerMessageCursor is the opaque per-message cursor identity — the same
// turnID/itemID pair the old SSE stream deduped on.
func reviewerMessageCursor(msg reviewerStreamMessage) string {
	return msg.TurnID + "/" + msg.ItemID
}

// buildReviewerConversation applies the incremental-cursor contract to a
// snapshot. `after` is the cursor of the last message the client already holds
// (empty on a first poll):
//   - empty `after` → full snapshot, reset=true.
//   - `after` matches a message → only the messages after it, reset=false
//     (an empty tail means "no new messages").
//   - `after` matches nothing (session rotated, IDs reconstructed, truncation)
//     → the full list, reset=true; the client must replace, not append.
func buildReviewerConversation(snap reviewerSnapshot, after string) reviewerConversation {
	resp := reviewerConversation{
		State:    snap.state,
		Detail:   snap.detail,
		Messages: []reviewerStreamMessage{},
	}
	// A reconnecting snapshot carries no messages by construction (the read
	// failed transiently). Don't signal a reset — that would blank the client;
	// echo its cursor back so it keeps its last-good conversation.
	if snap.state == "reconnecting" && len(snap.messages) == 0 {
		resp.Cursor = after
		return resp
	}
	if len(snap.messages) > 0 {
		resp.Cursor = reviewerMessageCursor(snap.messages[len(snap.messages)-1])
	}
	if after != "" {
		if idx := indexReviewerCursor(snap.messages, after); idx >= 0 {
			resp.Messages = append(resp.Messages, snap.messages[idx+1:]...)
			return resp
		}
	}
	resp.Messages = append(resp.Messages, snap.messages...)
	resp.Reset = true
	return resp
}

// indexReviewerCursor returns the position of the message whose cursor equals
// the given value, or -1 if no message matches.
func indexReviewerCursor(msgs []reviewerStreamMessage, cursor string) int {
	for i := range msgs {
		if reviewerMessageCursor(msgs[i]) == cursor {
			return i
		}
	}
	return -1
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
