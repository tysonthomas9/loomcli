package connectorsproviders

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	legacydomain "github.com/tysonthomas9/loomcli/internal/domain"
	domain "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

const testSlackToken = "xoxb-SECRET-slack-token-123456"

// The route-table fake from github_test.go is API-agnostic; the Slack tests
// reuse it as a fake Slack Web API.
func slackProvider(f *fakeGitHub) *Slack {
	return NewSlack(f.server.Client(), f.server.URL)
}

func slackPostSpec() CallSpec {
	return CallSpec{
		Action:   ActionSlackChatPost,
		Resource: "channel:C123",
		Args: map[string]any{
			"channel":  "C123",
			"text":     "deploy finished",
			"threadTs": "1700000000.000100",
		},
		IdempotencyKey: "run-1#slack.chat.post#0",
		Credential:     testSlackToken,
	}
}

const slackOpenChannelInfo = `{"ok":true,"channel":{"id":"C123","is_archived":false,"is_member":true}}`

func assertNoSlackToken(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), testSlackToken) {
		t.Fatalf("credential leaked into error text: %v", err)
	}
}

func TestSlackActionsPassActionGrammar(t *testing.T) {
	for _, action := range SlackActions() {
		if err := legacydomain.ValidateConnectorAction(action); err != nil {
			t.Errorf("action %q fails the CV1 grammar: %v", action, err)
		}
	}
}

func TestSlackChatPostHappyPath(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodGet, "/conversations.info", fakeResponse{
		status: http.StatusOK, body: slackOpenChannelInfo,
	})
	fake.route(http.MethodPost, "/chat.postMessage", fakeResponse{
		status: http.StatusOK,
		body:   `{"ok":true,"channel":"C123","ts":"1700000001.000200"}`,
	})

	spec := slackPostSpec()
	result, err := slackProvider(fake).Call(context.Background(), spec)
	if err != nil {
		t.Fatalf("chat.post: %v", err)
	}
	if result.Decision != domain.ConnectorCallGranted || result.Status != http.StatusOK {
		t.Fatalf("result = %+v, want status 200 granted", result)
	}
	if result.Body["channel"] != "C123" || result.Body["ts"] != "1700000001.000200" {
		t.Errorf("body = %+v", result.Body)
	}

	reqs := fake.recorded()
	if len(reqs) != 2 || reqs[0].Method != http.MethodGet || reqs[1].Method != http.MethodPost {
		t.Fatalf("requests = %+v, want access-check GET then POST", reqs)
	}
	if !strings.Contains(reqs[0].Query, "channel=C123") {
		t.Errorf("access check query = %q, want channel=C123", reqs[0].Query)
	}
	post := reqs[1]
	if post.Body["channel"] != "C123" || post.Body["text"] != "deploy finished" {
		t.Errorf("post payload = %+v", post.Body)
	}
	if post.Body["thread_ts"] != "1700000000.000100" {
		t.Errorf("thread_ts = %v, want camelCase threadTs mapped to thread_ts", post.Body["thread_ts"])
	}
	if got := post.Body["client_msg_id"]; got != clientMsgID(spec.IdempotencyKey) || got == "" {
		t.Errorf("client_msg_id = %v, want deterministic derivation from the idempotency key", got)
	}
	for _, req := range reqs {
		if got := req.Header.Get("Authorization"); got != "Bearer "+testSlackToken {
			t.Errorf("%s %s Authorization = %q, want bearer credential", req.Method, req.Path, got)
		}
		if got := req.Header.Get("Idempotency-Key"); got != spec.IdempotencyKey {
			t.Errorf("%s %s Idempotency-Key = %q, want runID-derived key", req.Method, req.Path, got)
		}
	}
}

