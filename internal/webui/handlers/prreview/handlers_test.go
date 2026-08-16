package prreview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/connectorsproviders"
	"github.com/tysonthomas9/loomcli/internal/infra/connectorsvault"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/localsettings"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
	localsettingshandler "github.com/tysonthomas9/loomcli/internal/webui/handlers/localsettings"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

const (
	prReviewTestWorkspace = "WS"
	prReviewTestToken     = "ghp-webui-prreview-token"
)

type fakeGitHubCall struct {
	method        string
	path          string
	query         string
	authorization string
	body          map[string]any
}

type fakeGitHub struct {
	mu    sync.Mutex
	calls []fakeGitHubCall

	headSha string
	state   string
	lists   map[string]fakePullList

	server *httptest.Server
}

type fakePullList struct {
	status int
	pulls  []map[string]any
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{headSha: "headsha-123", state: "open", lists: make(map[string]fakePullList)}
	g.server = httptest.NewServer(http.HandlerFunc(g.handle))
	t.Cleanup(g.server.Close)
	return g
}

func (g *fakeGitHub) handle(w http.ResponseWriter, r *http.Request) {
	bodyBytes, _ := io.ReadAll(r.Body)
	var body map[string]any
	if len(bytes.TrimSpace(bodyBytes)) > 0 {
		_ = json.Unmarshal(bodyBytes, &body)
	}
	g.mu.Lock()
	g.calls = append(g.calls, fakeGitHubCall{
		method:        r.Method,
		path:          r.URL.Path,
		query:         r.URL.RawQuery,
		authorization: r.Header.Get("Authorization"),
		body:          body,
	})
	headSha := g.headSha
	state := g.state
	list, hasList := g.lists[r.URL.Path+"?page="+r.URL.Query().Get("page")]
	if !hasList {
		list, hasList = g.lists[r.URL.Path]
	}
	g.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && hasList:
		if list.status == 0 {
			list.status = http.StatusOK
		}
		if list.status != http.StatusOK {
			writeUpstreamJSON(w, list.status, map[string]any{"message": "list failed"})
			return
		}
		writeUpstreamJSON(w, http.StatusOK, list.pulls)
	case r.Method == http.MethodGet && r.URL.Path == "/repos/octocat/hello/pulls/7":
		writeUpstreamJSON(w, http.StatusOK, map[string]any{
			"number": 7,
			"state":  state,
			"title":  "Add review API",
			"draft":  false,
			"merged": false,
			"head":   map[string]any{"sha": headSha, "ref": "feature/review-api"},
			"base":   map[string]any{"sha": "basesha-123", "ref": "main"},
		})
	case r.Method == http.MethodGet && r.URL.Path == "/repos/octocat/hello/compare/main...headsha-123":
		writeUpstreamJSON(w, http.StatusOK, map[string]any{
			"status":        "ahead",
			"ahead_by":      2,
			"behind_by":     0,
			"total_commits": 2,
			"files": []map[string]any{
				{
					"filename":  "README.md",
					"status":    "modified",
					"additions": 3,
					"deletions": 1,
					"patch":     "@@ -1 +1 @@\n-old\n+new",
				},
			},
		})
	case r.Method == http.MethodPost && r.URL.Path == "/repos/octocat/hello/pulls/7/reviews":
		writeUpstreamJSON(w, http.StatusCreated, map[string]any{
			"id":       101,
			"state":    "APPROVED",
			"html_url": "https://github.com/octocat/hello/pull/7#pullrequestreview-101",
		})
	default:
		writeUpstreamJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
	}
}

func (g *fakeGitHub) setHead(headSha string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.headSha = headSha
}

func (g *fakeGitHub) setListPayload(owner, repo string, pulls []map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lists["/repos/"+owner+"/"+repo+"/pulls"] = fakePullList{status: http.StatusOK, pulls: pulls}
}

func (g *fakeGitHub) setListPage(owner, repo string, page int, pulls []map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := "/repos/" + owner + "/" + repo + "/pulls?page=" + strconv.Itoa(page)
	g.lists[key] = fakePullList{status: http.StatusOK, pulls: pulls}
}

func (g *fakeGitHub) setListStatus(owner, repo string, status int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lists["/repos/"+owner+"/"+repo+"/pulls"] = fakePullList{status: status}
}

