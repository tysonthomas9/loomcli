package prreview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/prreviewer"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	providers "github.com/tysonthomas9/loomcli/internal/infra/connectorsproviders"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// A non-canonical-cased request must canonicalize owner/repo from the
// registered workspace repo BEFORE seeding + dispatch, so the seeded grant's
// (case-sensitive) resource pattern matches the dispatched resource — and a
// later canonical request reuses the same grant without a spurious 403.
func TestGetPullRequestCanonicalizesCasing(t *testing.T) {
	h := newPRReviewHarness(t, true)

	// Mixed-case path; repo is registered as octocat/hello.
	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/OCTOCAT/Hello/7")
	if status != http.StatusOK {
		t.Fatalf("mixed-case status = %d, want 200 (body %s)", status, raw)
	}
	// The dispatch must target the CANONICAL upstream path, not the raw casing.
	if calls := h.github.snapshot(); len(calls) != 1 || calls[0].path != "/repos/octocat/hello/pulls/7" {
		t.Fatalf("upstream calls = %+v, want one canonical /repos/octocat/hello/pulls/7", calls)
	}

	// A subsequent canonical request must still succeed (grant id/pattern did
	// not diverge — the pre-fix bug would 403 here).
	status, raw = h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
	if status != http.StatusOK {
		t.Fatalf("canonical status = %d, want 200 (body %s)", status, raw)
	}
}

func TestReviewerAgentNameSanitizesOwnerAndRepo(t *testing.T) {
	if got := reviewerAgentName("OpenAI.Inc", "Hello.World_repo", 7); got != "review-openai-inc-hello-world-repo-14113012-pr-7" {
		t.Fatalf("reviewerAgentName() = %q, want sanitized name with canonical identity hash", got)
	}
	if got := reviewerAgentName("Octocat", "...///", 7); got != "review-octocat-repo-b990e831-pr-7" {
		t.Fatalf("reviewerAgentName(empty segment) = %q, want fallback segment with identity hash", got)
	}
}

func TestReviewerAgentNameIncludesOwnerIdentity(t *testing.T) {
	orgA := reviewerAgentName("org-a", "api", 7)
	orgB := reviewerAgentName("org-b", "api", 7)
	if orgA == orgB {
		t.Fatalf("reviewer names collide: %q", orgA)
	}
}

func TestReviewerAgentNameSeparatesLossySanitizationCollisions(t *testing.T) {
	dotted := reviewerAgentName("org-a", "api.v2", 7)
	dashed := reviewerAgentName("org-a", "api-v2", 7)
	if dotted == dashed {
		t.Fatalf("reviewer names collide after sanitization: %q", dotted)
	}
	if !strings.Contains(dotted, "-a7549c10-pr-7") || !strings.Contains(dashed, "-6a12be2d-pr-7") {
		t.Fatalf("reviewer names lack canonical identity hashes: %q, %q", dotted, dashed)
	}
}

func TestReviewerAgentNameSeparatesTruncatedPrefixCollisions(t *testing.T) {
	ownerA := strings.Repeat("owner", 30) + "a"
	ownerB := strings.Repeat("owner", 30) + "b"
	repo := strings.Repeat("repository", 20)
	if oldA, oldB := intermediateReviewerAgentName(ownerA, repo, 7), intermediateReviewerAgentName(ownerB, repo, 7); oldA != oldB {
		t.Fatalf("test inputs do not collide in the intermediate shape: %q, %q", oldA, oldB)
	}
	if nameA, nameB := reviewerAgentName(ownerA, repo, 7), reviewerAgentName(ownerB, repo, 7); nameA == nameB {
		t.Fatalf("hashed reviewer names collide after truncation: %q", nameA)
	}
}