func TestSlackChatPostAccessCheckShortCircuits(t *testing.T) {
	tests := []struct {
		name       string
		infoBody   string
		wantReason string
	}{
		{
			name:       "channel gone",
			infoBody:   `{"ok":false,"error":"channel_not_found"}`,
			wantReason: "channel not found",
		},
		{
			name:       "channel archived via error code",
			infoBody:   `{"ok":false,"error":"is_archived"}`,
			wantReason: "archived",
		},
		{
			name:       "channel archived via channel object",
			infoBody:   `{"ok":true,"channel":{"id":"C123","is_archived":true}}`,
			wantReason: "archived",
		},
		{
			name:       "bot evicted",
			infoBody:   `{"ok":true,"channel":{"id":"C123","is_archived":false,"is_member":false}}`,
			wantReason: "evicted",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeGitHub(t)
			fake.route(http.MethodGet, "/conversations.info", fakeResponse{
				status: http.StatusOK, body: tt.infoBody,
			})
			fake.route(http.MethodPost, "/chat.postMessage", fakeResponse{
				status: http.StatusOK, body: `{"ok":true,"channel":"C123","ts":"1.2"}`,
			})

			result, err := slackProvider(fake).Call(context.Background(), slackPostSpec())
			var stale *StaleSubject
			if !errors.As(err, &stale) {
				t.Fatalf("error %T is not *StaleSubject (err=%v)", err, err)
			}
			if !strings.Contains(stale.Reason, tt.wantReason) {
				t.Errorf("reason = %q, want substring %q", stale.Reason, tt.wantReason)
			}
			if !errors.Is(err, domain.ErrConflict) {
				t.Error("StaleSubject must match domain.ErrConflict")
			}
			if result.Decision != domain.ConnectorCallStaleSubject {
				t.Errorf("decision = %q, want stale_subject", result.Decision)
			}
			assertNoSlackToken(t, err)
			for _, req := range fake.recorded() {
				if req.Method == http.MethodPost {
					t.Fatalf("write was issued despite stale subject: %s %s", req.Method, req.Path)
				}
			}
		})
	}
}

func TestSlackChannelScopeEnforced(t *testing.T) {
	fake := newFakeGitHub(t)
	provider := slackProvider(fake)

	post := slackPostSpec()
	post.Resource = "channel:C999"
	result, err := provider.Call(context.Background(), post)
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("scope mismatch err = %v, want domain.ErrInvalid", err)
	}
	if result.Decision != domain.ConnectorCallDenied {
		t.Errorf("decision = %q, want denied", result.Decision)
	}

	read := CallSpec{
		Action:     ActionSlackConversationsRead,
		Resource:   "channel:C999",
		Args:       map[string]any{"channel": "C123"},
		Credential: testSlackToken,
	}
	if _, err := provider.Call(context.Background(), read); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("read scope mismatch err = %v, want domain.ErrInvalid", err)
	}

	if n := len(fake.recorded()); n != 0 {
		t.Errorf("fake saw %d requests, want 0 (refused before egress)", n)
	}
}

func TestSlackChatPostRequiresIdempotencyKey(t *testing.T) {
	fake := newFakeGitHub(t)
	spec := slackPostSpec()
	spec.IdempotencyKey = ""

	_, err := slackProvider(fake).Call(context.Background(), spec)
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("err = %v, want domain.ErrInvalid", err)
	}
	if n := len(fake.recorded()); n != 0 {
		t.Errorf("fake saw %d requests, want 0", n)
	}
}

func TestSlackRateLimits(t *testing.T) {
	tests := []struct {
		name          string
		postResponse  fakeResponse
		wantRetryWait time.Duration
	}{
		{
			name: "HTTP 429 with Retry-After",
			postResponse: fakeResponse{
				status: http.StatusTooManyRequests,
				header: map[string]string{"Retry-After": "30"},
				body:   `{"ok":false,"error":"ratelimited"}`,
			},
			wantRetryWait: 30 * time.Second,
		},
		{
			name: "ok:false ratelimited code",
			postResponse: fakeResponse{
				status: http.StatusOK,
				body:   `{"ok":false,"error":"ratelimited"}`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeGitHub(t)
			fake.route(http.MethodGet, "/conversations.info", fakeResponse{
				status: http.StatusOK, body: slackOpenChannelInfo,
			})
			fake.route(http.MethodPost, "/chat.postMessage", tt.postResponse)

			result, err := slackProvider(fake).Call(context.Background(), slackPostSpec())
			var rl *RateLimited
			if !errors.As(err, &rl) {
				t.Fatalf("error %T is not *RateLimited (err=%v)", err, err)
			}
			if !Retryable(err) || !errors.Is(err, ErrUpstream) {
				t.Error("rate limits must be retryable and match ErrUpstream")
			}
			if rl.RetryAfter != tt.wantRetryWait {
				t.Errorf("RetryAfter = %v, want %v", rl.RetryAfter, tt.wantRetryWait)
			}
			if result.Decision != domain.ConnectorCallUpstreamError {
				t.Errorf("decision = %q, want upstream_error", result.Decision)
			}
		})
	}
}