func fakePullRequestPage(first, count int) []map[string]any {
	pulls := make([]map[string]any, 0, count)
	for number := first; number < first+count; number++ {
		pulls = append(pulls, map[string]any{
			"number": number,
			"state":  "open",
			"title":  "PR " + strconv.Itoa(number),
			"head":   map[string]any{"sha": "head", "ref": "feature"},
			"base":   map[string]any{"sha": "base", "ref": "main"},
		})
	}
	return pulls
}

func (g *fakeGitHub) snapshot() []fakeGitHubCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]fakeGitHubCall(nil), g.calls...)
}

func writeUpstreamJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decodePullRequestsResponse(t *testing.T, raw []byte) loomapi.PullRequestsData {
	t.Helper()
	var decoded loomapi.PullRequestsResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode pull request response: %v (body %s)", err, raw)
	}
	if !decoded.Success {
		t.Fatalf("pull request response was not successful: %s", raw)
	}
	return decoded.Data
}

type prReviewHarness struct {
	store         *memstore.Store
	github        *fakeGitHub
	mux           *http.ServeMux
	module        *Module
	dataDir       string
	reviewers     *reviewerAgentRegistry
	runtime       *reviewerTestRuntime
	materializer  *reviewerTestMaterializer
	chat          *reviewerTestChatAPI
	messenger     *reviewerTestChatMessenger
	chatAuthority *reviewerTestOperatorAuthority
}

type reviewerTestRuntime struct {
	mu    sync.Mutex
	calls [][2]string
	err   error
}

func (runtime *reviewerTestRuntime) StopReviewerSession(_ context.Context, workspace, agentID string) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.calls = append(runtime.calls, [2]string{workspace, agentID})
	return runtime.err
}

func (runtime *reviewerTestRuntime) stopCalls() [][2]string {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return append([][2]string(nil), runtime.calls...)
}

type reviewerTestMaterializer struct {
	prepare func(context.Context, sourcecontrol.PullRequestCheckoutCommand) (*sourcecontrol.PullRequestCheckout, error)
}

func (*reviewerTestMaterializer) PrepareTaskCheckout(
	context.Context,
	sourcecontrol.TaskCheckoutCommand,
) (*sourcecontrol.TaskCheckout, error) {
	return nil, sourcecontrol.ErrUnavailable
}

func (materializer *reviewerTestMaterializer) PreparePullRequestCheckout(
	ctx context.Context,
	command sourcecontrol.PullRequestCheckoutCommand,
) (*sourcecontrol.PullRequestCheckout, error) {
	if materializer == nil || materializer.prepare == nil {
		return nil, sourcecontrol.ErrUnavailable
	}
	return materializer.prepare(ctx, command)
}

func newReviewerTestMaterializer() *reviewerTestMaterializer {
	return &reviewerTestMaterializer{prepare: func(
		ctx context.Context,
		command sourcecontrol.PullRequestCheckoutCommand,
	) (*sourcecontrol.PullRequestCheckout, error) {
		cache, err := bootstrap.LoadStateCache()
		if err != nil {
			return nil, err
		}
		local, ok := cache.Workspaces[command.WorkspaceKey]
		if !ok {
			return nil, sourcecontrol.ErrInvalidMaterialization
		}
		repoPath := strings.TrimSpace(local.Repos[command.RepositoryRef])
		if repoPath == "" {
			for _, candidate := range local.Repos {
				if strings.TrimSpace(candidate) != "" {
					repoPath = strings.TrimSpace(candidate)
					break
				}
			}
		}
		if repoPath == "" {
			return nil, sourcecontrol.ErrInvalidMaterialization
		}
		sum := sha256.Sum256([]byte(command.ReviewID + "\x00" + command.HeadCommit))
		coordinate := hex.EncodeToString(sum[:8])
		headRef := "refs/loom/pr-reviews/" + coordinate + "/head"
		baseRef := "refs/loom/pr-reviews/" + coordinate + "/base"
		if err := localworkspace.FetchGitRefAnonymous(
			ctx,
			repoPath,
			"origin",
			"refs/pull/"+strconv.Itoa(command.Number)+"/head",
			headRef,
		); err != nil {
			return nil, err
		}
		if err := localworkspace.FetchGitRefAnonymous(
			ctx,
			repoPath,
			"origin",
			"refs/heads/"+command.BaseBranch,
			baseRef,
		); err != nil {
			return nil, err
		}
		head, err := reviewerTestResolveCommit(ctx, repoPath, headRef)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(head, command.HeadCommit) {
			return nil, &sourcecontrol.RefChangedError{
				ExpectedCommit: command.HeadCommit,
				FetchedCommit:  head,
			}
		}
		base, err := reviewerTestResolveCommit(ctx, repoPath, baseRef)
		if err != nil {
			return nil, err
		}
		return &sourcecontrol.PullRequestCheckout{
			WorkspaceKey: command.WorkspaceKey, ReviewID: command.ReviewID,
			RepositoryRef: command.RepositoryRef, CheckoutPath: repoPath,
			HeadRef: headRef, HeadCommit: head, BaseRef: baseRef, BaseCommit: base,
		}, nil
	}}
}