func TestReviewerAgentNameTruncatesToStoredNameLimit(t *testing.T) {
	owner := strings.Repeat("owner-", 40)
	repo := strings.Repeat("repo-", 40)
	name := reviewerAgentName(owner, repo, 123)
	if len(name) > reviewerAgentNameMaxLen {
		t.Fatalf("reviewerAgentName length = %d, want <= %d: %q", len(name), reviewerAgentNameMaxLen, name)
	}
	if !agentcoord.ValidStoredAgentName.MatchString(name) {
		t.Fatalf("reviewerAgentName() = %q, want valid stored agent name", name)
	}
	if !strings.HasPrefix(name, "review-") || !strings.HasSuffix(name, "-pr-123") {
		t.Fatalf("reviewerAgentName() = %q, want preserved prefix and suffix", name)
	}
	if !strings.HasSuffix(name, "-"+reviewerIdentityHash(owner, repo)+"-pr-123") {
		t.Fatalf("reviewerAgentName() = %q, want hash segment before PR suffix", name)
	}
	if strings.Contains(name, "--") {
		t.Fatalf("reviewerAgentName() = %q, want segments not ending in dashes", name)
	}
}

func TestLegacyReviewerAgentNameUsesOldRepoOnlyShape(t *testing.T) {
	if got := legacyReviewerAgentName("Hello.World_repo", 7); got != "review-hello-world-repo-pr-7" {
		t.Fatalf("legacyReviewerAgentName() = %q, want review-hello-world-repo-pr-7", got)
	}
	longRepo := strings.Repeat("a", legacyReviewerRepoSegmentMaxLen-1) + ".tail"
	want := "review-" + strings.Repeat("a", legacyReviewerRepoSegmentMaxLen-1) + "-pr-7"
	if got := legacyReviewerAgentName(longRepo, 7); got != want {
		t.Fatalf("legacyReviewerAgentName(truncated) = %q, want %q", got, want)
	}
	if got := intermediateReviewerAgentName("OpenAI.Inc", "Hello.World_repo", 7); got != "review-openai-inc-hello-world-repo-pr-7" {
		t.Fatalf("intermediateReviewerAgentName() = %q, want owner-inclusive unhashed shape", got)
	}
}

func TestPostReviewerMessageQueuesPending(t *testing.T) {
	h := newPRReviewHarness(t, false)
	agentName := createReviewerAgentForTest(t, h, "octocat", "hello", 7)

	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/messages", `{"text":"hello"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	var decoded struct {
		Success bool                  `json:"success"`
		Data    reviewerMessageResult `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, raw)
	}
	if !decoded.Success || decoded.Data.State != "pending" {
		t.Fatalf("response = %+v, want pending delivery", decoded)
	}
	queued := queuedReviewerMessagesForTest(t, h.store, agentName)
	if len(queued) != 1 {
		t.Fatalf("queued messages = %d, want 1", len(queued))
	}
	if queued[0].Body != "hello" || queued[0].SourceKind != "user_chat" {
		t.Fatalf("queued message = %+v, want user_chat hello", queued[0])
	}
	h.messenger.mu.Lock()
	commands := append(
		[]interaction.DeliverChatMessageCommand(nil),
		h.messenger.commands...,
	)
	h.messenger.mu.Unlock()
	if len(commands) != 1 ||
		commands[0].WorkspaceKey != prReviewTestWorkspace ||
		commands[0].AgentID != agentName ||
		commands[0].Body != "hello" ||
		commands[0].SourceKind != "user_chat" ||
		!strings.HasPrefix(commands[0].DedupeKey, "pr-review-msg:") {
		t.Fatalf("Interaction chat commands = %#v", commands)
	}
}

func TestPostReviewerMessageRequiresStartedReviewer(t *testing.T) {
	h := newPRReviewHarness(t, false)
	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/messages", `{"text":"hello"}`)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", status, raw)
	}
	if code := decodeErrorCode(t, raw); code != "reviewer_not_started" {
		t.Fatalf("code = %q, want reviewer_not_started", code)
	}
}

func TestPostReviewerMessageFailsClosedWithoutInteractionMessenger(t *testing.T) {
	h := newPRReviewHarness(t, false)
	agentName := createReviewerAgentForTest(t, h, "octocat", "hello", 7)
	h.module.interactionMessenger = nil

	status, raw := h.post(
		t,
		"/api/workspaces/WS/pull-requests/octocat/hello/7/messages",
		`{"text":"hello"}`,
	)

	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", status, raw)
	}
	if code := decodeErrorCode(t, raw); code != "interaction_unavailable" {
		t.Fatalf("code = %q, want interaction_unavailable", code)
	}
	if queued := queuedReviewerMessagesForTest(t, h.store, agentName); len(queued) != 0 {
		t.Fatalf("queued messages = %d without Interaction messenger", len(queued))
	}
}

func TestPostReviewerMessageRejectsEmptyText(t *testing.T) {
	h := newPRReviewHarness(t, false)
	createReviewerAgentForTest(t, h, "octocat", "hello", 7)
	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/messages", `{"text":"   "}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", status, raw)
	}
	if code := decodeErrorCode(t, raw); code != "invalid" {
		t.Fatalf("code = %q, want invalid", code)
	}
}