func TestSlackUpstreamErrorRedactsToken(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodGet, "/conversations.info", fakeResponse{
		status: http.StatusOK, body: slackOpenChannelInfo,
	})
	fake.route(http.MethodPost, "/chat.postMessage", fakeResponse{
		status: http.StatusInternalServerError,
		body:   `{"ok":false,"error":"internal failure echoing ` + testSlackToken + `"}`,
	})

	result, err := slackProvider(fake).Call(context.Background(), slackPostSpec())
	var ue *UpstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("error %T is not *UpstreamError", err)
	}
	if ue.Class != ClassServerError || !Retryable(err) {
		t.Errorf("class = %q retryable = %v, want retryable server_error", ue.Class, Retryable(err))
	}
	if result.Status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", result.Status)
	}
	assertNoSlackToken(t, err)
}

func TestSlackNetworkErrorIsRetryableAndRedacted(t *testing.T) {
	fake := newFakeGitHub(t)
	provider := slackProvider(fake)
	fake.server.Close() // force a transport failure

	_, err := provider.Call(context.Background(), slackPostSpec())
	var ue *UpstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("error %T is not *UpstreamError", err)
	}
	if ue.Class != ClassNetwork || !Retryable(err) {
		t.Errorf("class = %q retryable = %v, want retryable network", ue.Class, Retryable(err))
	}
	assertNoSlackToken(t, err)
}

func TestSlackConversationsRead(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.route(http.MethodGet, "/conversations.history", fakeResponse{
		status: http.StatusOK,
		body: `{"ok":true,"has_more":true,"messages":[
			{"type":"message","user":"U1","text":"hello","ts":"1700000000.000100","thread_ts":"1700000000.000050"}]}`,
	})

	result, err := slackProvider(fake).Call(context.Background(), CallSpec{
		Action:     ActionSlackConversationsRead,
		Resource:   "channel:C123",
		Args:       map[string]any{"channel": "C123", "limit": 50},
		Credential: testSlackToken,
	})
	if err != nil {
		t.Fatalf("conversations.read: %v", err)
	}
	if result.Decision != domain.ConnectorCallGranted {
		t.Errorf("decision = %q, want granted", result.Decision)
	}
	if result.Body["hasMore"] != true {
		t.Errorf("hasMore = %v, want true", result.Body["hasMore"])
	}
	messages, ok := result.Body["messages"].([]map[string]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %+v, want one whitelisted message", result.Body["messages"])
	}
	msg := messages[0]
	if msg["user"] != "U1" || msg["text"] != "hello" || msg["threadTs"] != "1700000000.000050" {
		t.Errorf("message = %+v, want camelCase threadTs and whitelisted fields", msg)
	}

	req := fake.recorded()[0]
	for _, want := range []string{"channel=C123", "limit=50"} {
		if !strings.Contains(req.Query, want) {
			t.Errorf("query %q missing %q", req.Query, want)
		}
	}
}

func TestSlackUnknownActionAndBadArgs(t *testing.T) {
	fake := newFakeGitHub(t)
	provider := slackProvider(fake)

	_, err := provider.Call(context.Background(), CallSpec{Action: "slack.channel.delete"})
	if !errors.Is(err, ErrUnknownAction) || !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("unknown action err = %v, want ErrUnknownAction wrapping domain.ErrInvalid", err)
	}

	spec := slackPostSpec()
	delete(spec.Args, "text")
	if _, err := provider.Call(context.Background(), spec); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("missing text err = %v, want domain.ErrInvalid", err)
	}

	if n := len(fake.recorded()); n != 0 {
		t.Errorf("fake saw %d requests, want 0", n)
	}
}

func TestRegistrySlackAndDatadog(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(domain.ConnectorSourceSlack, NewSlack(nil, "")); err != nil {
		t.Fatalf("register slack: %v", err)
	}
	if err := reg.Register(domain.ConnectorSourceDatadog, NewDatadog(nil, "")); err != nil {
		t.Fatalf("register datadog: %v", err)
	}
	if p, err := reg.Get(domain.ConnectorSourceSlack); err != nil || p == nil {
		t.Errorf("get slack = %v, %v", p, err)
	}
	if p, err := reg.Get(domain.ConnectorSourceDatadog); err != nil || p == nil {
		t.Errorf("get datadog = %v, %v", p, err)
	}
}
