package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// githubAdapter implements Adapter for GitHub webhooks. It derives route keys
// of the form "github.{event}.{action}" (or "github.{event}" when the payload
// carries no action) and verifies the X-Hub-Signature-256 HMAC.
type githubAdapter struct{}

func (githubAdapter) Name() string { return "github" }

const (
	githubEventHeader     = "X-GitHub-Event"
	githubDeliveryHeader  = "X-GitHub-Delivery"
	githubSignatureHeader = "X-Hub-Signature-256"
	githubSignaturePrefix = "sha256="
)

// githubPayload captures only the fields needed for routing and subject
// derivation. Unknown fields are ignored, so malformed or partial payloads
// degrade gracefully to "github.{event}".
type githubPayload struct {
	Action      string `json:"action"`
	Ref         string `json:"ref"`
	Number      int    `json:"number"`
	PullRequest *struct {
		Number int `json:"number"`
	} `json:"pull_request"`
	Issue *struct {
		Number int `json:"number"`
	} `json:"issue"`
	Repository *struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender *struct {
		Login string `json:"login"`
	} `json:"sender"`
}

func (githubAdapter) Normalize(r *http.Request, body []byte) (NormalizedEvent, error) {
	event := strings.TrimSpace(r.Header.Get(githubEventHeader))
	if event == "" {
		return NormalizedEvent{}, badRequest("missing " + githubEventHeader + " header")
	}
	delivery := strings.TrimSpace(r.Header.Get(githubDeliveryHeader))
	if delivery == "" {
		return NormalizedEvent{}, badRequest("missing " + githubDeliveryHeader + " header")
	}

	// Best-effort parse: a malformed body still yields a usable route key.
	var payload githubPayload
	_ = json.Unmarshal(body, &payload)

	return NormalizedEvent{
		RouteKey:   githubRouteKey(event, payload.Action),
		EventType:  event,
		SubjectRef: githubSubjectRef(event, payload),
		ActorRef:   githubActorRef(payload),
		DeliveryID: delivery,
	}, nil
}

// githubRouteKey builds "github.{event}.{action}" when an action is present,
// otherwise "github.{event}". Exposed package-internally for unit testing.
func githubRouteKey(event, action string) string {
	event = strings.TrimSpace(event)
	action = strings.TrimSpace(action)
	if action == "" {
		return "github." + event
	}
	return "github." + event + "." + action
}

func githubSubjectRef(event string, p githubPayload) string {
	repo := ""
	if p.Repository != nil {
		repo = p.Repository.FullName
	}
	switch {
	case p.PullRequest != nil && p.PullRequest.Number > 0:
		return joinSubject(repo, fmt.Sprintf("#%d", p.PullRequest.Number))
	case p.Issue != nil && p.Issue.Number > 0:
		return joinSubject(repo, fmt.Sprintf("#%d", p.Issue.Number))
	case p.Number > 0:
		return joinSubject(repo, fmt.Sprintf("#%d", p.Number))
	case event == "push" && p.Ref != "":
		// Refs carry no "#" separator, so add an explicit "@" delimiter.
		return joinSubject(repo, "@"+p.Ref)
	default:
		return repo
	}
}

// joinSubject appends an already-delimited suffix (e.g. "#42" or "@refs/...")
// to the repository name, or returns the suffix alone when the repo is unknown.
func joinSubject(repo, suffix string) string {
	if repo == "" {
		return strings.TrimPrefix(suffix, "@")
	}
	return repo + suffix
}

func githubActorRef(p githubPayload) string {
	if p.Sender != nil {
		return p.Sender.Login
	}
	return ""
}

func (githubAdapter) Verify(r *http.Request, body []byte, secret string) error {
	if strings.TrimSpace(secret) == "" {
		return unverified("webhook secret is not configured for this binding")
	}
	header := strings.TrimSpace(r.Header.Get(githubSignatureHeader))
	if header == "" {
		return unverified("missing " + githubSignatureHeader + " header")
	}
	if !strings.HasPrefix(header, githubSignaturePrefix) {
		return unverified("signature must be " + githubSignaturePrefix + " hex")
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(header, githubSignaturePrefix))
	if err != nil {
		return unverified("signature is not valid hex")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)
	if subtle.ConstantTimeCompare(provided, expected) != 1 {
		return unverified("signature does not match")
	}
	return nil
}

// githubSignature computes the X-Hub-Signature-256 header value for a body and
// secret. Used by tests and tooling to produce signed requests.
func githubSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return githubSignaturePrefix + hex.EncodeToString(mac.Sum(nil))
}