func TestPostReviewerMessageUnregisteredRepoDoesNotEnqueue(t *testing.T) {
	h := newPRReviewHarness(t, false)
	createReviewerAgentForTest(t, h, "octocat", "hello", 7)
	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/acme/widgets/7/messages", `{"text":"hello"}`)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", status, raw)
	}
	if code := decodeErrorCode(t, raw); code != "repo_not_registered" {
		t.Fatalf("code = %q, want repo_not_registered", code)
	}
	if queued := queuedReviewerMessagesForTest(t, h.store, reviewerAgentName("octocat", "hello", 7)); len(queued) != 0 {
		t.Fatalf("queued messages = %d, want 0", len(queued))
	}
}

func TestStreamReviewerStartingWhenNoSession(t *testing.T) {
	h := newPRReviewHarness(t, false)
	createReviewerAgentForTest(t, h, "octocat", "hello", 7)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)
	status, raw := h.streamWithContext(t, ctx, "/api/workspaces/WS/pull-requests/octocat/hello/7/stream")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	body := string(raw)
	if !strings.Contains(body, "event: status") || !strings.Contains(body, `"state":"starting"`) {
		t.Fatalf("stream body = %q, want starting status event", body)
	}
}

func TestStreamReviewerEmitsMessages(t *testing.T) {
	h := newPRReviewHarness(t, false)
	agentName := createReviewerAgentForTest(t, h, "octocat", "hello", 7)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.module.streamPollInterval = time.Millisecond
	onRead := cancelAfterReads(2, cancel)
	h.chat.read = func(
		_ context.Context,
		query interaction.ConversationQuery,
	) (*interaction.Conversation, error) {
		if query.WorkspaceKey != prReviewTestWorkspace ||
			query.AgentID != agentName {
			t.Fatalf("conversation query = %#v", query)
		}
		onRead()
		return &interaction.Conversation{
			State: interaction.ConversationIdle,
			Messages: []interaction.ConversationMessage{
				{
					TurnID: "turn-1", ItemID: "item-user",
					Role: "user", Text: "hello",
				},
				{
					TurnID: "turn-1", ItemID: "item-agent",
					Role: "assistant", Text: "hi there", Phase: "final_answer",
				},
			},
		}, nil
	}

	status, raw := h.streamWithContext(t, ctx, "/api/workspaces/WS/pull-requests/octocat/hello/7/stream")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	body := string(raw)
	if strings.Count(body, `"role":"user"`) != 1 || strings.Count(body, `"text":"hello"`) != 1 {
		t.Fatalf("stream body = %q, want one user hello message", body)
	}
	if strings.Count(body, `"role":"assistant"`) != 1 || strings.Count(body, `"text":"hi there"`) != 1 {
		t.Fatalf("stream body = %q, want one assistant hi there message", body)
	}
	if strings.Count(body, "event: message") != 2 {
		t.Fatalf("stream body = %q, want exactly two message events", body)
	}
}

