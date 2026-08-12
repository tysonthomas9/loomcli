//nolint:staticcheck // The project already uses nhooyr/websocket; migration to coder/websocket is separate.
package leadcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"nhooyr.io/websocket" //nolint:staticcheck // existing project websocket dependency

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

// A transcript page contains one turn so ordinary accumulated text cannot make
// a valid page exceed the websocket frame bound before Loom can apply its
// aggregate source-text limiter. The 64 MiB read ceiling remains a hard bound
// for a single unusually large turn plus JSON framing.
const codexRPCReadLimit = 64 << 20
const codexTranscriptTurnsPageLimit = 1

// codexTranscriptSourceTextLimit bounds the retained user/assistant message
// text while paginating a Codex thread. The websocket already limits each
// response page to 64 MiB; this aggregate budget prevents a long-lived thread
// from being duplicated without bound before canonical transcript encoding.
// It also leaves headroom for JSON field overhead and escaping under the
// canonical transcript artifact's separate 63 MiB ceiling.
const codexTranscriptSourceTextLimit = 48 << 20

type CodexClient struct {
	conn *websocket.Conn
	next atomic.Int64
}

type codexAppServerClient interface {
	Close(reason string) error
	ListThreads(ctx context.Context, cwd string, limit int) ([]CodexThread, error)
	ReadThread(ctx context.Context, threadID string) (*CodexThread, error)
	ReadThreadWithTurns(ctx context.Context, threadID string) (*CodexThread, error)
	ReadThreadTranscript(ctx context.Context, threadID string) (*CodexThread, error)
	StartTurn(ctx context.Context, threadID, text string) error
}

var dialCodexAppServerClient = func(ctx context.Context, endpoint string) (codexAppServerClient, error) {
	return DialCodexAppServer(ctx, endpoint)
}

type CodexThread struct {
	ID          string            `json:"id"`
	Preview     string            `json:"preview"`
	Cwd         string            `json:"cwd"`
	CreatedAt   float64           `json:"createdAt"`
	CreatedAtMS float64           `json:"createdAtMs"`
	UpdatedAt   float64           `json:"updatedAt"`
	UpdatedAtMS float64           `json:"updatedAtMs"`
	Status      CodexThreadStatus `json:"status"`
	Turns       []CodexTurn       `json:"turns"`

	// TranscriptTruncated is local capture state, not part of Codex's JSON
	// protocol. The accompanying cause and typed limit preserve whether Loom
	// stopped on source text, canonical-event count, or scanned-page count.
	TranscriptTruncated         bool   `json:"-"`
	TranscriptTruncationCause   string `json:"-"`
	TranscriptSourceLimitBytes  int    `json:"-"`
	TranscriptSourceLimitEvents int    `json:"-"`
	TranscriptSourceLimitPages  int    `json:"-"`
}

type CodexThreadStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags,omitempty"`
}

type CodexTurn struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Items  []CodexTurnItem `json:"items"`
}

type CodexTurnItem struct {
	Type    string              `json:"type"`
	ID      string              `json:"id"`
	Text    string              `json:"text"`
	Content []CodexContentBlock `json:"content"`
	Phase   string              `json:"phase,omitempty"`

	// Tool-ish item fields (commandExecution, mcpToolCall, fileChange, …).
	// Unknown types keep unmarshaling; consumers ignore empty fields.
	Command          string            `json:"command,omitempty"`
	Cwd              string            `json:"cwd,omitempty"`
	Status           string            `json:"status,omitempty"`
	AggregatedOutput string            `json:"aggregatedOutput,omitempty"`
	Query            string            `json:"query,omitempty"`
	Server           string            `json:"server,omitempty"`
	Tool             string            `json:"tool,omitempty"`
	Arguments        json.RawMessage   `json:"arguments,omitempty"`
	Result           json.RawMessage   `json:"result,omitempty"`
	Error            json.RawMessage   `json:"error,omitempty"`
	Changes          []CodexFileChange `json:"changes,omitempty"`
}

type CodexFileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind,omitempty"`
	Diff string `json:"diff,omitempty"`
}

type CodexContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (it CodexTurnItem) PlainText() string {
	switch it.Type {
	case "agentMessage":
		return it.Text
	case "userMessage":
		var b strings.Builder
		for _, block := range it.Content {
			if block.Type != "text" {
				continue
			}
			b.WriteString(block.Text)
		}
		return b.String()
	default:
		return ""
	}
}

