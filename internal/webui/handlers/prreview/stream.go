package prreview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

const (
	reviewerStreamPollInterval      = time.Second
	reviewerStreamHeartbeatInterval = 15 * time.Second
	// reviewerPollTimeout bounds one provider-neutral Interaction read so a
	// stalled provider cannot wedge the SSE heartbeat or initial response.
	reviewerPollTimeout = 10 * time.Second
)

type reviewerStreamSession struct {
	ws        string
	agentName string
	request   *http.Request
}

type reviewerStreamStatus struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

type reviewerStreamMessage = interaction.ConversationMessage

// reviewerSnapshot is the provider-neutral conversation snapshot both the SSE
// stream and the poll endpoint serve.
type reviewerSnapshot struct {
	state    string
	detail   string
	messages []reviewerStreamMessage
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
	ws := canonicalWorkspaceFromRequest(r)
	if ws == "" {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "canonical workspace is required", false)
		return reviewerStreamSession{}, false
	}
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
	if !m.requireReviewerIdentity(w, r.Context(), ws, agentName) {
		return reviewerStreamSession{}, false
	}
	if m == nil || m.interactionChat == nil || m.interactionAuthority == nil {
		writeReviewerConversationError(w, interaction.ErrUnavailable)
		return reviewerStreamSession{}, false
	}
	if _, err := m.resolveReviewerConversationAuthority(r, ws); err != nil {
		writeReviewerConversationError(w, err)
		return reviewerStreamSession{}, false
	}
	return reviewerStreamSession{
		ws:        ws,
		agentName: agentName,
		request:   r,
	}, true
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

// readReviewerSnapshot refreshes request-bound operator authority for every
// poll, then delegates provider/session metadata, transcript reads, prompt
// trimming, and redaction to Interaction. The refresh matters for SSE streams:
// operator authorities are deliberately short-lived.
func (m *Module) readReviewerSnapshot(
	ctx context.Context,
	session reviewerStreamSession,
) (reviewerSnapshot, error) {
	if m == nil || m.interactionChat == nil ||
		m.interactionAuthority == nil || session.request == nil {
		return reviewerSnapshot{}, interaction.ErrUnavailable
	}
	pollCtx, cancel := context.WithTimeout(ctx, reviewerPollTimeout)
	defer cancel()
	request := session.request.Clone(pollCtx)
	auth, err := m.resolveReviewerConversationAuthority(request, session.ws)
	if err != nil {
		return reviewerSnapshot{}, err
	}
	conversation, err := m.interactionChat.ReadConversation(
		pollCtx,
		auth,
		interaction.ConversationQuery{
			WorkspaceKey: session.ws,
			AgentID:      session.agentName,
		},
	)
	if err != nil {
		return reviewerSnapshot{}, err
	}
	if conversation == nil {
		return reviewerSnapshot{}, interaction.ErrInvalidPersistedState
	}
	return reviewerSnapshot{
		state:    string(conversation.State),
		detail:   conversation.Detail,
		messages: append([]reviewerStreamMessage(nil), conversation.Messages...),
	}, nil
}

func (m *Module) resolveReviewerConversationAuthority(
	request *http.Request,
	workspace string,
) (authority.OperatorAuthority, error) {
	if m == nil || m.interactionAuthority == nil {
		return authority.OperatorAuthority{}, interaction.ErrUnavailable
	}
	return m.interactionAuthority.ResolveOperatorAuthority(
		request,
		workspace,
		interaction.ActionReadConversation,
	)
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
		writeReviewerConversationError(w, err)
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

//nolint:funlen // Keep the exhaustive authority and Interaction error-to-HTTP classification in one auditable response matrix.
func writeReviewerConversationError(w http.ResponseWriter, err error) {
	var admissionErr *authority.AdmissionError
	if errors.As(err, &admissionErr) {
		writeReviewerAdmissionError(w, admissionErr)
		return
	}
	switch {
	case errors.Is(err, workflowcataloghttp.ErrUnauthenticated),
		errors.Is(err, authority.ErrInvalidPrincipal),
		errors.Is(err, authority.ErrPrincipalExpired),
		errors.Is(err, authority.ErrOpaqueAuthority):
		writePRReviewErrorCode(
			w,
			http.StatusUnauthorized,
			"unauthenticated",
			"operator authentication required",
			false,
		)
	case errors.Is(err, authority.ErrAdmissionDenied),
		errors.Is(err, authority.ErrWorkspaceMismatch),
		errors.Is(err, authority.ErrPrincipalClass),
		errors.Is(err, authority.ErrActionNotAllowed):
		writePRReviewErrorCode(
			w,
			http.StatusForbidden,
			"forbidden",
			"operator is not allowed to read this workspace",
			false,
		)
	case errors.Is(err, interaction.ErrUnavailable):
		writePRReviewErrorCode(
			w,
			http.StatusServiceUnavailable,
			"interaction_unavailable",
			"reviewer conversation is unavailable",
			true,
		)
	case errors.Is(err, context.DeadlineExceeded):
		writePRReviewErrorCode(
			w,
			http.StatusGatewayTimeout,
			"timeout",
			"reviewer conversation timed out",
			true,
		)
	case errors.Is(err, context.Canceled):
		writePRReviewErrorCode(
			w,
			499,
			"canceled",
			"reviewer conversation was canceled",
			true,
		)
	default:
		writePRReviewErrorCode(
			w,
			http.StatusBadGateway,
			"upstream_error",
			"failed to resolve reviewer conversation",
			true,
		)
	}
}

func writeReviewerAdmissionError(
	w http.ResponseWriter,
	admissionErr *authority.AdmissionError,
) {
	switch admissionErr.Reason {
	case authority.DenialInvalidAuthority, authority.DenialExpired:
		writePRReviewErrorCode(
			w,
			http.StatusUnauthorized,
			"unauthenticated",
			"operator authentication required",
			false,
		)
	default:
		writePRReviewErrorCode(
			w,
			http.StatusForbidden,
			"forbidden",
			"operator is not allowed to read this workspace",
			false,
		)
	}
}