func TestGetReviewerConversationStartingWhenNoSession(t *testing.T) {
	h := newPRReviewHarness(t, false)
	createReviewerAgentForTest(t, h, "octocat", "hello", 7)

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/conversation")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	var decoded struct {
		Data struct {
			State    string           `json:"state"`
			Messages []map[string]any `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode: %v (body %s)", err, raw)
	}
	if decoded.Data.State != "starting" || len(decoded.Data.Messages) != 0 {
		t.Fatalf("conversation = %+v, want starting + no messages", decoded.Data)
	}
}

func TestReviewerConversationUsesCanonicalWorkspaceForAlias(t *testing.T) {
	h := newPRReviewHarness(t, false)
	agentName := createReviewerAgentForTest(t, h, "octocat", "hello", 7)
	h.chat.read = func(
		_ context.Context,
		query interaction.ConversationQuery,
	) (*interaction.Conversation, error) {
		if query.WorkspaceKey != prReviewTestWorkspace || query.AgentID != agentName {
			t.Fatalf("conversation query = %#v", query)
		}
		return &interaction.Conversation{State: interaction.ConversationIdle}, nil
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/workspaces/ALIAS/pull-requests/octocat/hello/7/conversation",
		nil,
	)
	request = request.WithContext(middleware.WithWorkspaceRef(
		request.Context(),
		middleware.WorkspaceRef{
			RequestedID: "ALIAS",
			CanonicalID: prReviewTestWorkspace,
		},
	))
	request = request.WithContext(middleware.WithUserIdentity(
		request.Context(),
		middleware.UserIdentity{UserID: "user-1"},
	))
	response := httptest.NewRecorder()

	h.mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
}

func TestGetReviewerConversationSnapshot(t *testing.T) {
	h := newPRReviewHarness(t, false)
	agentName := createReviewerAgentForTest(t, h, "octocat", "hello", 7)
	h.chat.read = func(
		_ context.Context,
		query interaction.ConversationQuery,
	) (*interaction.Conversation, error) {
		if query.AgentID != agentName {
			t.Fatalf("conversation query = %#v", query)
		}
		return &interaction.Conversation{
			State: interaction.ConversationIdle,
			Messages: []interaction.ConversationMessage{
				{
					TurnID: "turn-1", ItemID: "item-user",
					Role: "user", Text: "hello",
				},
				{
					TurnID: "turn-1", ItemID: "item-agent",
					Role: "assistant", Text: "hi there", Phase: "final_answer",
				},
			},
		}, nil
	}

	status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/conversation")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	var decoded struct {
		Data struct {
			State    string `json:"state"`
			Messages []struct {
				Role string `json:"role"`
				Text string `json:"text"`
			} `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode: %v (body %s)", err, raw)
	}
	if decoded.Data.State != "idle" || len(decoded.Data.Messages) != 2 {
		t.Fatalf("conversation = %+v, want idle + 2 messages", decoded.Data)
	}
	if decoded.Data.Messages[0].Role != "user" || decoded.Data.Messages[0].Text != "hello" {
		t.Fatalf("message[0] = %+v, want user/hello", decoded.Data.Messages[0])
	}
	if decoded.Data.Messages[1].Role != "assistant" || decoded.Data.Messages[1].Text != "hi there" {
		t.Fatalf("message[1] = %+v, want assistant/hi there", decoded.Data.Messages[1])
	}
	h.chatAuthority.mu.Lock()
	actions := append([]authority.Action(nil), h.chatAuthority.actions...)
	h.chatAuthority.mu.Unlock()
	if len(actions) < 2 {
		t.Fatalf("Interaction authority resolutions = %v, want prepare + read", actions)
	}
	for _, action := range actions {
		if action != interaction.ActionReadConversation {
			t.Fatalf("Interaction authority action = %q", action)
		}
	}
}

func TestGetReviewerConversationMapsInteractionAuthorityFailure(t *testing.T) {
	h := newPRReviewHarness(t, false)
	createReviewerAgentForTest(t, h, "octocat", "hello", 7)
	h.chatAuthority.err = workflowcataloghttp.ErrUnauthenticated

	status, raw := h.get(
		t,
		"/api/workspaces/WS/pull-requests/octocat/hello/7/conversation",
	)

	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %s)", status, raw)
	}
	if code := decodeErrorCode(t, raw); code != "unauthenticated" {
		t.Fatalf("code = %q, want unauthenticated", code)
	}
	h.chat.mu.Lock()
	queryCount := len(h.chat.queries)
	h.chat.mu.Unlock()
	if queryCount != 0 {
		t.Fatalf("Interaction conversation reads = %d before authority", queryCount)
	}
}

func TestGetReviewerConversationFailsClosedWithoutInteractionChat(t *testing.T) {
	h := newPRReviewHarness(t, false)
	createReviewerAgentForTest(t, h, "octocat", "hello", 7)
	h.module.interactionChat = nil

	status, raw := h.get(
		t,
		"/api/workspaces/WS/pull-requests/octocat/hello/7/conversation",
	)

	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", status, raw)
	}
	if code := decodeErrorCode(t, raw); code != "interaction_unavailable" {
		t.Fatalf("code = %q, want interaction_unavailable", code)
	}
}

