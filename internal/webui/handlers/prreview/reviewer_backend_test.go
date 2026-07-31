package prreview

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/agentscompat"
	"github.com/tysonthomas9/loomcli/internal/app/prreviewer"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/agentscompatstore"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// fakeTerminalService implements just the tab-listing/deletion surface legacy
// reviewer retirement touches. The canonical reviewer provisioning workflow
// has no terminal dependency.
type fakeTerminalService struct {
	service.TerminalService

	tabs        []tabmeta.TabMetadata
	listErr     error
	deleted     []string
	deleteErr   error
	listCalled  bool
	deleteCalls int
}

func (fake *fakeTerminalService) ListTabs(
	_ context.Context,
	_ string,
) ([]tabmeta.TabMetadata, error) {
	fake.listCalled = true
	if fake.listErr != nil {
		return nil, fake.listErr
	}
	return append([]tabmeta.TabMetadata(nil), fake.tabs...), nil
}

func (fake *fakeTerminalService) DeleteTab(
	_ context.Context,
	_,
	session string,
) error {
	fake.deleteCalls++
	if fake.deleteErr != nil {
		return fake.deleteErr
	}
	fake.deleted = append(fake.deleted, session)
	return nil
}

type reviewerIdentityTestEnv struct {
	store     store.Store
	module    *Module
	term      *fakeTerminalService
	canonical *reviewerAgentRegistry
}

func newReviewerIdentityTestEnv(t *testing.T) *reviewerIdentityTestEnv {
	t.Helper()
	st := memstore.New()
	if _, err := st.Workspaces().Create(t.Context(), store.WorkspaceCreate{
		Key: prReviewTestWorkspace, Name: "Test",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	term := &fakeTerminalService{}
	canonical := newReviewerAgentRegistry()
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(agents.OperationRules()...)
	if err != nil {
		t.Fatalf("new Agents admission: %v", err)
	}
	compatibilityPersistence, err := agentscompatstore.New(
		st.Roles(),
		st.AgentServices(),
		st.Agents(),
	)
	if err != nil {
		t.Fatal(err)
	}
	compatibility, err := agentscompat.NewAPI(compatibilityPersistence, admission)
	if err != nil {
		t.Fatalf("new Agents compatibility API: %v", err)
	}
	retirements, err := agentscompat.NewManagedRetirements(compatibility, issuer)
	if err != nil {
		t.Fatalf("new Agents compatibility retirement: %v", err)
	}
	return &reviewerIdentityTestEnv{
		store: st,
		module: &Module{
			store: st, terminalSvc: term,
			reviewerProvisioning: canonical,
			reviewerAgents:       canonical,
			managedRetirements:   retirements,
		},
		term:      term,
		canonical: canonical,
	}
}

func (environment *reviewerIdentityTestEnv) seedLegacyReviewer(
	t *testing.T,
	agentName string,
) {
	t.Helper()
	if _, err := environment.store.Roles().Create(t.Context(), store.RoleCreate{
		WorkspaceKey: prReviewTestWorkspace,
		Name:         "lead",
	}); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("create legacy role: %v", err)
	}
	if _, err := environment.store.Agents().Create(t.Context(), store.AgentCreate{
		WorkspaceKey: prReviewTestWorkspace,
		Name:         agentName,
		RoleName:     "lead",
		Backend:      "codex",
		DesiredState: domain.AgentDesiredRunning,
	}); err != nil {
		t.Fatalf("create legacy reviewer agent: %v", err)
	}
}

func TestEnsureReviewerAgentUsesCanonicalAgentsWithoutLegacyWrites(t *testing.T) {
	environment := newReviewerIdentityTestEnv(t)
	const agentID = "review-octocat-hello-abc12345-pr-7"

	if err := environment.module.ensureReviewerAgent(
		t.Context(),
		prReviewTestWorkspace,
		agentID,
	); err != nil {
		t.Fatalf("ensureReviewerAgent: %v", err)
	}
	agent, err := environment.canonical.GetAgent(
		t.Context(),
		prReviewTestWorkspace,
		agentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if agent.Kind != agents.AgentKindSupport ||
		agent.Behavior != (agents.BehaviorReference{RoleName: prreviewer.RoleName}) ||
		agent.DesiredState != agents.DesiredRunning {
		t.Fatalf("canonical Agent = %#v", agent)
	}
	if _, err := environment.store.Agents().Get(
		t.Context(),
		prReviewTestWorkspace,
		agentID,
	); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("legacy Agent lookup = %v, want not found", err)
	}
	if _, err := environment.store.Roles().Get(
		t.Context(),
		prReviewTestWorkspace,
		prreviewer.RoleName,
	); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("legacy Role lookup = %v, want not found", err)
	}
	if environment.term.listCalled || environment.term.deleteCalls != 0 {
		t.Fatal("canonical ensure touched terminal compatibility cleanup")
	}
}

func TestEnsureReviewerAgentFailsClosedWithoutCanonicalCapability(t *testing.T) {
	module := &Module{store: memstore.New()}
	if err := module.ensureReviewerAgent(
		t.Context(),
		prReviewTestWorkspace,
		"reviewer",
	); !errors.Is(err, prreviewer.ErrUnavailable) {
		t.Fatalf("ensureReviewerAgent error = %v, want unavailable", err)
	}
}

