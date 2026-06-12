package providers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// Slack connector actions (dotted snake_case per the CV1 action grammar).
const (
	// ActionSlackChatPost posts a channel/thread-scoped message. Slack has
	// no native precondition header, so freshness is a best-effort
	// pre-egress conversations.info access check: StaleSubject when the
	// channel is gone, archived, or the bot was evicted, refused WITHOUT
	// issuing the post. The check-to-post TOCTOU window is accepted and
	// documented because a posted message is reversible (deletable),
	// unlike a merge. Idempotency rides as a client_msg_id derived from
	// the runID-derived IdempotencyKey.
	ActionSlackChatPost = "slack.chat.post"
	// ActionSlackConversationsRead reads recent conversation history
	// (read-only).
	ActionSlackConversationsRead = "slack.conversations.read"
)

// SlackActions returns the actions the Slack provider implements (a copy).
func SlackActions() []string {
	return []string{
		ActionSlackChatPost,
		ActionSlackConversationsRead,
	}
}

// DefaultSlackBaseURL is the public Slack Web API endpoint.
const DefaultSlackBaseURL = "https://slack.com/api"

// channelScopePrefix marks a channel-scoped grant resource, e.g.
// "channel:C0123456".
const channelScopePrefix = "channel:"

// slackStaleReasons maps Slack error codes that mean the channel subject
// moved (gone, archived, bot evicted) to redaction-safe StaleSubject reasons.
var slackStaleReasons = map[string]string{
	"channel_not_found": "channel not found",
	"is_archived":       "channel is archived",
	"not_in_channel":    "bot evicted from channel",
}

// Slack is the Provider adapter for the Slack Web API. The base URL is
// injectable for tests; the zero values fall back to the public API and
// http.DefaultClient.
type Slack struct {
	baseURL string
	client  *http.Client
}

var _ Provider = (*Slack)(nil)

// NewSlack builds a Slack provider. client defaults to http.DefaultClient
// and baseURL to DefaultSlackBaseURL when empty.
func NewSlack(client *http.Client, baseURL string) *Slack {
	if client == nil {
		client = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = DefaultSlackBaseURL
	}
	return &Slack{baseURL: strings.TrimSuffix(baseURL, "/"), client: client}
}

// Call implements Provider, dispatching on spec.Action.
func (s *Slack) Call(ctx context.Context, spec CallSpec) (CallResult, error) {
	switch spec.Action {
	case ActionSlackChatPost:
		return s.chatPost(ctx, spec)
	case ActionSlackConversationsRead:
		return s.conversationsRead(ctx, spec)
	default:
		return CallResult{Decision: domain.ConnectorCallUpstreamError},
			fmt.Errorf("slack provider does not implement %q: %w", spec.Action, ErrUnknownAction)
	}
}

// chatPost posts a message via chat.postMessage, scoped to the granted
// channel resource and gated by a best-effort pre-egress conversations.info
// access check: the write is never issued when the channel is gone,
// archived, or the bot was evicted (StaleSubject). Slack offers no native
// precondition, so the check-to-post TOCTOU window is accepted — a posted
// message is deletable.
func (s *Slack) chatPost(ctx context.Context, spec CallSpec) (CallResult, error) {
	if err := requireIdempotencyKey(spec); err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	channel, ok := stringArg(spec.Args, "channel")
	if !ok {
		return CallResult{Decision: domain.ConnectorCallUpstreamError},
			fmt.Errorf("%s requires args.channel: %w", spec.Action, domain.ErrInvalid)
	}
	text, ok := stringArg(spec.Args, "text")
	if !ok {
		return CallResult{Decision: domain.ConnectorCallUpstreamError},
			fmt.Errorf("%s requires args.text: %w", spec.Action, domain.ErrInvalid)
	}
	if err := requireChannelScope(spec, channel); err != nil {
		return CallResult{Decision: domain.ConnectorCallDenied}, err
	}

	// Best-effort pre-egress access check.
	res, err := s.do(ctx, spec, http.MethodGet, "/conversations.info",
		url.Values{"channel": []string{channel}}, nil)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	if err := s.checkConversationAccess(spec, channel, res); err != nil {
		result := CallResult{Decision: DecisionForError(err)}
		if result.Decision != domain.ConnectorCallStaleSubject {
			// The refusal is upstream-shaped, not a freshness refusal:
			// surface the access-check status for the audit journal.
			result.Status = res.status
		}
		return result, err
	}

	payload := map[string]any{
		"channel": channel,
		"text":    text,
		// Deterministic per idempotency key so a task retry re-sends the
		// same client_msg_id instead of minting a new message identity.
		"client_msg_id": clientMsgID(spec.IdempotencyKey),
	}
	if v, ok := stringArg(spec.Args, "threadTs"); ok {
		payload["thread_ts"] = v
	}
	res, err = s.do(ctx, spec, http.MethodPost, "/chat.postMessage", nil, payload)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	obj, err := s.classify(spec, channel, res)
	if err != nil {
		return CallResult{Status: res.status, Decision: DecisionForError(err)}, err
	}
	return CallResult{
		Status:   res.status,
		Body:     map[string]any{"channel": obj["channel"], "ts": obj["ts"]},
		Decision: domain.ConnectorCallGranted,
	}, nil
}