func TestEnsureReviewerCreatesAgentWorktreeAndSeed(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	h := newPRReviewHarness(t, true)
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	workspacePath := filepath.Join(root, "workspace")
	repoPath := filepath.Join(workspacePath, "hello")

	git(t, "", "init", "--bare", remote)
	git(t, "", "init", seed)
	git(t, seed, "checkout", "-b", "main")
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeTestFile(t, filepath.Join(seed, "base.txt"), "base\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "base")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "HEAD:refs/heads/main")

	writeTestFile(t, filepath.Join(seed, "pr.txt"), "pr head\n")
	git(t, seed, "add", "pr.txt")
	git(t, seed, "commit", "-m", "pr head")
	headSHA := gitOutput(t, seed, "rev-parse", "HEAD")
	git(t, seed, "push", "origin", "HEAD:refs/pull/7/head")

	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("mkdir workspace path: %v", err)
	}
	git(t, "", "clone", remote, repoPath)
	git(t, repoPath, "checkout", "main")
	h.rememberLocalPaths(t, workspacePath, "hello", repoPath)
	h.github.setHead(headSHA)

	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/reviewer", `{}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	assertGrantActions(t, h, prReadActions)
	var decoded struct {
		Success bool                 `json:"success"`
		Data    reviewerEnsureResult `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, raw)
	}
	agentName := reviewerAgentName("octocat", "hello", 7)
	if !decoded.Success || decoded.Data.AgentName != agentName || decoded.Data.CheckedOutSHA != headSHA || !decoded.Data.Seeded {
		t.Fatalf("response = %+v, want agent %s sha %s seeded", decoded, agentName, headSHA)
	}
	if got := h.reviewers.ensureCount(); got != 1 {
		t.Fatalf("canonical reviewer ensures = %d, want 1", got)
	}

	agent, err := h.reviewers.GetAgent(context.Background(), prReviewTestWorkspace, agentName)
	if err != nil {
		t.Fatalf("get reviewer agent: %v", err)
	}
	if agent.Kind != agents.AgentKindSupport ||
		agent.Behavior != (agents.BehaviorReference{RoleName: reviewerRoleName}) ||
		agent.DesiredState != agents.DesiredRunning {
		t.Fatalf("canonical reviewer Agent = %+v", agent)
	}
	if _, err := h.store.Roles().Get(
		context.Background(),
		prReviewTestWorkspace,
		reviewerRoleName,
	); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("legacy Role row lookup = %v, want not found", err)
	}
	worktreePath, err := localworkspace.PRReviewWorktreePath(workspacePath, "hello", 7)
	if err != nil {
		t.Fatalf("PRReviewWorktreePath: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		t.Fatalf("worktree .git missing: %v", err)
	}
	if got := gitOutput(t, worktreePath, "rev-parse", "HEAD"); got != headSHA {
		t.Fatalf("worktree HEAD = %s, want %s", got, headSHA)
	}
	// The checkout is self-describing: the base is recorded in per-worktree git
	// config and diffs cleanly against HEAD. No seed message is delivered — the
	// prompt (codex's first turn) drives the review.
	base := strings.TrimSpace(gitOutput(t, worktreePath, "config", "loom.reviewBase"))
	if base == "" {
		t.Fatal("loom.reviewBase not recorded in the review worktree")
	}
	if diff := gitOutput(t, worktreePath, "diff", base+"...HEAD", "--name-only"); !strings.Contains(diff, "pr.txt") {
		t.Fatalf("review diff = %q, want the PR's pr.txt change", diff)
	}
	if queued := queuedReviewerMessagesForTest(t, h.store, agentName); len(queued) != 0 {
		t.Fatalf("queued messages = %d, want 0 (prompt drives the review, no seed)", len(queued))
	}

	// A second ensure is idempotent: still 200, base still recorded.
	status, raw = h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/reviewer", `{}`)
	if status != http.StatusOK {
		t.Fatalf("second status = %d, want 200 (body %s)", status, raw)
	}
	if got := h.reviewers.ensureCount(); got != 2 {
		t.Fatalf("canonical reviewer ensures after replay = %d, want 2", got)
	}
	if got := strings.TrimSpace(gitOutput(t, worktreePath, "config", "loom.reviewBase")); got != base {
		t.Fatalf("loom.reviewBase after second ensure = %q, want %q", got, base)
	}
}

func TestEnsureReviewerFailsClosedBeforeEgressWithoutCanonicalAgents(t *testing.T) {
	st := memstore.New()
	module := NewModule(Config{
		Workspace: buildTestWorkspaceQueries(t, st), LocalSettingsDir: t.TempDir(),
	})
	mux := http.NewServeMux()
	module.Register(mux)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/workspaces/WS/pull-requests/octocat/hello/7/reviewer",
		strings.NewReader(`{}`),
	)
	request = request.WithContext(middleware.WithWorkspace(request.Context(), prReviewTestWorkspace))

	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", recorder.Code, recorder.Body.String())
	}
	if code := decodeErrorCode(t, recorder.Body.Bytes()); code != "agents_unavailable" {
		t.Fatalf("code = %q, want agents_unavailable", code)
	}
}

func TestEnsureReviewerRejectsChangedFetchedTip(t *testing.T) {
	h := newPRReviewHarness(t, true)
	workspacePath := t.TempDir()
	repoPath := filepath.Join(workspacePath, "hello")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo path: %v", err)
	}
	h.rememberLocalPaths(t, workspacePath, "hello", repoPath)
	h.github.setHead("ABC123")
	h.materializer.prepare = func(
		_ context.Context,
		command sourcecontrol.PullRequestCheckoutCommand,
	) (*sourcecontrol.PullRequestCheckout, error) {
		return &sourcecontrol.PullRequestCheckout{
			WorkspaceKey: command.WorkspaceKey, ReviewID: command.ReviewID,
			RepositoryRef: command.RepositoryRef, CheckoutPath: repoPath,
			HeadRef: "refs/loom/pr-reviews/test/head", HeadCommit: command.HeadCommit,
			BaseRef: "refs/loom/pr-reviews/test/base", BaseCommit: strings.Repeat("a", 40),
		}, nil
	}

	checkoutCalled := false
	h.module.checkoutReviewerPRHead = func(_ context.Context, _, _, _ string, headSHA string) (string, error) {
		checkoutCalled = true
		if headSHA != "ABC123" {
			t.Fatalf("checkout head sha = %q, want ABC123", headSHA)
		}
		return "def456", &localworkspace.PRHeadChangedError{
			ExpectedSHA: headSHA,
			TipSHA:      "def456",
		}
	}

	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/reviewer", `{}`)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", status, raw)
	}
	var decoded struct {
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, raw)
	}
	if decoded.Code != "stale_subject" || !decoded.Retryable {
		t.Fatalf("response = %+v, want stale_subject retryable=true", decoded)
	}
	if !checkoutCalled {
		t.Fatal("checkout seam was not called")
	}

	agentName := reviewerAgentName("octocat", "hello", 7)
	if _, err := h.reviewers.GetAgent(context.Background(), prReviewTestWorkspace, agentName); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("reviewer agent lookup error = %v, want ErrNotFound", err)
	}
	cache, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("LoadStateCache: %v", err)
	}
	if _, remembered := cache.Workspaces[prReviewTestWorkspace].Agents[agentName]; remembered {
		t.Fatalf("reviewer worktree was remembered for stale agent %q", agentName)
	}
}

func TestEnsureReviewerUsesSourceControlReceiptWithoutPreexistingLocalCheckout(t *testing.T) {
	h := newPRReviewHarness(t, true)
	workspacePath := t.TempDir()
	repoPath := filepath.Join(workspacePath, "missing-checkout")
	materializeCalled := false
	h.materializer.prepare = func(
		_ context.Context,
		command sourcecontrol.PullRequestCheckoutCommand,
	) (*sourcecontrol.PullRequestCheckout, error) {
		materializeCalled = true
		if command.WorkspaceKey != prReviewTestWorkspace ||
			command.RepositoryRef != "hello" ||
			command.Number != 7 ||
			command.HeadCommit != "headsha-123" ||
			command.BaseBranch != "main" {
			t.Fatalf("materializer command = %#v", command)
		}
		return &sourcecontrol.PullRequestCheckout{
			WorkspaceKey: command.WorkspaceKey, ReviewID: command.ReviewID,
			RepositoryRef: command.RepositoryRef, CheckoutPath: repoPath,
			HeadRef: "refs/loom/pr-reviews/test/head", HeadCommit: command.HeadCommit,
			BaseRef: "refs/loom/pr-reviews/test/base", BaseCommit: strings.Repeat("a", 40),
		}, nil
	}

	checkoutCalled := false
	h.module.checkoutReviewerPRHead = func(
		_ context.Context,
		gotRepoPath, _, headRef, headSHA string,
	) (string, error) {
		checkoutCalled = true
		if gotRepoPath != repoPath ||
			headRef != "refs/loom/pr-reviews/test/head" ||
			headSHA != "headsha-123" {
			t.Fatalf("checkout receipt = repo:%q ref:%q sha:%q", gotRepoPath, headRef, headSHA)
		}
		return headSHA, nil
	}
	recordCalled := false
	h.module.recordReviewerPRContext = func(
		_ context.Context,
		_ string,
		baseCommit string,
		_ map[string]string,
	) (string, error) {
		recordCalled = true
		if baseCommit != strings.Repeat("a", 40) {
			t.Fatalf("base commit = %q", baseCommit)
		}
		return baseCommit, nil
	}
	status, raw := h.post(t, "/api/workspaces/WS/pull-requests/octocat/hello/7/reviewer", `{}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, raw)
	}
	if !materializeCalled || !checkoutCalled || !recordCalled {
		t.Fatalf("calls = materialize:%t checkout:%t record:%t", materializeCalled, checkoutCalled, recordCalled)
	}
	cache, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("LoadStateCache: %v", err)
	}
	if got := cache.Workspaces[prReviewTestWorkspace].Repos["hello"]; got != "" {
		t.Fatalf("handler read or wrote legacy repo checkout %q instead of trusting SourceControl", got)
	}
}