func TestEnsureReviewerAgentRetiresBothLegacyNamedAgents(t *testing.T) {
	const owner = "octocat"
	const repo = "hello"
	const number = 7
	environment := newReviewerIdentityTestEnv(t)
	currentName := reviewerAgentName(owner, repo, number)
	legacyNames := []string{
		currentName,
		legacyReviewerAgentName(repo, number),
		intermediateReviewerAgentName(owner, repo, number),
	}
	legacySessionIDs := []string{"legacy-current", "legacy-orch-1", "legacy-orch-2"}
	for index, legacyName := range legacyNames {
		environment.seedLegacyReviewer(t, legacyName)
		if _, err := environment.store.AgentSessions().Create(
			t.Context(),
			store.AgentSessionCreate{
				WorkspaceKey: prReviewTestWorkspace,
				SessionID:    legacySessionIDs[index],
				AgentID:      legacyName,
				Kind:         domain.AgentSessionKindOrchestration,
				Status:       domain.AgentSessionRunning,
				Metadata: map[string]string{
					"lead_runtime_provider":     "codex",
					"codex_app_server_endpoint": "unix:///tmp/legacy.sock",
					"source":                    "web-terminal",
				},
			},
		); err != nil {
			t.Fatalf("create legacy orchestration session: %v", err)
		}
	}
	environment.term.tabs = []tabmeta.TabMetadata{
		{
			SessionName: "agent-current-reviewer",
			Kind:        terminalKindAgent, AgentID: currentName, PTYAlive: true,
		},
		{
			SessionName: "agent-repo-only-reviewer",
			Kind:        terminalKindAgent, AgentID: legacyNames[1], PTYAlive: true,
		},
		{
			SessionName: "agent-unhashed-reviewer",
			Kind:        terminalKindAgent, AgentID: legacyNames[2], PTYAlive: true,
		},
	}

	if err := environment.module.ensureReviewerAgentAndRetireLegacy(
		t.Context(),
		prReviewTestWorkspace,
		currentName,
		owner,
		repo,
		number,
	); err != nil {
		t.Fatalf("ensureReviewerAgentAndRetireLegacy: %v", err)
	}
	if _, err := environment.canonical.GetAgent(
		t.Context(),
		prReviewTestWorkspace,
		currentName,
	); err != nil {
		t.Fatalf("canonical reviewer missing: %v", err)
	}
	for _, legacyName := range legacyNames {
		if _, err := environment.store.Agents().Get(
			t.Context(),
			prReviewTestWorkspace,
			legacyName,
		); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("legacy Agent %q lookup = %v, want not found", legacyName, err)
		}
	}
	deleted := make(map[string]bool, len(environment.term.deleted))
	for _, sessionName := range environment.term.deleted {
		deleted[sessionName] = true
	}
	if len(environment.term.deleted) != 3 ||
		!deleted["agent-current-reviewer"] ||
		!deleted["agent-repo-only-reviewer"] ||
		!deleted["agent-unhashed-reviewer"] {
		t.Fatalf("deleted tabs = %v", environment.term.deleted)
	}
	for index := range legacyNames {
		session, err := environment.store.AgentSessions().Get(
			t.Context(),
			prReviewTestWorkspace,
			legacySessionIDs[index],
		)
		if err != nil {
			t.Fatalf("get legacy session: %v", err)
		}
		if session.Metadata["lead_runtime_provider"] != "codex" {
			t.Fatalf("historical session evidence was rewritten: %v", session.Metadata)
		}
		if _, ok := session.Metadata["source"]; !ok {
			t.Fatalf("historical session source was cleared: %v", session.Metadata)
		}
	}
}

func TestEnsureReviewerAgentWithoutLegacyNameDoesNotTouchTerminals(t *testing.T) {
	environment := newReviewerIdentityTestEnv(t)
	if err := environment.module.ensureReviewerAgentAndRetireLegacy(
		t.Context(),
		prReviewTestWorkspace,
		reviewerAgentName("octocat", "hello", 7),
		"octocat",
		"hello",
		7,
	); err != nil {
		t.Fatalf("ensureReviewerAgentAndRetireLegacy: %v", err)
	}
	if environment.term.listCalled || environment.term.deleteCalls != 0 {
		t.Fatalf(
			"terminal service touched without legacy rows (list=%v deletes=%d)",
			environment.term.listCalled,
			environment.term.deleteCalls,
		)
	}
}

func TestLegacyReviewerRetirementRefusesWhenLiveTabsAreUnknown(t *testing.T) {
	environment := newReviewerIdentityTestEnv(t)
	legacyName := legacyReviewerAgentName("hello", 7)
	environment.seedLegacyReviewer(t, legacyName)
	environment.term.listErr = errors.New("terminal store unavailable")

	err := environment.module.ensureReviewerAgentAndRetireLegacy(
		t.Context(),
		prReviewTestWorkspace,
		reviewerAgentName("octocat", "hello", 7),
		"octocat",
		"hello",
		7,
	)
	if err == nil {
		t.Fatal("retirement succeeded without enumerating live terminals")
	}
	if _, getErr := environment.store.Agents().Get(
		t.Context(),
		prReviewTestWorkspace,
		legacyName,
	); getErr != nil {
		t.Fatalf("legacy Agent was removed after refused retirement: %v", getErr)
	}
}

func TestRetireLegacyReviewerDeduplicatesHistoricalNames(t *testing.T) {
	environment := newReviewerIdentityTestEnv(t)
	if err := environment.module.retireLegacyReviewer(
		context.Background(),
		prReviewTestWorkspace,
		"missing",
		"missing",
	); err != nil {
		t.Fatalf("retireLegacyReviewer: %v", err)
	}
}