// conversationsRead reads recent conversation history (read-only), scoped to
// the granted channel resource.
func (s *Slack) conversationsRead(ctx context.Context, spec CallSpec) (CallResult, error) {
	channel, ok := stringArg(spec.Args, "channel")
	if !ok {
		return CallResult{Decision: domain.ConnectorCallUpstreamError},
			fmt.Errorf("%s requires args.channel: %w", spec.Action, domain.ErrInvalid)
	}
	if err := requireChannelScope(spec, channel); err != nil {
		return CallResult{Decision: domain.ConnectorCallDenied}, err
	}
	query := url.Values{"channel": []string{channel}}
	if v, ok, err := intArg(spec.Args, "limit"); err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	} else if ok {
		query.Set("limit", strconv.Itoa(v))
	}
	if v, ok := stringArg(spec.Args, "oldest"); ok {
		query.Set("oldest", v)
	}
	res, err := s.do(ctx, spec, http.MethodGet, "/conversations.history", query, nil)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	obj, err := s.classify(spec, channel, res)
	if err != nil {
		return CallResult{Status: res.status, Decision: DecisionForError(err)}, err
	}
	raw, _ := obj["messages"].([]any)
	messages := make([]map[string]any, 0, len(raw))
	for _, m := range raw {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		messages = append(messages, map[string]any{
			"type":     mm["type"],
			"user":     mm["user"],
			"text":     mm["text"],
			"ts":       mm["ts"],
			"threadTs": mm["thread_ts"],
		})
	}
	return CallResult{
		Status:   res.status,
		Body:     map[string]any{"messages": messages, "hasMore": obj["has_more"]},
		Decision: domain.ConnectorCallGranted,
	}, nil
}

// checkConversationAccess inspects a conversations.info response and refuses
// with StaleSubject when the channel subject moved: API error codes meaning
// gone/archived/evicted, an archived channel object, or an explicit
// is_member: false (best-effort — chat:write.public bots may post without
// membership, but conversations.info then omits or sets the field truthfully
// for the granted bot).
func (s *Slack) checkConversationAccess(spec CallSpec, channel string, res httpResult) error {
	obj, err := s.classify(spec, channel, res)
	if err != nil {
		return err
	}
	ch, _ := obj["channel"].(map[string]any)
	if archived, _ := ch["is_archived"].(bool); archived {
		return &StaleSubject{
			Action:   spec.Action,
			Resource: spec.Resource,
			Expected: channel,
			Reason:   slackStaleReasons["is_archived"],
		}
	}
	if member, present := ch["is_member"].(bool); present && !member {
		return &StaleSubject{
			Action:   spec.Action,
			Resource: spec.Resource,
			Expected: channel,
			Reason:   slackStaleReasons["not_in_channel"],
		}
	}
	return nil
}