func reviewerTestResolveCommit(ctx context.Context, repoPath, ref string) (string, error) {
	command := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--verify", ref+"^{commit}") //nolint:norawexec,gosec // test helper.
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve test commit: %w: %s", err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

type reviewerTestChatAPI struct {
	mu      sync.Mutex
	queries []interaction.ConversationQuery
	read    func(context.Context, interaction.ConversationQuery) (*interaction.Conversation, error)
}

func (chat *reviewerTestChatAPI) DeliverChatMessage(
	context.Context,
	authority.OperatorAuthority,
	interaction.DeliverChatMessageCommand,
) (*interaction.ChatDelivery, error) {
	return nil, interaction.ErrUnavailable
}

func (chat *reviewerTestChatAPI) DeliverAssignment(
	context.Context,
	authority.OperatorAuthority,
	interaction.DeliverAssignmentCommand,
) (*interaction.ChatDelivery, error) {
	return nil, interaction.ErrUnavailable
}

func (chat *reviewerTestChatAPI) ReadConversation(
	ctx context.Context,
	_ authority.OperatorAuthority,
	query interaction.ConversationQuery,
) (*interaction.Conversation, error) {
	chat.mu.Lock()
	chat.queries = append(chat.queries, query)
	read := chat.read
	chat.mu.Unlock()
	if read != nil {
		return read(ctx, query)
	}
	return &interaction.Conversation{
		State:    interaction.ConversationStarting,
		Messages: []interaction.ConversationMessage{},
	}, nil
}

type reviewerTestChatMessenger struct {
	mu       sync.Mutex
	inbox    reviewerTestInboxEnqueuer
	commands []interaction.DeliverChatMessageCommand
	err      error
}

func (messenger *reviewerTestChatMessenger) DeliverChatMessage(
	ctx context.Context,
	command interaction.DeliverChatMessageCommand,
) (*interaction.ChatDelivery, error) {
	messenger.mu.Lock()
	messenger.commands = append(messenger.commands, command)
	err := messenger.err
	messenger.mu.Unlock()
	if err != nil {
		return nil, err
	}
	message, err := messenger.inbox.Enqueue(ctx, interaction.EnqueueInboxCommand{
		WorkspaceKey:  command.WorkspaceKey,
		TargetAgentID: command.AgentID,
		Body:          command.Body, SourceKind: command.SourceKind,
		SourceRef: command.SourceRef, DriverRunID: command.DriverRunID,
		TaskRunID: command.TaskRunID, TriggerEventID: command.TriggerEventID,
		TriggerDeliveryID: command.TriggerDeliveryID, DedupeKey: command.DedupeKey,
	})
	if err != nil {
		return nil, err
	}
	return &interaction.ChatDelivery{
		State:          interaction.ChatDeliveryPending,
		InboxMessageID: message.MessageID,
	}, nil
}

func (*reviewerTestChatMessenger) DeliverAssignment(
	context.Context,
	interaction.DeliverAssignmentCommand,
) (*interaction.ChatDelivery, error) {
	return nil, interaction.ErrUnavailable
}

type reviewerTestOperatorAuthority struct {
	mu      sync.Mutex
	actions []authority.Action
	err     error
}

func (resolver *reviewerTestOperatorAuthority) ResolveOperatorAuthority(
	_ *http.Request,
	workspace string,
	action authority.Action,
) (authority.OperatorAuthority, error) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	resolver.actions = append(resolver.actions, action)
	if resolver.err != nil {
		return authority.OperatorAuthority{}, resolver.err
	}
	if workspace != prReviewTestWorkspace {
		return authority.OperatorAuthority{}, authority.ErrWorkspaceMismatch
	}
	return authority.OperatorAuthority{}, nil
}

type testCredentialSource string

const (
	testCredentialEnv      testCredentialSource = "env"
	testCredentialSettings testCredentialSource = "settings"
	testCredentialNone     testCredentialSource = "none"
)

type fallbackPullRequestLister struct {
	called bool
	ws     string
	state  string
	result *sourcecontrol.PullRequestList
	err    error
}