func (s CodexThreadStatus) RuntimeStatus() string {
	switch s.Type {
	case "idle":
		return RuntimeStatusIdle
	case "active":
		for _, flag := range s.ActiveFlags {
			switch flag {
			case "waitingOnApproval":
				return RuntimeStatusWaitingApproval
			case "waitingOnUserInput":
				return RuntimeStatusWaitingUserInput
			}
		}
		return RuntimeStatusActive
	case "systemError":
		return RuntimeStatusFailed
	case "notLoaded", "":
		return RuntimeStatusDisconnected
	default:
		return s.Type
	}
}

func (s CodexThreadStatus) CanStartTurn() bool {
	return s.Type == "idle"
}

type rpcRequest struct {
	ID     int64  `json:"id,omitempty"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type rpcResponse struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return ""
	}
	if len(e.Data) > 0 {
		return fmt.Sprintf("codex app-server rpc error %d: %s: %s", e.Code, e.Message, string(e.Data))
	}
	return fmt.Sprintf("codex app-server rpc error %d: %s", e.Code, e.Message)
}

func isRPCMethodNotFound(err error) bool {
	var rpcErr *rpcError
	return errors.As(err, &rpcErr) && rpcErr.Code == -32601
}

func DialCodexAppServer(ctx context.Context, endpoint string) (*CodexClient, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, errors.New("codex app-server endpoint required")
	}
	conn, _, err := websocket.Dial(ctx, endpoint, nil) //nolint:bodyclose // websocket response body is managed by nhooyr
	if err != nil {
		return nil, fmt.Errorf("dial codex app-server %s: %w", endpoint, err)
	}
	conn.SetReadLimit(codexRPCReadLimit)
	c := &CodexClient{conn: conn}
	if err := c.Initialize(ctx); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "initialize failed")
		return nil, err
	}
	return c, nil
}

func (c *CodexClient) Close(reason string) error {
	if c == nil || c.conn == nil {
		return nil
	}
	if strings.TrimSpace(reason) == "" {
		reason = "done"
	}
	return c.conn.Close(websocket.StatusNormalClosure, reason)
}

func (c *CodexClient) Initialize(ctx context.Context) error {
	params := map[string]any{
		"clientInfo": map[string]any{
			"name":    "loom",
			"title":   "Loom",
			"version": "dev",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}
	var result json.RawMessage
	if err := c.Call(ctx, "initialize", params, &result); err != nil {
		return err
	}
	return c.Notify(ctx, "initialized", nil)
}

func (c *CodexClient) ListThreads(ctx context.Context, cwd string, limit int) ([]CodexThread, error) {
	params := map[string]any{}
	if strings.TrimSpace(cwd) != "" {
		params["cwd"] = strings.TrimSpace(cwd)
	}
	if limit > 0 {
		params["limit"] = limit
	}
	var result struct {
		Data []CodexThread `json:"data"`
	}
	if err := c.Call(ctx, "thread/list", params, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

func (c *CodexClient) ReadThread(ctx context.Context, threadID string) (*CodexThread, error) {
	return c.readThreadSnapshot(ctx, threadID, false)
}

// ReadThreadWithTurns returns Codex's single-RPC thread snapshot. This is the
// caller-appropriate path for live UI polling: each poll asks app-server for
// its current snapshot once rather than replaying transcript pagination from
// cursor zero. Durable transcript capture uses ReadThreadTranscript instead.
func (c *CodexClient) ReadThreadWithTurns(ctx context.Context, threadID string) (*CodexThread, error) {
	return c.readThreadSnapshot(ctx, threadID, true)
}

func (c *CodexClient) readThreadSnapshot(
	ctx context.Context,
	threadID string,
	includeTurns bool,
) (*CodexThread, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, errors.New("codex thread id required")
	}
	var result struct {
		Thread CodexThread `json:"thread"`
	}
	if err := c.Call(ctx, "thread/read", map[string]any{
		"threadId":     threadID,
		"includeTurns": includeTurns,
	}, &result); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.Thread.ID) == "" {
		return nil, fmt.Errorf("codex thread/read returned no thread for %s", threadID)
	}
	return &result.Thread, nil
}

// ReadThreadTranscript captures the canonical user/assistant history through
// bounded pagination. Unlike the live snapshot path, it deliberately walks
// from cursor zero so the durable artifact contains the oldest retained prefix
// and explicit truncation provenance.
func (c *CodexClient) ReadThreadTranscript(ctx context.Context, threadID string) (*CodexThread, error) {
	return c.readThreadWithTurnsLimits(
		ctx,
		threadID,
		codexTranscriptSourceTextLimit,
		transcript.MaxCanonicalEvents,
	)
}

func (c *CodexClient) readThreadWithTurnsTextLimit(
	ctx context.Context,
	threadID string,
	maxTextBytes int,
) (*CodexThread, error) {
	return c.readThreadWithTurnsLimits(
		ctx,
		threadID,
		maxTextBytes,
		transcript.MaxCanonicalEvents,
	)
}

func (c *CodexClient) readThreadWithTurnsLimits(
	ctx context.Context,
	threadID string,
	maxTextBytes, maxEvents int,
) (*CodexThread, error) {
	threadID, err := validateCodexTranscriptReadLimits(threadID, maxTextBytes, maxEvents)
	if err != nil {
		return nil, err
	}
	// Subscribe/read metadata without hydrating the full rollout. A single
	// includeTurns response grows with the lifetime of the thread and can exceed
	// the websocket's bounded message limit, silently preventing transcript
	// capture for otherwise healthy long-running sessions.
	thread, err := c.ReadThread(ctx, threadID)
	if err != nil {
		return nil, err
	}

	state := codexTranscriptPaginationState{
		thread:       thread,
		turns:        make([]CodexTurn, 0),
		maxTextBytes: maxTextBytes,
		maxEvents:    maxEvents,
		seenCursors:  make(map[string]struct{}),
	}
	for {
		page, err := c.listCodexTranscriptTurnsPage(ctx, threadID, state.cursor)
		if err != nil {
			if state.scannedPages == 0 && isRPCMethodNotFound(err) {
				return c.readThreadSnapshotWithinTranscriptLimits(
					ctx,
					threadID,
					maxTextBytes,
					maxEvents,
				)
			}
			return nil, err
		}
		done, err := state.consume(page)
		if err != nil {
			return nil, err
		}
		if done {
			break
		}
	}
	thread.Turns = state.turns
	return thread, nil
}

func (c *CodexClient) readThreadSnapshotWithinTranscriptLimits(
	ctx context.Context,
	threadID string,
	maxTextBytes, maxEvents int,
) (*CodexThread, error) {
	thread, err := c.ReadThreadWithTurns(ctx, threadID)
	if err != nil {
		return nil, fmt.Errorf("read codex transcript compatibility snapshot: %w", err)
	}
	retained, _, _, truncationCause := retainCodexTranscriptTurns(
		thread.Turns,
		maxTextBytes,
		maxEvents,
	)
	thread.Turns = retained
	if truncationCause != "" {
		markCodexTranscriptTruncated(
			thread,
			truncationCause,
			maxTextBytes,
			maxEvents,
		)
	}
	return thread, nil
}

func validateCodexTranscriptReadLimits(threadID string, maxTextBytes, maxEvents int) (string, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", errors.New("codex thread id required")
	}
	if maxTextBytes <= 0 {
		return "", errors.New("codex transcript source-text limit must be positive")
	}
	if maxEvents <= 0 {
		return "", errors.New("codex transcript event limit must be positive")
	}
	return threadID, nil
}

type codexTranscriptTurnsPage struct {
	Data       []CodexTurn `json:"data"`
	NextCursor *string     `json:"nextCursor"`
}

func (c *CodexClient) listCodexTranscriptTurnsPage(
	ctx context.Context,
	threadID, cursor string,
) (codexTranscriptTurnsPage, error) {
	params := map[string]any{
		"threadId":      threadID,
		"limit":         codexTranscriptTurnsPageLimit,
		"sortDirection": "asc",
		// Summary omits large command/tool payloads while retaining the user
		// and agent messages needed by the canonical transcript.
		"itemsView": "summary",
	}
	if cursor != "" {
		params["cursor"] = cursor
	}
	var page codexTranscriptTurnsPage
	if err := c.Call(ctx, "thread/turns/list", params, &page); err != nil {
		return codexTranscriptTurnsPage{}, fmt.Errorf("list codex transcript turns: %w", err)
	}
	return page, nil
}

type codexTranscriptPaginationState struct {
	thread            *CodexThread
	turns             []CodexTurn
	retainedTextBytes int
	retainedEvents    int
	scannedPages      int
	maxTextBytes      int
	maxEvents         int
	cursor            string
	seenCursors       map[string]struct{}
}

func (s *codexTranscriptPaginationState) consume(page codexTranscriptTurnsPage) (bool, error) {
	s.scannedPages++
	retained, textBytes, eventCount, truncationCause := retainCodexTranscriptTurns(
		page.Data,
		s.maxTextBytes-s.retainedTextBytes,
		s.maxEvents-s.retainedEvents,
	)
	s.turns = append(s.turns, retained...)
	s.retainedTextBytes += textBytes
	s.retainedEvents += eventCount
	if truncationCause != "" {
		s.markTruncated(truncationCause)
		return true, nil
	}

	next := nextCodexTranscriptCursor(page.NextCursor)
	if next == "" {
		return true, nil
	}
	if cause := s.reachedBoundCause(); cause != "" {
		s.markTruncated(cause)
		return true, nil
	}
	if _, exists := s.seenCursors[next]; exists {
		return false, fmt.Errorf("list codex transcript turns: repeated cursor %q", next)
	}
	s.seenCursors[next] = struct{}{}
	s.cursor = next
	return false, nil
}

func (s *codexTranscriptPaginationState) reachedBoundCause() string {
	// A page contains one turn by contract. Bound pages as well as retained
	// events so a history made entirely of tool-only or empty turns cannot grow
	// the cursor set and request count without limit.
	switch {
	case s.retainedTextBytes >= s.maxTextBytes:
		return transcriptSourceCauseCodexText
	case s.retainedEvents >= s.maxEvents:
		return transcriptSourceCauseCodexEvents
	case s.scannedPages >= s.maxEvents:
		return transcriptSourceCauseCodexPages
	default:
		return ""
	}
}

func (s *codexTranscriptPaginationState) markTruncated(cause string) {
	markCodexTranscriptTruncated(s.thread, cause, s.maxTextBytes, s.maxEvents)
}

func nextCodexTranscriptCursor(cursor *string) string {
	if cursor == nil {
		return ""
	}
	return strings.TrimSpace(*cursor)
}

// retainCodexTranscriptTurns returns only canonical-transcript-bearing
// user/assistant items, clipping the final item when necessary. The returned
// turns retain at most maxTextBytes of message text and maxEvents canonical
// events, and never retain summary tool payloads that the canonical transcript
// would discard anyway.
func retainCodexTranscriptTurns(
	turns []CodexTurn,
	maxTextBytes, maxEvents int,
) (retained []CodexTurn, textBytes, eventCount int, truncationCause string) {
	state := codexTranscriptRetentionState{
		remainingTextBytes: max(maxTextBytes, 0),
		remainingEvents:    max(maxEvents, 0),
	}
	retained = make([]CodexTurn, 0, len(turns))
	for _, turn := range turns {
		keptTurn, cause := state.retainTurn(turn)
		if len(keptTurn.Items) > 0 {
			retained = append(retained, keptTurn)
		}
		if cause != "" {
			return retained, state.textBytes, state.eventCount, cause
		}
	}
	return retained, state.textBytes, state.eventCount, ""
}

type codexTranscriptRetentionState struct {
	remainingTextBytes int
	remainingEvents    int
	textBytes          int
	eventCount         int
}

func (s *codexTranscriptRetentionState) retainTurn(turn CodexTurn) (CodexTurn, string) {
	keptTurn := turn
	keptTurn.Items = make([]CodexTurnItem, 0, len(turn.Items))
	for _, item := range turn.Items {
		keptItem, cause := s.retainItem(item)
		if keptItem != nil {
			keptTurn.Items = append(keptTurn.Items, *keptItem)
		}
		if cause != "" {
			return keptTurn, cause
		}
	}
	return keptTurn, ""
}

func (s *codexTranscriptRetentionState) retainItem(item CodexTurnItem) (*CodexTurnItem, string) {
	switch item.Type {
	case "agentMessage":
		return s.retainAgentMessage(item)
	case "userMessage":
		return s.retainUserMessage(item)
	default:
		return nil, ""
	}
}

func (s *codexTranscriptRetentionState) retainAgentMessage(item CodexTurnItem) (*CodexTurnItem, string) {
	if item.Text == "" {
		return nil, ""
	}
	if s.remainingEvents == 0 {
		return nil, transcriptSourceCauseCodexEvents
	}
	keptText, complete := retainCodexText(item.Text, s.remainingTextBytes)
	if keptText == "" {
		return nil, transcriptSourceCauseCodexText
	}
	item.Text = keptText
	item.Content = nil
	s.recordEvent(len(keptText))
	if !complete {
		return &item, transcriptSourceCauseCodexText
	}
	return &item, ""
}

func (s *codexTranscriptRetentionState) retainUserMessage(item CodexTurnItem) (*CodexTurnItem, string) {
	if !codexUserMessageHasText(item.Content) {
		return nil, ""
	}
	if s.remainingEvents == 0 {
		return nil, transcriptSourceCauseCodexEvents
	}
	keptItem := item
	keptItem.Text = ""
	keptItem.Content = make([]CodexContentBlock, 0, len(item.Content))
	for _, block := range item.Content {
		if block.Type != "text" || block.Text == "" {
			continue
		}
		keptBlock, complete := s.retainContentBlock(block)
		if keptBlock != nil {
			keptItem.Content = append(keptItem.Content, *keptBlock)
		}
		if !complete {
			return s.finishUserMessage(keptItem), transcriptSourceCauseCodexText
		}
	}
	return s.finishUserMessage(keptItem), ""
}

func (s *codexTranscriptRetentionState) retainContentBlock(
	block CodexContentBlock,
) (*CodexContentBlock, bool) {
	keptText, complete := retainCodexText(block.Text, s.remainingTextBytes)
	if keptText == "" {
		return nil, complete
	}
	block.Text = keptText
	s.remainingTextBytes -= len(keptText)
	s.textBytes += len(keptText)
	return &block, complete
}

func (s *codexTranscriptRetentionState) finishUserMessage(item CodexTurnItem) *CodexTurnItem {
	if len(item.Content) == 0 {
		return nil
	}
	s.remainingEvents--
	s.eventCount++
	return &item
}

func (s *codexTranscriptRetentionState) recordEvent(textBytes int) {
	s.remainingTextBytes -= textBytes
	s.remainingEvents--
	s.textBytes += textBytes
	s.eventCount++
}

func markCodexTranscriptTruncated(
	thread *CodexThread,
	cause string,
	maxTextBytes, maxEvents int,
) {
	if thread == nil {
		return
	}
	thread.TranscriptTruncated = true
	thread.TranscriptTruncationCause = cause
	switch cause {
	case transcriptSourceCauseCodexText:
		thread.TranscriptSourceLimitBytes = maxTextBytes
	case transcriptSourceCauseCodexEvents:
		thread.TranscriptSourceLimitEvents = maxEvents
	case transcriptSourceCauseCodexPages:
		thread.TranscriptSourceLimitPages = maxEvents
	}
}

func codexUserMessageHasText(content []CodexContentBlock) bool {
	for _, block := range content {
		if block.Type == "text" && block.Text != "" {
			return true
		}
	}
	return false
}

func retainCodexText(text string, maxBytes int) (string, bool) {
	if len(text) <= maxBytes {
		return text, true
	}
	if maxBytes <= 0 {
		return "", false
	}
	// JSON strings are UTF-8. Back up over a continuation byte so clipping
	// never introduces invalid text at the aggregate budget boundary.
	end := maxBytes
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end], false
}

func (c *CodexClient) StartTurn(ctx context.Context, threadID, text string) error {
	threadID = strings.TrimSpace(threadID)
	text = strings.TrimSpace(text)
	if threadID == "" {
		return errors.New("codex thread id required")
	}
	if text == "" {
		return errors.New("codex turn text required")
	}
	params := map[string]any{
		"threadId": threadID,
		"input": []map[string]any{{
			"type":          "text",
			"text":          text,
			"text_elements": []any{},
		}},
	}
	var result json.RawMessage
	return c.Call(ctx, "turn/start", params, &result)
}

func (c *CodexClient) Notify(ctx context.Context, method string, params any) error {
	if c == nil || c.conn == nil {
		return errors.New("codex app-server client is closed")
	}
	req := rpcRequest{Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal codex app-server notification %s: %w", method, err)
	}
	return c.conn.Write(ctx, websocket.MessageText, data)
}

func (c *CodexClient) Call(ctx context.Context, method string, params any, result any) error {
	if c == nil || c.conn == nil {
		return errors.New("codex app-server client is closed")
	}
	id := c.next.Add(1)
	req := rpcRequest{ID: id, Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal codex app-server request %s: %w", method, err)
	}
	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		return fmt.Errorf("write codex app-server request %s: %w", method, err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		readCtx, cancel := context.WithDeadline(ctx, deadline)
		_, msg, err := c.conn.Read(readCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("read codex app-server response for %s: %w", method, err)
		}
		var resp rpcResponse
		if err := json.Unmarshal(msg, &resp); err != nil {
			return fmt.Errorf("decode codex app-server response for %s: %w", method, err)
		}
		if !rpcIDMatches(resp.ID, id) {
			continue
		}
		if resp.Error != nil {
			return resp.Error
		}
		if result == nil {
			return nil
		}
		if len(resp.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("decode codex app-server result for %s: %w", method, err)
		}
		return nil
	}
}

func rpcIDMatches(raw json.RawMessage, want int64) bool {
	if len(raw) == 0 {
		return false
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n == want
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s == strconv.FormatInt(want, 10)
	}
	return false
}
