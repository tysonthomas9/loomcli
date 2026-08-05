package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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
	Action string `json:"action"`
	Ref    string `json:"ref"`
	// After is the post-push head commit SHA on push events.
	After       string `json:"after"`
	Number      int    `json:"number"`
	PullRequest *struct {
		Number int  `json:"number"`
		Draft  bool `json:"draft"`
		Head   *struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Base *struct {
			Ref string `json:"ref"`
		} `json:"base"`
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
		RouteKey:     githubRouteKey(event, payload.Action),
		EventType:    event,
		SubjectRef:   githubSubjectRef(event, payload),
		ActorRef:     githubActorRef(payload),
		DeliveryID:   delivery,
		SubjectAttrs: githubSubjectAttrs(event, payload),
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

// githubSubjectAttrs extracts the payload attributes consumed by server-side
// subject_key_template rendering ("{{attrs.head_sha}}" pins repo#PR@sha
// subject keys). SubjectRef itself stays owner/repo#N for backcompat; the @sha
// pinning happens only via templates. Keys are set only when the payload
// carries the value, so templates fall back deterministically on partial
// payloads; nil is returned when nothing applies (e.g. malformed body).
func githubSubjectAttrs(event string, p githubPayload) map[string]string {
	attrs := map[string]string{}
	if p.Repository != nil && p.Repository.FullName != "" {
		attrs["repo"] = p.Repository.FullName
	}
	if pr := p.PullRequest; pr != nil {
		if pr.Number > 0 {
			attrs["pr_number"] = strconv.Itoa(pr.Number)
		} else if p.Number > 0 {
			attrs["pr_number"] = strconv.Itoa(p.Number)
		}
		if pr.Head != nil && pr.Head.SHA != "" {
			attrs["head_sha"] = pr.Head.SHA
		}
		if pr.Base != nil && pr.Base.Ref != "" {
			attrs["base_ref"] = pr.Base.Ref
		}
		// draft is a non-optional bool on every pull_request payload, so it is
		// set whenever a PR subject is present (unlike head_sha/base_ref, which
		// are sub-objects that may be absent). This lets ready_for_review
		// bindings and workflows branch on the PR's draft state.
		attrs["draft"] = strconv.FormatBool(pr.Draft)
	}
	if p.Issue != nil && p.Issue.Number > 0 {
		attrs["issue_number"] = strconv.Itoa(p.Issue.Number)
	}
	if event == "push" && p.After != "" {
		attrs["head_sha"] = p.After
	}
	if len(attrs) == 0 {
		return nil
	}
	return attrs
}

func githubActorRef(p githubPayload) string {
	if p.Sender != nil {
		return p.Sender.Login
	}
	return ""
}

func (githubAdapter) PresentedSignature(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.Header.Get(githubSignatureHeader))
}

func (githubAdapter) VerifySignature(body []byte, header, secret string) error {
	if strings.TrimSpace(secret) == "" {
		return unverified("webhook secret is not configured for this binding")
	}
	header = strings.TrimSpace(header)
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