// reviewerAgentRegistry is the handler test's purpose-scoped Agents seam.
type reviewerAgentRegistry struct {
	mu       sync.Mutex
	agents   map[string]*agents.Agent
	commands []agents.ManagedReviewerCommand
	err      error
}

func newReviewerAgentRegistry() *reviewerAgentRegistry {
	return &reviewerAgentRegistry{agents: make(map[string]*agents.Agent)}
}

func (registry *reviewerAgentRegistry) ConvergeReviewerIdentity(
	_ context.Context,
	command agents.ManagedReviewerCommand,
) (*agents.ManagedReviewerResult, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.commands = append(registry.commands, command)
	if registry.err != nil {
		return nil, registry.err
	}
	key := command.WorkspaceKey + "\x00" + command.AgentID
	agent := registry.agents[key]
	if agent == nil {
		agent = &agents.Agent{
			WorkspaceKey: command.WorkspaceKey,
			AgentID:      command.AgentID,
			Name:         command.AgentID,
			Kind:         agents.AgentKindSupport,
			Behavior:     agents.BehaviorReference{RoleName: reviewerRoleName},
			DesiredState: agents.DesiredRunning,
			MaxInstances: 1,
			CreatedAt:    time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		registry.agents[key] = agent
	}
	if command.DesiredState == agents.ManagedReviewerArchived && agent.DeletedAt == nil {
		deletedAt := time.Now().UTC()
		agent.DeletedAt = &deletedAt
		agent.UpdatedAt = deletedAt
	}
	if command.DesiredState == agents.ManagedReviewerActive {
		agent.DeletedAt = nil
	}
	return &agents.ManagedReviewerResult{
		PresetID: command.Preset.PresetID, PresetRevision: command.Preset.Revision,
		Role:  &agents.Role{WorkspaceKey: command.WorkspaceKey, Name: reviewerRoleName},
		Agent: cloneReviewerAgent(agent), Changed: true,
	}, nil
}

func (registry *reviewerAgentRegistry) GetAgent(
	_ context.Context,
	workspace,
	agentID string,
) (*agents.Agent, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	agent := registry.agents[workspace+"\x00"+agentID]
	if agent == nil {
		return nil, agents.ErrNotFound
	}
	return cloneReviewerAgent(agent), nil
}

func (registry *reviewerAgentRegistry) ListAgents(
	_ context.Context,
	workspace string,
	filter agents.AgentFilter,
) ([]*agents.Agent, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	var out []*agents.Agent
	for key, agent := range registry.agents {
		if strings.HasPrefix(key, workspace+"\x00") && (filter.IncludeDeleted || agent.DeletedAt == nil) {
			out = append(out, cloneReviewerAgent(agent))
		}
	}
	return out, nil
}

func (registry *reviewerAgentRegistry) ensureCount() int {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	count := 0
	for _, command := range registry.commands {
		if command.DesiredState == agents.ManagedReviewerActive {
			count++
		}
	}
	return count
}

func cloneReviewerAgent(agent *agents.Agent) *agents.Agent {
	if agent == nil {
		return nil
	}
	out := *agent
	if agent.Metadata != nil {
		out.Metadata = make(map[string]string, len(agent.Metadata))
		for key, value := range agent.Metadata {
			out.Metadata[key] = value
		}
	}
	if agent.DeletedAt != nil {
		deletedAt := *agent.DeletedAt
		out.DeletedAt = &deletedAt
	}
	return &out
}

func (s *fallbackPullRequestLister) ListPullRequests(_ context.Context, query sourcecontrol.ListPullRequestsQuery) (*sourcecontrol.PullRequestList, error) {
	s.called = true
	s.ws = query.WorkspaceKey
	s.state = query.State
	return s.result, s.err
}

func newPRReviewHarness(t *testing.T, withDispatcher bool) *prReviewHarness {
	return newPRReviewHarnessWithPullRequests(t, withDispatcher, nil)
}

func newPRReviewHarnessWithPullRequests(t *testing.T, withDispatcher bool, pullRequests PullRequestLister) *prReviewHarness {
	return newPRReviewHarnessWithCredential(t, withDispatcher, pullRequests, testCredentialEnv, prReviewTestToken)
}

func newPRReviewHarnessWithCredential(
	t *testing.T,
	withDispatcher bool,
	pullRequests PullRequestLister,
	source testCredentialSource,
	token string,
) *prReviewHarness {
	t.Helper()
	h := &prReviewHarness{
		store:         memstore.New(),
		github:        newFakeGitHub(t),
		mux:           http.NewServeMux(),
		dataDir:       t.TempDir(),
		reviewers:     newReviewerAgentRegistry(),
		runtime:       &reviewerTestRuntime{},
		materializer:  newReviewerTestMaterializer(),
		chat:          &reviewerTestChatAPI{},
		chatAuthority: &reviewerTestOperatorAuthority{},
	}
	h.messenger = &reviewerTestChatMessenger{
		inbox: reviewerTestInboxEnqueuer{store: h.store},
	}
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv(connectorsproviders.GitHubBaseURLEnvVar, h.github.server.URL)
	switch source {
	case testCredentialEnv:
		t.Setenv(connectorsvault.VaultKeyEnvVar, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32)))
		t.Setenv(webuiGitHubTokenEnv, token)
	case testCredentialSettings:
		t.Setenv(connectorsvault.VaultKeyEnvVar, "")
		t.Setenv(webuiGitHubTokenEnv, "")
		h.setSettingsGitHubToken(t, token)
	case testCredentialNone:
		t.Setenv(connectorsvault.VaultKeyEnvVar, "")
		t.Setenv(webuiGitHubTokenEnv, "")
	default:
		t.Fatalf("unknown test credential source %q", source)
	}
	h.seedWorkspace(t)

	dispatcher, connectorManagement, connectorSealer := buildTestConnectorCapabilities(t, h.store, h.dataDir)
	if !withDispatcher {
		dispatcher = nil
	}
	h.module = NewModule(Config{
		Workspace: buildTestWorkspaceQueries(t, h.store), ConnectorManagement: connectorManagement, ConnectorSealer: connectorSealer,
		Dispatcher: dispatcher, PullRequests: pullRequests, LocalSettingsDir: h.dataDir,
		ReviewerIdentities: h.reviewers, ReviewerAgents: h.reviewers, SourceControl: h.materializer,
		ReviewerRuntime: h.runtime,
		InteractionChat: h.chat, InteractionMessenger: h.messenger, InteractionAuthority: h.chatAuthority,
	})
	h.module.Register(h.mux)
	return h
}

