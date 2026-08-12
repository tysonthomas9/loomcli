package webhooks

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
)

func TestGithubRouteKey(t *testing.T) {
	cases := []struct {
		name   string
		event  string
		action string
		want   string
	}{
		{"with action", "pull_request", "opened", "github.pull_request.opened"},
		{"without action", "push", "", "github.push"},
		{"trims whitespace", " issue_comment ", " created ", "github.issue_comment.created"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := githubRouteKey(tc.event, tc.action); got != tc.want {
				t.Fatalf("githubRouteKey(%q,%q) = %q, want %q", tc.event, tc.action, got, tc.want)
			}
		})
	}
}

func githubRequest(t *testing.T, event, delivery string, body []byte) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/webhooks/github", nil)
	if event != "" {
		r.Header.Set(githubEventHeader, event)
	}
	if delivery != "" {
		r.Header.Set(githubDeliveryHeader, delivery)
	}
	return r
}

func TestGithubNormalize(t *testing.T) {
	body := []byte(`{"action":"opened","pull_request":{"number":42},"repository":{"full_name":"acme/widgets"},"sender":{"login":"octocat"}}`)
	got, err := githubAdapter{}.Normalize(githubRequest(t, "pull_request", "d-1", body), body)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got.RouteKey != "github.pull_request.opened" {
		t.Errorf("RouteKey = %q", got.RouteKey)
	}
	if got.EventType != "pull_request" {
		t.Errorf("EventType = %q", got.EventType)
	}
	if got.DeliveryID != "d-1" {
		t.Errorf("DeliveryID = %q", got.DeliveryID)
	}
	if got.SubjectRef != "acme/widgets#42" {
		t.Errorf("SubjectRef = %q", got.SubjectRef)
	}
	if got.ActorRef != "octocat" {
		t.Errorf("ActorRef = %q", got.ActorRef)
	}
}

// TestGithubNormalizePullRequestActions proves the generic
// "github.{event}.{action}" derivation covers every PR action the router
// binds on — including reopened and ready_for_review, which had no explicit
// coverage before (vet A1 gap: binding patterns like github.pull_request.*
// must see these as distinct, correctly-derived route keys). It also asserts
// the draft attr is carried for both draft=true (opened) and draft=false
// states, so ready_for_review bindings/workflows can branch on it.
func TestGithubNormalizePullRequestActions(t *testing.T) {
	cases := []struct {
		action string
		draft  bool
	}{
		{"opened", true},
		{"opened", false},
		{"synchronize", false},
		{"reopened", false},
		{"ready_for_review", false},
		{"closed", false},
	}
	for _, tc := range cases {
		name := fmt.Sprintf("%s/draft=%t", tc.action, tc.draft)
		t.Run(name, func(t *testing.T) {
			body := fmt.Appendf(nil,
				`{"action":%q,"pull_request":{"number":42,"draft":%t,"head":{"sha":"abc123def456"},"base":{"ref":"main"}},"repository":{"full_name":"acme/widgets"},"sender":{"login":"octocat"}}`,
				tc.action, tc.draft)
			got, err := githubAdapter{}.Normalize(githubRequest(t, "pull_request", "d-"+name, body), body)
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			if want := "github.pull_request." + tc.action; got.RouteKey != want {
				t.Errorf("RouteKey = %q, want %q", got.RouteKey, want)
			}
			// SubjectRef stays owner/repo#N (backcompat); @sha pinning is
			// template-only via attrs.
			if got.SubjectRef != "acme/widgets#42" {
				t.Errorf("SubjectRef = %q, want acme/widgets#42", got.SubjectRef)
			}
			wantAttrs := map[string]string{
				"repo":      "acme/widgets",
				"pr_number": "42",
				"head_sha":  "abc123def456",
				"base_ref":  "main",
				"draft":     strconv.FormatBool(tc.draft),
			}
			if !reflect.DeepEqual(got.SubjectAttrs, wantAttrs) {
				t.Errorf("SubjectAttrs = %#v, want %#v", got.SubjectAttrs, wantAttrs)
			}
		})
	}
}