func TestGetPullRequestEgressUnavailable(t *testing.T) {
	t.Run("nil dispatcher", func(t *testing.T) {
		h := newPRReviewHarness(t, false)
		status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
		if status != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body %s)", status, raw)
		}
		if code := decodeErrorCode(t, raw); code != "egress_unavailable" {
			t.Fatalf("code = %q, want egress_unavailable", code)
		}
	})

	t.Run("token unset", func(t *testing.T) {
		h := newPRReviewHarness(t, true)
		t.Setenv(webuiGitHubTokenEnv, "")
		status, raw := h.get(t, "/api/workspaces/WS/pull-requests/octocat/hello/7")
		if status != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body %s)", status, raw)
		}
		if code := decodeErrorCode(t, raw); code != "egress_unavailable" {
			t.Fatalf("code = %q, want egress_unavailable", code)
		}
	})
}

func createReviewerAgentForTest(t *testing.T, h *prReviewHarness, owner, repo string, number int) string {
	t.Helper()
	agentName := reviewerAgentName(owner, repo, number)
	if _, err := h.reviewers.EnsureReviewer(context.Background(), prreviewer.EnsureCommand{
		WorkspaceKey: prReviewTestWorkspace,
		AgentID:      agentName,
	}); err != nil {
		t.Fatalf("Create canonical reviewer Agent: %v", err)
	}
	return agentName
}