func (h *prReviewHarness) setSettingsGitHubToken(t *testing.T, token string) {
	t.Helper()
	settings, err := localsettings.Load(h.dataDir)
	if err != nil {
		t.Fatalf("Load local settings: %v", err)
	}
	credential, err := localsettings.SealRuntimeCredential(
		h.dataDir,
		localsettings.RuntimeCredentialProviderGitHub,
		token,
		time.Now(),
	)
	if err != nil {
		t.Fatalf("SealRuntimeCredential: %v", err)
	}
	settings.RuntimeCredentials.GitHub = credential
	if err := localsettings.Save(h.dataDir, settings); err != nil {
		t.Fatalf("Save local settings: %v", err)
	}
}

func (h *prReviewHarness) rebuildWithDataDir(t *testing.T, dataDir string) {
	t.Helper()
	dispatcher, connectorManagement, connectorSealer := buildTestConnectorCapabilities(t, h.store, dataDir)
	h.dataDir = dataDir
	h.module = NewModule(Config{
		Workspace: buildTestWorkspaceQueries(t, h.store), ConnectorManagement: connectorManagement, ConnectorSealer: connectorSealer,
		Dispatcher: dispatcher, LocalSettingsDir: dataDir,
		ReviewerIdentities: h.reviewers, ReviewerAgents: h.reviewers, SourceControl: h.materializer,
		ReviewerRuntime: h.runtime,
		InteractionChat: h.chat, InteractionMessenger: h.messenger, InteractionAuthority: h.chatAuthority,
	})
	h.mux = http.NewServeMux()
	h.module.Register(h.mux)
}

func buildTestConnectorCapabilities(
	t *testing.T,
	st *memstore.Store,
	dataDir string,
) (connectors.Dispatcher, connectors.Management, connectors.CredentialSealer) {
	t.Helper()
	vault, err := connectorsvault.NewVaultFromEnvOrKeyFile(dataDir)
	if err != nil {
		t.Fatalf("NewVaultFromEnvOrKeyFile: %v", err)
	}
	dispatcher, err := connectors.NewDispatch(
		st.Connectors(),
		vault,
		connectorsproviders.Default(&http.Client{Timeout: 10 * time.Second}),
		nil,
	)
	if err != nil {
		t.Fatalf("new connector dispatcher: %v", err)
	}
	vaultAdapter, err := connectorsvault.New(vault)
	if err != nil {
		t.Fatalf("new connector credential vault: %v", err)
	}
	management, err := connectors.NewManagementWithCredentialVault(st.Connectors(), vaultAdapter, time.Now)
	if err != nil {
		t.Fatalf("new connector management: %v", err)
	}
	return dispatcher, management, vault
}

