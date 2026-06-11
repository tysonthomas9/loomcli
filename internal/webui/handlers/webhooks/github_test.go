package webhooks

import (
	"net/http"
	"net/http/httptest"
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

	t.Run("valid signature", func(t *testing.T) {
		r := githubRequest(t, "pull_request", "d", body)
		r.Header.Set(githubSignatureHeader, githubSignature(secret, body))
		if err := (githubAdapter{}).Verify(r, body, secret); err != nil {
			t.Fatalf("Verify rejected valid signature: %v", err)
		}
	})

	t.Run("wrong secret", func(t *testing.T) {
		r := githubRequest(t, "pull_request", "d", body)
		r.Header.Set(githubSignatureHeader, githubSignature("other", body))
		if err := (githubAdapter{}).Verify(r, body, secret); err == nil {
			t.Fatal("Verify accepted signature from wrong secret")
		}
	})

	t.Run("tampered body", func(t *testing.T) {
		r := githubRequest(t, "pull_request", "d", body)
		r.Header.Set(githubSignatureHeader, githubSignature(secret, body))
		if err := (githubAdapter{}).Verify(r, []byte(`{"action":"closed"}`), secret); err == nil {
			t.Fatal("Verify accepted signature for tampered body")
		}
	})

	t.Run("missing header", func(t *testing.T) {
		r := githubRequest(t, "pull_request", "d", body)
		if err := (githubAdapter{}).Verify(r, body, secret); err == nil {
			t.Fatal("Verify accepted missing signature")
		}
	})

	t.Run("empty secret", func(t *testing.T) {
		r := githubRequest(t, "pull_request", "d", body)
		r.Header.Set(githubSignatureHeader, githubSignature("", body))
		if err := (githubAdapter{}).Verify(r, body, ""); err == nil {
			t.Fatal("Verify accepted empty secret")
		}
	})

	t.Run("non-sha256 prefix", func(t *testing.T) {
		r := githubRequest(t, "pull_request", "d", body)
		r.Header.Set(githubSignatureHeader, "sha1=deadbeef")
		if err := (githubAdapter{}).Verify(r, body, secret); err == nil {
			t.Fatal("Verify accepted non-sha256 signature prefix")
		}
	})
}