func cancelAfterReads(want int, cancel context.CancelFunc) func() {
	count := 0
	return func() {
		count++
		if count >= want {
			cancel()
		}
	}
}

func queuedReviewerMessagesForTest(t *testing.T, st store.Store, agentName string) []*domain.AgentInboxMessage {
	t.Helper()
	items, err := st.AgentInboxMessages().List(context.Background(), prReviewTestWorkspace, store.AgentInboxMessageFilter{
		TargetAgentID: agentName,
		Status:        domain.AgentInboxMessageQueued,
		Limit:         100,
	})
	if err != nil {
		t.Fatalf("List queued inbox messages: %v", err)
	}
	return items
}

type reviewerTestInboxEnqueuer struct {
	store store.Store
}

func (enqueuer reviewerTestInboxEnqueuer) Enqueue(
	ctx context.Context,
	command interaction.EnqueueInboxCommand,
) (*interaction.InboxMessage, error) {
	message, err := enqueuer.store.AgentInboxMessages().Create(ctx, store.AgentInboxMessageCreate{
		WorkspaceKey: command.WorkspaceKey, TargetAgentID: command.TargetAgentID,
		SessionID: command.SessionID, Body: command.Body,
		SourceKind: command.SourceKind, SourceRef: command.SourceRef,
		DriverRunID: command.DriverRunID, TaskRunID: command.TaskRunID,
		TriggerEventID: command.TriggerEventID, TriggerDeliveryID: command.TriggerDeliveryID,
		DedupeKey: command.DedupeKey,
	})
	if err != nil {
		return nil, err
	}
	return &interaction.InboxMessage{
		WorkspaceKey: message.WorkspaceKey, MessageID: message.InboxMessageID,
		Cursor: message.Cursor, TargetAgentID: message.TargetAgentID,
		SessionID: message.SessionID, Body: message.Body,
		Status:     interaction.InboxStatus(message.Status),
		SourceKind: message.SourceKind, SourceRef: message.SourceRef,
		DriverRunID: message.DriverRunID, TaskRunID: message.TaskRunID,
		TriggerEventID: message.TriggerEventID, TriggerDeliveryID: message.TriggerDeliveryID,
		DedupeKey: message.DedupeKey, Attempt: message.Attempt,
		ClaimedBy: message.ClaimedBy, ClaimExpiresAt: message.ClaimExpiresAt,
		ErrorClass: message.ErrorClass, DeliveredThreadID: message.DeliveredThreadID,
		DeliveredAt: message.DeliveredAt, CreatedAt: message.CreatedAt,
		UpdatedAt: message.UpdatedAt,
	}, nil
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // fixed test helper command; args are test-controlled.
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // fixed test helper command; args are test-controlled.
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

func decodeErrorCode(t *testing.T, raw []byte) string {
	t.Helper()
	var decoded struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode error: %v (body %s)", err, raw)
	}
	if decoded.Success {
		t.Fatalf("error response marked success: %s", raw)
	}
	if decoded.Error == "" {
		t.Fatalf("error response missing error: %s", raw)
	}
	return decoded.Code
}