func buildTestWorkspaceQueries(t *testing.T, st *memstore.Store) WorkspaceQueries {
	t.Helper()
	queries, err := workspacemodule.NewFromRecordStores(st.Workspaces(), st.Repos())
	if err != nil {
		t.Fatalf("new workspace catalog: %v", err)
	}
	return queries
}

func (h *prReviewHarness) seedWorkspace(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.store.Workspaces().Create(ctx, workspacemodule.WorkspaceCreate{
		Key:  prReviewTestWorkspace,
		Name: "Test Workspace",
	}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	if _, err := h.store.Repos().Create(ctx, workspacemodule.RepoCreate{
		WorkspaceKey: prReviewTestWorkspace,
		Name:         "hello",
		RemoteURL:    "https://github.com/octocat/hello",
	}); err != nil {
		t.Fatalf("Create repo: %v", err)
	}
}

func (h *prReviewHarness) addRepo(t *testing.T, name, remoteURL string) {
	t.Helper()
	if _, err := h.store.Repos().Create(context.Background(), workspacemodule.RepoCreate{
		WorkspaceKey: prReviewTestWorkspace,
		Name:         name,
		RemoteURL:    remoteURL,
	}); err != nil {
		t.Fatalf("Create repo %s: %v", name, err)
	}
}

func (h *prReviewHarness) updateRepoRemote(t *testing.T, name, remoteURL string) {
	t.Helper()
	if _, err := h.store.Repos().Update(context.Background(), prReviewTestWorkspace, name, workspacemodule.RepoUpdate{
		RemoteURL: &remoteURL,
	}); err != nil {
		t.Fatalf("Update repo %s remote URL: %v", name, err)
	}
}

func (h *prReviewHarness) rememberLocalPaths(t *testing.T, workspacePath, repoName, repoPath string) {
	t.Helper()
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		if sc.Workspaces == nil {
			sc.Workspaces = map[string]bootstrap.WorkspaceLocalState{}
		}
		sc.Workspaces[prReviewTestWorkspace] = bootstrap.WorkspaceLocalState{
			Path:  workspacePath,
			Repos: map[string]string{repoName: repoPath},
		}
		return nil
	}); err != nil {
		t.Fatalf("MutateStateCache: %v", err)
	}
}

func (h *prReviewHarness) get(t *testing.T, path string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), prReviewTestWorkspace))
	req = req.WithContext(middleware.WithUserIdentity(req.Context(), middleware.UserIdentity{UserID: "user-1"}))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func (h *prReviewHarness) streamWithContext(t *testing.T, ctx context.Context, path string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), prReviewTestWorkspace))
	req = req.WithContext(middleware.WithUserIdentity(req.Context(), middleware.UserIdentity{UserID: "user-1"}))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func (h *prReviewHarness) post(t *testing.T, path, body string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req = req.WithContext(middleware.WithWorkspace(req.Context(), prReviewTestWorkspace))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.WithUserIdentity(req.Context(), middleware.UserIdentity{UserID: "user-1"}))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func (h *prReviewHarness) delete(t *testing.T, path string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), prReviewTestWorkspace))
	req = req.WithContext(middleware.WithUserIdentity(req.Context(), middleware.UserIdentity{UserID: "user-1"}))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func (h *prReviewHarness) patchLocalSettings(t *testing.T, body string, onGitHubCredentialChanged func()) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/local/settings", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	localsettingshandler.HandlePatch(h.dataDir, localsettingshandler.PatchOptions{
		OnGitHubRuntimeCredentialChanged: onGitHubCredentialChanged,
	}).ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func assertGrantActions(t *testing.T, h *prReviewHarness, want []string) {
	t.Helper()
	grants, err := h.store.Connectors().ListGrantRecordsByBinding(context.Background(), prReviewTestWorkspace, bindingID)
	if err != nil {
		t.Fatalf("ListByBinding: %v", err)
	}
	got := make([]string, 0, len(grants))
	for _, grant := range grants {
		got = append(got, grant.Action)
	}
	slices.Sort(got)
	want = slices.Clone(want)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("grant actions = %v, want %v", got, want)
	}
}
