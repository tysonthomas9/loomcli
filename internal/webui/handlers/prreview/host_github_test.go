package prreview

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// recordingProvider is the GitHub provider with every CallSpec it is handed
// recorded.
type recordingProvider struct {
	providers.Provider
	mu    sync.Mutex
	specs []providers.CallSpec
}

func (p *recordingProvider) Call(ctx context.Context, spec providers.CallSpec) (providers.CallResult, error) {
	p.mu.Lock()
	p.specs = append(p.specs, spec)
	p.mu.Unlock()
	return p.Provider.Call(ctx, spec)
}

// hostHarness is readHarness with GET /user answering the login of the
// token it is sent (logins maps token -> login) and the GitHub provider
// recorded.
func hostHarness(t *testing.T, logins map[string]string) (*prReviewHarness, *HostGitHub, *recordingProvider) {
	t.Helper()
	h := readHarness(t)
	var mu sync.Mutex
	h.github.extra["/user"] = func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		login, ok := logins[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		mu.Unlock()
		if !ok {
			writeUpstreamJSON(w, http.StatusUnauthorized, map[string]any{"message": "Bad credentials"})
			return
		}
		writeUpstreamJSON(w, http.StatusOK, map[string]any{"login": login, "id": 1, "type": "User", "node_id": "U", "email": "e@x"})
	}
	rec := &recordingProvider{Provider: providers.NewGitHub(h.github.server.Client(), h.github.server.URL)}
	reg := providers.NewRegistry()
	if err := reg.Register(domain.ConnectorSourceGitHub, rec); err != nil {
		t.Fatal(err)
	}
	h.module.dispatcher.Providers = reg
	return h, NewHostGitHub(h.module), rec
}

func (h *prReviewHarness) userCalls() int {
	n := 0
	for _, c := range h.github.snapshot() {
		if c.path == "/user" {
			n++
		}
	}
	return n
}

// TestGitHubViewerViaDispatcher: the host's GitHub viewer resolves through
// the host connector's dispatcher with no agent and no bridge, bound to a
// registered repo; the provider gets the credential only in CallSpec, the
// call is audited, and the login is cached.
func TestGitHubViewerViaDispatcher(t *testing.T) {
	h, host, rec := hostHarness(t, map[string]string{prReviewTestToken: "loom-host"})
	ctx := context.Background()
	login, err := host.Viewer(ctx, prReviewTestWorkspace, "octocat", "hello")
	if err != nil || login != "loom-host" {
		t.Fatalf("Viewer = %q, %v; want loom-host", login, err)
	}
	if len(rec.specs) != 1 {
		t.Fatalf("provider calls = %d; want 1", len(rec.specs))
	}
	spec := rec.specs[0]
	if spec.Action != providers.ActionGitHubRead || spec.Args["op"] != "viewer" || spec.Resource != "repo:octocat/hello" {
		t.Fatalf("spec = %+v; want github.read viewer on repo:octocat/hello", spec)
	}
	if spec.Credential != prReviewTestToken {
		t.Fatalf("CallSpec.Credential = %q; want the host token", spec.Credential)
	}
	for k, v := range spec.Args {
		if s, _ := v.(string); strings.Contains(s, prReviewTestToken) {
			t.Fatalf("args.%s carries the credential", k)
		}
	}
	calls, err := h.store.ConnectorCalls().ListByBinding(ctx, prReviewTestWorkspace, bindingID, store.ConnectorCallFilter{})
	if err != nil || len(calls) != 1 || calls[0].Decision != domain.ConnectorCallGranted || calls[0].CallID != spec.IdempotencyKey ||
		!strings.HasPrefix(calls[0].RunID, "host-github-read:") {
		t.Fatalf("audit = %+v, %v; want one granted host-github-read call", calls, err)
	}
	if login, err := host.Viewer(ctx, prReviewTestWorkspace, "octocat", "hello"); err != nil || login != "loom-host" || h.userCalls() != 1 {
		t.Fatalf("cached Viewer = %q, %v after %d /user calls; want loom-host from the cache", login, err, h.userCalls())
	}
	if _, err := host.Viewer(ctx, prReviewTestWorkspace, "octocat", "other"); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("Viewer on an unregistered repo = %v; want ErrNotOwner", err)
	}
	if _, err := host.Read(ctx, prReviewTestWorkspace, "someone", "else", "pr_view", map[string]any{"number": 8}); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("Read on an unregistered repo = %v; want ErrNotOwner", err)
	}
	if body, err := host.Read(ctx, prReviewTestWorkspace, "octocat", "hello", "pr_view", map[string]any{"number": 8}); err != nil || dig(body, "item.head.sha") != "headsha-8" {
		t.Fatalf("Read pr_view = %v, %v", body, err)
	}
}

// TestGitHubViewerCredentialRotation: when the host credential changes to
// another user's, the cached viewer is dropped and the next call returns the
// new login; a read answered 401 drops it too.
func TestGitHubViewerCredentialRotation(t *testing.T) {
	const bobToken = "ghp-bob-token"
	logins := map[string]string{prReviewTestToken: "alice", bobToken: "bob"}
	h, host, rec := hostHarness(t, logins)
	ctx := context.Background()
	if login, err := host.Viewer(ctx, prReviewTestWorkspace, "octocat", "hello"); err != nil || login != "alice" {
		t.Fatalf("Viewer = %q, %v; want alice", login, err)
	}
	t.Setenv(webuiGitHubTokenEnv, bobToken)
	if login, err := host.Viewer(ctx, prReviewTestWorkspace, "octocat", "hello"); err != nil || login != "bob" {
		t.Fatalf("Viewer after rotation = %q, %v; want bob", login, err)
	}
	if last := rec.specs[len(rec.specs)-1]; last.Credential != bobToken {
		t.Fatalf("rotated viewer read with credential %q; want bob's", last.Credential)
	}
	if login, err := host.Viewer(ctx, prReviewTestWorkspace, "octocat", "hello"); err != nil || login != "bob" || h.userCalls() != 2 {
		t.Fatalf("Viewer = %q, %v after %d /user calls; want bob from the cache", login, err, h.userCalls())
	}

	// GitHub revokes bob's token: the 401 drops the cached viewer, so the
	// next viewer call asks GitHub again (and fails) rather than answering bob.
	delete(logins, bobToken)
	h.github.extra["/repos/octocat/hello/pulls/8"] = func(w http.ResponseWriter, _ *http.Request) {
		writeUpstreamJSON(w, http.StatusUnauthorized, map[string]any{"message": "Bad credentials"})
	}
	if _, err := host.Read(ctx, prReviewTestWorkspace, "octocat", "hello", "pr_view", map[string]any{"number": 8}); err == nil {
		t.Fatal("Read with a revoked token succeeded")
	}
	if login, err := host.Viewer(ctx, prReviewTestWorkspace, "octocat", "hello"); err == nil || h.userCalls() != 3 {
		t.Fatalf("Viewer after a 401 = %q, %v after %d /user calls; want a fresh failing /user call", login, err, h.userCalls())
	}
}

// The host reader refuses with the clear no-token error when no GitHub
// credential is configured, so a registration fails clearly.
func TestGitHubViewerNoCredential(t *testing.T) {
	_, host, _ := hostHarness(t, nil)
	t.Setenv(webuiGitHubTokenEnv, "")
	if _, err := host.Viewer(context.Background(), prReviewTestWorkspace, "octocat", "hello"); !errors.Is(err, errNoGitHubToken) {
		t.Fatalf("Viewer with no token = %v; want errNoGitHubToken", err)
	}
}