func TestParseGitHubOwnerRepo(t *testing.T) {
	for _, tc := range []struct {
		remote string
		owner  string
		repo   string
		ok     bool
	}{
		{remote: "git@github.com:octocat/hello.git", owner: "octocat", repo: "hello", ok: true},
		{remote: "ssh://git@github.com/octocat/hello.git", owner: "octocat", repo: "hello", ok: true},
		{remote: "ssh://deploy@github.com/octocat/hello", owner: "octocat", repo: "hello", ok: true},
		{remote: "https://github.com/octocat/hello", owner: "octocat", repo: "hello", ok: true},
		{remote: "https://github.com/octocat/hello.git", owner: "octocat", repo: "hello", ok: true},
		{remote: "ssh://git@gitlab.com/octocat/hello.git", ok: false},
		{remote: "https://gitlab.com/octocat/hello.git", ok: false},
	} {
		owner, repo, ok := parseGitHubOwnerRepo(tc.remote)
		if ok != tc.ok || owner != tc.owner || repo != tc.repo {
			t.Fatalf("parseGitHubOwnerRepo(%q) = %q/%q %v, want %q/%q %v",
				tc.remote, owner, repo, ok, tc.owner, tc.repo, tc.ok)
		}
	}
}

func TestReviewSubmissionRunIDUniquePerSubmission(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/pull-requests/octocat/hello/7/review", nil)
	params := pullRequestPath{owner: "octocat", repo: "hello", number: 7}

	first, err := reviewSubmissionRunID(req, params)
	if err != nil {
		t.Fatalf("reviewSubmissionRunID: %v", err)
	}
	second, err := reviewSubmissionRunID(req, params)
	if err != nil {
		t.Fatalf("reviewSubmissionRunID: %v", err)
	}
	if first == second {
		t.Fatalf("run ids identical across submissions: %s", first)
	}
	base := syntheticRunID(req, params, providers.ActionGitHubReviewPost) + ":"
	if !strings.HasPrefix(first, base) || !strings.HasPrefix(second, base) {
		t.Fatalf("run ids %q / %q missing deterministic prefix %q", first, second, base)
	}
}