// classify maps one Slack Web API response to the structured provider
// errors. Slack reports most failures as HTTP 200 with ok:false plus an
// error code, so both the HTTP status and the code are inspected: 429 and
// the ratelimited code become RateLimited; 5xx becomes a retryable server
// UpstreamError; stale-shaped codes become StaleSubject; any other ok:false
// becomes a client UpstreamError. Only the error code — never the raw body —
// feeds summaries, and it is sanitized anyway.
func (s *Slack) classify(spec CallSpec, channel string, res httpResult) (map[string]any, error) {
	if res.status == http.StatusTooManyRequests {
		return nil, &RateLimited{
			Action:     spec.Action,
			Status:     res.status,
			RetryAfter: parseRetryAfter(res.header),
		}
	}
	obj := decodeObject(res.body)
	code, _ := obj["error"].(string)
	if res.status >= 500 {
		return nil, &UpstreamError{
			Action:  spec.Action,
			Class:   ClassServerError,
			Status:  res.status,
			Summary: sanitizeUpstreamMessage(code, spec.Credential),
		}
	}
	if okFlag, _ := obj["ok"].(bool); okFlag {
		return obj, nil
	}
	if code == "ratelimited" || code == "rate_limited" {
		return nil, &RateLimited{
			Action:     spec.Action,
			Status:     res.status,
			RetryAfter: parseRetryAfter(res.header),
		}
	}
	if reason, stale := slackStaleReasons[code]; stale {
		return nil, &StaleSubject{
			Action:   spec.Action,
			Resource: spec.Resource,
			Expected: channel,
			Reason:   reason,
		}
	}
	return nil, &UpstreamError{
		Action:  spec.Action,
		Class:   ClassClientError,
		Status:  res.status,
		Summary: sanitizeUpstreamMessage(code, spec.Credential),
	}
}

// do issues one Slack Web API request: the credential rides in the
// Authorization header only.
func (s *Slack) do(ctx context.Context, spec CallSpec, method, path string, query url.Values, payload any) (httpResult, error) {
	u := s.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return doJSON(ctx, s.client, spec, method, u, payload,
		func(req *http.Request) {
			req.Header.Set("Authorization", "Bearer "+spec.Credential)
		},
		func(msg string) string { return sanitizeUpstreamMessage(msg, spec.Credential) })
}

// requireChannelScope re-checks — defense in depth; the dispatch layer
// already matched the grant's resource pattern — that the call targets the
// granted channel when the resource is channel-scoped. Mismatches wrap
// domain.ErrInvalid, refuse before any egress, and audit as denied.
func requireChannelScope(spec CallSpec, channel string) error {
	if !strings.HasPrefix(spec.Resource, channelScopePrefix) {
		return nil
	}
	if granted := strings.TrimPrefix(spec.Resource, channelScopePrefix); granted != channel {
		return fmt.Errorf("%s: args.channel %q is outside the granted resource %q: %w",
			spec.Action, channel, spec.Resource, domain.ErrInvalid)
	}
	return nil
}

// clientMsgID derives Slack's client_msg_id from the runID-derived
// idempotency key: deterministic so a task retry re-sends the same message
// identity, hashed so run identifiers never leak into Slack metadata.
func clientMsgID(idempotencyKey string) string {
	sum := sha256.Sum256([]byte(idempotencyKey))
	return hex.EncodeToString(sum[:16])
}

// doJSON issues one HTTP request for a provider adapter: payload (when
// non-nil) is JSON-encoded, the runID-derived idempotency key rides as the
// Idempotency-Key header on every request, prepare applies the provider's
// auth headers, and transport failures map to UpstreamError with
// ClassNetwork after sanitize scrubs credential material. Shared by the
// Slack and Datadog adapters; the GitHub adapter (CV6) predates it and keeps
// its own equivalent.
func doJSON(ctx context.Context, client *http.Client, spec CallSpec, method, urlStr string, payload any, prepare func(*http.Request), sanitize func(string) string) (httpResult, error) {
	var bodyReader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return httpResult{}, fmt.Errorf("providers: encode %s request: %w", spec.Action, err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, urlStr, bodyReader)
	if err != nil {
		return httpResult{}, fmt.Errorf("providers: build %s request: %w", spec.Action, err)
	}
	if spec.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", spec.IdempotencyKey)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	prepare(req)
	resp, err := client.Do(req)
	if err != nil {
		return httpResult{}, &UpstreamError{
			Action:  spec.Action,
			Class:   ClassNetwork,
			Summary: sanitize(err.Error()),
		}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return httpResult{}, &UpstreamError{
			Action:  spec.Action,
			Class:   ClassNetwork,
			Status:  resp.StatusCode,
			Summary: sanitize(err.Error()),
		}
	}
	return httpResult{status: resp.StatusCode, header: resp.Header, body: raw}, nil
}