func TestGithubSubjectAttrs(t *testing.T) {
	cases := []struct {
		name  string
		event string
		body  string
		want  map[string]string
	}{
		{
			name:  "push uses after sha as head_sha",
			event: "push",
			body:  `{"ref":"refs/heads/main","after":"fee1dead","repository":{"full_name":"acme/widgets"}}`,
			want:  map[string]string{"repo": "acme/widgets", "head_sha": "fee1dead"},
		},
		{
			name:  "push without after omits head_sha",
			event: "push",
			body:  `{"ref":"refs/heads/main","repository":{"full_name":"acme/widgets"}}`,
			want:  map[string]string{"repo": "acme/widgets"},
		},
		{
			name:  "pr without head or base omits sha and ref but keeps draft",
			event: "pull_request",
			body:  `{"action":"opened","pull_request":{"number":7},"repository":{"full_name":"acme/widgets"}}`,
			want:  map[string]string{"repo": "acme/widgets", "pr_number": "7", "draft": "false"},
		},
		{
			name:  "pr draft true is carried as draft attr",
			event: "pull_request",
			body:  `{"action":"opened","pull_request":{"number":7,"draft":true},"repository":{"full_name":"acme/widgets"}}`,
			want:  map[string]string{"repo": "acme/widgets", "pr_number": "7", "draft": "true"},
		},
		{
			name:  "pr number falls back to top-level number",
			event: "pull_request",
			body:  `{"action":"opened","number":9,"pull_request":{"head":{"sha":"abc"}}}`,
			want:  map[string]string{"pr_number": "9", "head_sha": "abc", "draft": "false"},
		},
		{
			name:  "issue event extracts issue_number",
			event: "issues",
			body:  `{"action":"opened","issue":{"number":11},"repository":{"full_name":"acme/widgets"}}`,
			want:  map[string]string{"repo": "acme/widgets", "issue_number": "11"},
		},
		{
			name:  "missing repository omits repo",
			event: "pull_request",
			body:  `{"action":"opened","pull_request":{"number":3,"head":{"sha":"abc"},"base":{"ref":"dev"}}}`,
			want:  map[string]string{"pr_number": "3", "head_sha": "abc", "base_ref": "dev", "draft": "false"},
		},
		{
			name:  "empty payload yields nil attrs",
			event: "ping",
			body:  `{}`,
			want:  nil,
		},
		{
			name:  "malformed body yields nil attrs",
			event: "push",
			body:  `{not valid json`,
			want:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			got, err := githubAdapter{}.Normalize(githubRequest(t, tc.event, "d-attrs", body), body)
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			if !reflect.DeepEqual(got.SubjectAttrs, tc.want) {
				t.Errorf("SubjectAttrs = %#v, want %#v", got.SubjectAttrs, tc.want)
			}
		})
	}
}

func TestGithubNormalizeMissingHeaders(t *testing.T) {
	body := []byte(`{}`)
	if _, err := (githubAdapter{}).Normalize(githubRequest(t, "", "d-1", body), body); err == nil {
		t.Error("expected error for missing event header")
	}
	if _, err := (githubAdapter{}).Normalize(githubRequest(t, "push", "", body), body); err == nil {
		t.Error("expected error for missing delivery header")
	}
}

func TestGithubNormalizePushSubjectRef(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main","repository":{"full_name":"acme/widgets"}}`)
	got, err := githubAdapter{}.Normalize(githubRequest(t, "push", "d-3", body), body)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got.RouteKey != "github.push" {
		t.Errorf("RouteKey = %q, want github.push", got.RouteKey)
	}
	if got.SubjectRef != "acme/widgets@refs/heads/main" {
		t.Errorf("SubjectRef = %q, want acme/widgets@refs/heads/main", got.SubjectRef)
	}
}

func TestGithubNormalizeMalformedBodyStillRoutes(t *testing.T) {
	body := []byte(`{not valid json`)
	got, err := githubAdapter{}.Normalize(githubRequest(t, "push", "d-2", body), body)
	if err != nil {
		t.Fatalf("Normalize should tolerate malformed body: %v", err)
	}
	if got.RouteKey != "github.push" {
		t.Errorf("RouteKey = %q, want github.push", got.RouteKey)
	}
}

func TestGithubVerify(t *testing.T) {
	secret := "s3cr3t"
	body := []byte(`{"action":"opened"}`)
	verify := func(r *http.Request, payload []byte, secret string) error {
		adapter := githubAdapter{}
		return adapter.VerifySignature(payload, adapter.PresentedSignature(r), secret)
	}

	t.Run("valid signature", func(t *testing.T) {
		r := githubRequest(t, "pull_request", "d", body)
		r.Header.Set(githubSignatureHeader, githubSignature(secret, body))
		if err := verify(r, body, secret); err != nil {
			t.Fatalf("Verify rejected valid signature: %v", err)
		}
	})

	t.Run("wrong secret", func(t *testing.T) {
		r := githubRequest(t, "pull_request", "d", body)
		r.Header.Set(githubSignatureHeader, githubSignature("other", body))
		if err := verify(r, body, secret); err == nil {
			t.Fatal("Verify accepted signature from wrong secret")
		}
	})

	t.Run("tampered body", func(t *testing.T) {
		r := githubRequest(t, "pull_request", "d", body)
		r.Header.Set(githubSignatureHeader, githubSignature(secret, body))
		if err := verify(r, []byte(`{"action":"closed"}`), secret); err == nil {
			t.Fatal("Verify accepted signature for tampered body")
		}
	})

	t.Run("missing header", func(t *testing.T) {
		r := githubRequest(t, "pull_request", "d", body)
		if err := verify(r, body, secret); err == nil {
			t.Fatal("Verify accepted missing signature")
		}
	})

	t.Run("empty secret", func(t *testing.T) {
		r := githubRequest(t, "pull_request", "d", body)
		r.Header.Set(githubSignatureHeader, githubSignature("", body))
		if err := verify(r, body, ""); err == nil {
			t.Fatal("Verify accepted empty secret")
		}
	})

	t.Run("non-sha256 prefix", func(t *testing.T) {
		r := githubRequest(t, "pull_request", "d", body)
		r.Header.Set(githubSignatureHeader, "sha1=deadbeef")
		if err := verify(r, body, secret); err == nil {
			t.Fatal("Verify accepted non-sha256 signature prefix")
		}
	})
}
