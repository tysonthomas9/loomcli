package placement

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/leadtoken"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const testDeploymentID = "test-deployment"

var testTokenKey = []byte("01234567890123456789012345678901")

func TestProvisionGetOrCreateConcurrent(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{createDelay: 10 * time.Millisecond}
	broker := mustBroker(t, st, provider)

	results := make(chan *ProvisionResult, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			result, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4))
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}()
	}

	first := receiveResult(t, results, errs)
	second := receiveResult(t, results, errs)
	if first.Node.NodeID != second.Node.NodeID {
		t.Fatalf("node IDs = %q/%q, want same placement", first.Node.NodeID, second.Node.NodeID)
	}
	if got := provider.createCallCount(); got != 1 {
		t.Fatalf("Create calls = %d, want 1", got)
	}
}

func TestProvisionDifferentAgentsDoNotSerializeProviderCreate(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	enteredCreate := make(chan string, 2)
	releaseCreates := make(chan struct{})
	provider := &fakeProvider{
		createHook: func(sandboxID string) {
			enteredCreate <- sandboxID
			<-releaseCreates
		},
	}
	broker := mustBroker(t, st, provider)

	errs := make(chan error, 2)
	for _, agent := range []string{"nova", "atlas"} {
		agent := agent
		go func() {
			_, err := broker.Provision(ctx, testProvisionRequest(agent, 1, 2))
			errs <- err
		}()
	}

	receiveCreateEntered(t, enteredCreate)
	receiveCreateEntered(t, enteredCreate)
	close(releaseCreates)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("Provision error: %v", err)
		}
	}
	if got := provider.createCallCount(); got != 2 {
		t.Fatalf("Create calls = %d, want 2", got)
	}
}

func TestProvisionRecordBeforeCreateFailure(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{createErr: errors.New("provider down")}
	broker := mustBroker(t, st, provider)

	_, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4))
	if err == nil {
		t.Fatal("Provision succeeded, want create error")
	}
	nodes, err := st.Nodes().List(ctx, "WS")
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want one placement record", len(nodes))
	}
	// A create that produced no sandbox can never boot, so the record is flipped
	// out of provisioning to released with the cause recorded — it must not hang
	// in provisioning until the reaper deadline (which would also leak the
	// MaxLive reservation, since released is not quota-reserved).
	assertPlacement(t, nodes[0], domain.PlacementStateReleased, "")
	if got := nodes[0].Placement.LastDeleteError; !strings.Contains(got, "provider down") {
		t.Fatalf("LastDeleteError = %q, want the create error recorded", got)
	}
	if nodes[0].Placement.ProvisioningDeadlineAt != nil {
		t.Fatalf("ProvisioningDeadlineAt = %v, want cleared on provision failure", nodes[0].Placement.ProvisioningDeadlineAt)
	}
	if got := provider.deleteCallCount(); got != 0 {
		t.Fatalf("Delete calls = %d, want 0 when Create returned no id", got)
	}
}

func TestProvisionCreateLabelAndToken(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)

	result, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if got := result.Node.DrainState; got != domain.NodeDrainDrained {
		t.Fatalf("placement drain state = %q, want %q", got, domain.NodeDrainDrained)
	}
	call := provider.createCall(t, 0)
	if got := call.Labels[PlacementLabelKey]; got != result.Node.NodeID {
		t.Fatalf("create label %s = %q, want node id %q", PlacementLabelKey, got, result.Node.NodeID)
	}
	if got := call.Labels[EnvironmentLabelKey]; got != testDeploymentID {
		t.Fatalf("create label %s = %q, want %q", EnvironmentLabelKey, got, testDeploymentID)
	}
	claims := parseCreateToken(t, call)
	if claims.PlacementID != result.Node.NodeID || claims.Generation != result.Node.Placement.Generation {
		t.Fatalf("claims placement/gen = %q/%d, want %q/%d",
			claims.PlacementID, claims.Generation, result.Node.NodeID, result.Node.Placement.Generation)
	}
	if claims.WorkspaceKey != "WS" || len(claims.Caps) == 0 {
		t.Fatalf("claims workspace/caps = %q/%v, want WS and non-empty caps", claims.WorkspaceKey, claims.Caps)
	}
	if spec := provider.startProcessCall(t, 0); spec.SessionID != LeadPTYSessionID {
		t.Fatalf("lead PTY session id = %q, want %q", spec.SessionID, LeadPTYSessionID)
	}
}

func TestProvisionPreparesLeadBootBeforeCreatePty(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createRepo(t, st, "WS", "app", "https://github.com/acme/app", "main")
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)

	req := testProvisionRequest("nova", 2, 4)
	req.NetworkDomainAllowlist = []string{"api.loom.invalid", "github.com"}
	req.PromptText = "lead prompt"
	result, err := broker.Provision(ctx, req)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	assertStringSlicesEqual(t, provider.eventsSnapshot(), []string{
		"create:sandbox-1",
		"prep:sandbox-1",
		"startProcess:sandbox-1",
		"autostop:2m0s",
	})
	prep := provider.prepCall(t, 0)
	if prep.Repo == nil || prep.Repo.Checkout != "/root/workspace/app" || prep.Repo.Ref != "main" {
		t.Fatalf("prep repo = %#v, want app checkout on main", prep.Repo)
	}
	spec := provider.startProcessCall(t, 0)
	if spec.WorkingDir != prep.Repo.Checkout {
		t.Fatalf("WorkingDir = %q, want checkout %q", spec.WorkingDir, prep.Repo.Checkout)
	}
	if got := promptPathFromCommand(spec.Command); got != prep.PromptPath {
		t.Fatalf("command prompt = %q, want prep prompt path %q", got, prep.PromptPath)
	}
	if !result.LeadStarted || result.LeadStartError != "" {
		t.Fatalf("lead boot = started %v err %q, want success", result.LeadStarted, result.LeadStartError)
	}
}

func TestProvisionPrepFailureCompensatesAndLeavesNoActivePlacement(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createRepo(t, st, "WS", "app", "https://github.com/acme/app", "main")
	provider := &fakeProvider{prepErr: errors.New("clone failed")}
	broker := mustBroker(t, st, provider)

	req := testProvisionRequest("nova", 2, 4)
	req.NetworkDomainAllowlist = []string{"github.com"}
	_, err := broker.Provision(ctx, req)
	if err == nil || !strings.Contains(err.Error(), "clone failed") {
		t.Fatalf("Provision = %v, want prep error", err)
	}

	node := onlyNode(t, st, "WS")
	assertPlacement(t, node, domain.PlacementStateReleased, "sandbox-1")
	assertStringSlicesEqual(t, node.Placement.AbandonedSandboxIDs, []string{"sandbox-1"})
	assertStringSlicesEqual(t, provider.deleteCallsSnapshot(), []string{"sandbox-1"})
	if got := provider.startProcessCallCount(); got != 0 {
		t.Fatalf("CreatePty calls = %d, want none after prep failure", got)
	}
}

// Seed files must reach prep (before any PTY exists) and must by themselves
// make prep run -- the credential drop can be the only prep work.
func TestProvisionSeedFilesReachPrep(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)

	req := testProvisionRequest("nova", 2, 4)
	req.SeedFiles = []SandboxFile{{Path: "/root/.codex/auth.json", Content: []byte(`{"t":1}`), Mode: "600"}}
	if _, err := broker.Provision(ctx, req); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if got := provider.prepCallCount(); got != 1 {
		t.Fatalf("prep calls = %d, want 1 for seed-files-only prep", got)
	}
	prep := provider.prepCall(t, 0)
	if len(prep.Files) != 1 || prep.Files[0].Path != "/root/.codex/auth.json" ||
		string(prep.Files[0].Content) != `{"t":1}` || prep.Files[0].Mode != "600" {
		t.Fatalf("prep files = %+v, want the seeded auth.json", prep.Files)
	}
}

// A prep-failure compensation whose delete the provider has not yet confirmed
// must leave the placement `releasing` (re-driven by the next Provision), never
// stamp `released` over a possibly-live sandbox. Regression: the first
// implementation issued an unconfirmed delete and stamped released anyway.
func TestProvisionPrepFailureUnconfirmedDeleteLeavesReleasing(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createRepo(t, st, "WS", "app", "https://github.com/acme/app", "main")
	provider := &fakeProvider{
		prepErr:             errors.New("clone failed"),
		deleteLeavesSandbox: true,
	}
	broker := mustBroker(t, st, provider)

	req := testProvisionRequest("nova", 2, 4)
	req.NetworkDomainAllowlist = []string{"github.com"}
	_, err := broker.Provision(ctx, req)
	if err == nil || !strings.Contains(err.Error(), "compensating release failed") {
		t.Fatalf("Provision = %v, want compensating release failure", err)
	}

	node := onlyNode(t, st, "WS")
	assertPlacement(t, node, domain.PlacementStateReleasing, "sandbox-1")
	assertStringSlicesEqual(t, node.Placement.AbandonedSandboxIDs, []string{"sandbox-1"})
}

// Prep must run on the resume path too: a placement whose checkout or prompt
// file is missing would otherwise boot a PTY that dies on the hook and wedge
// permanently, since only the create path materialized them. Regression for
// exactly that wedge.
func TestProvisionResumeRunsPrepAgain(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createRepo(t, st, "WS", "app", "https://github.com/acme/app", "main")
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)

	req := testProvisionRequest("nova", 2, 4)
	req.NetworkDomainAllowlist = []string{"github.com"}
	first, err := broker.Provision(ctx, req)
	if err != nil {
		t.Fatalf("first Provision: %v", err)
	}
	if got := provider.prepCallCount(); got != 1 {
		t.Fatalf("prep calls after create = %d, want 1", got)
	}

	// Simulate the wedge scenario: the lead never started (e.g. its PTY died
	// on a missing checkout). The resume path must re-run the idempotent prep
	// before retrying the boot, or the retry dies identically forever.
	node := getNode(t, st, "WS", first.Node.NodeID)
	cleared := clonePlacement(node.Placement)
	cleared.LeadProcessStartedAt = nil
	clearedPtr := &cleared
	if _, err := st.Nodes().Update(ctx, "WS", first.Node.NodeID, store.NodeUpdate{Placement: &clearedPtr}); err != nil {
		t.Fatalf("clear lead process started: %v", err)
	}

	second, err := broker.Provision(ctx, req)
	if err != nil {
		t.Fatalf("second Provision: %v", err)
	}
	if second.Node.NodeID != first.Node.NodeID {
		t.Fatalf("resume created a new placement %q, want %q", second.Node.NodeID, first.Node.NodeID)
	}
	if got := provider.prepCallCount(); got != 2 {
		t.Fatalf("prep calls after resume = %d, want 2", got)
	}
	firstPrep := provider.prepCall(t, 0)
	resumePrep := provider.prepCall(t, 1)
	if firstPrep.Repo == nil || resumePrep.Repo == nil || resumePrep.Repo.Checkout != firstPrep.Repo.Checkout {
		t.Fatalf("resume prep checkout = %+v, want same checkout as create prep %+v", resumePrep.Repo, firstPrep.Repo)
	}
}

func TestProvisionZeroReposBootsWithoutCloneOrWorkingDir(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)

	result := mustProvision(t, ctx, broker, "nova", 2, 4)
	if provider.prepCallCount() != 0 {
		t.Fatalf("prep calls = %d, want none for repo-less prompt-less workspace", provider.prepCallCount())
	}
	spec := provider.startProcessCall(t, 0)
	if spec.WorkingDir != "" {
		t.Fatalf("WorkingDir = %q, want empty without repo", spec.WorkingDir)
	}
	if !result.LeadStarted {
		t.Fatalf("LeadStarted = false, error %q", result.LeadStartError)
	}
}

func TestProvisionRepoSelectionFailsClosed(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		repoName string
		wantRepo string
		wantErr  error
	}{
		{name: "missing selector", wantErr: domain.ErrInvalid},
		{name: "matching selector", repoName: "api", wantRepo: "api"},
		{name: "nonmatching selector", repoName: "web", wantErr: domain.ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := memstore.New()
			createRepo(t, st, "WS", "api", "https://github.com/acme/api", "main")
			createRepo(t, st, "WS", "cli", "https://github.com/acme/cli", "trunk")
			provider := &fakeProvider{}
			broker := mustBroker(t, st, provider)

			req := testProvisionRequest("nova", 2, 4)
			req.NetworkDomainAllowlist = []string{"github.com"}
			req.RepoName = tc.repoName
			_, err := broker.Provision(ctx, req)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Provision = %v, want %v", err, tc.wantErr)
				}
				if got := provider.createCallCount(); got != 0 {
					t.Fatalf("Create calls = %d, want fail before provider create", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Provision: %v", err)
			}
			prep := provider.prepCall(t, 0)
			if prep.Repo == nil || prep.Repo.Name != tc.wantRepo {
				t.Fatalf("prep repo = %#v, want %q", prep.Repo, tc.wantRepo)
			}
		})
	}
}

func TestProvisionAllowlistRequiresCloneHostBeforeCreate(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createRepo(t, st, "WS", "app", "https://github.com/acme/app", "main")
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)

	req := testProvisionRequest("nova", 2, 4)
	req.NetworkDomainAllowlist = []string{"api.loom.invalid"}
	_, err := broker.Provision(ctx, req)
	if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "github.com") {
		t.Fatalf("Provision = %v, want allowlist error mentioning github.com", err)
	}
	if got := provider.createCallCount(); got != 0 {
		t.Fatalf("Create calls = %d, want fail before provider create", got)
	}
}

func TestProvisionDeadPtyAfterCreateIsNotMarkedStarted(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{dropPtyAfterCreate: true}
	broker := mustBroker(t, st, provider)

	result := mustProvision(t, ctx, broker, "nova", 2, 4)
	if result.LeadStarted || !strings.Contains(result.LeadStartError, "exited immediately") {
		t.Fatalf("lead boot = started %v err %q, want immediate-exit error", result.LeadStarted, result.LeadStartError)
	}
	if result.Node.Placement.LeadProcessStartedAt != nil {
		t.Fatalf("LeadProcessStartedAt = %v, want nil", result.Node.Placement.LeadProcessStartedAt)
	}
}

func TestProvisionPromptJoinUsesOnePath(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		command []string
		want    string
	}{
		{name: "default prompt path", want: defaultLeadPromptPath},
		{name: "existing prompt arg", command: []string{"loom", "lead", "--prompt", "/tmp/custom.md"}, want: "/tmp/custom.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := memstore.New()
			provider := &fakeProvider{}
			broker := mustBroker(t, st, provider)

			req := testProvisionRequest("nova", 2, 4)
			req.PromptText = "role prompt"
			req.Process.Command = tc.command
			_, err := broker.Provision(ctx, req)
			if err != nil {
				t.Fatalf("Provision: %v", err)
			}
			prep := provider.prepCall(t, 0)
			spec := provider.startProcessCall(t, 0)
			if prep.PromptPath != tc.want {
				t.Fatalf("prep prompt path = %q, want %q", prep.PromptPath, tc.want)
			}
			if got := promptPathFromCommand(spec.Command); got != tc.want {
				t.Fatalf("command prompt = %q, want %q", got, tc.want)
			}
			if countPromptArgs(spec.Command) != 1 {
				t.Fatalf("command = %v, want exactly one prompt arg", spec.Command)
			}
		})
	}
}

func TestProvisionLeadEnvIncludesTermBackendAndRole(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name         string
		reqBackend   string
		reqRole      string
		agentBackend string
		agentRole    string
		wantBackend  string
		wantRole     string
	}{
		{
			name:         "request wins",
			reqBackend:   "codex",
			reqRole:      "operator",
			agentBackend: "claude",
			agentRole:    "lead",
			wantBackend:  "codex",
			wantRole:     "operator",
		},
		{
			name:         "agent row fallback",
			agentBackend: "claude",
			agentRole:    "lead",
			wantBackend:  "claude",
			wantRole:     "lead",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := memstore.New()
			if _, err := st.Agents().Create(ctx, store.AgentCreate{
				WorkspaceKey: "WS",
				Name:         "nova",
				RoleName:     tc.agentRole,
				Backend:      tc.agentBackend,
			}); err != nil {
				t.Fatalf("create agent: %v", err)
			}
			provider := &fakeProvider{}
			broker := mustBroker(t, st, provider)

			req := testProvisionRequest("nova", 2, 4)
			req.Backend = tc.reqBackend
			req.AgentRole = tc.reqRole
			if _, err := broker.Provision(ctx, req); err != nil {
				t.Fatalf("Provision: %v", err)
			}
			createEnv := provider.createCall(t, 0).Env
			ptyEnv := provider.startProcessCall(t, 0).Env
			for _, env := range []map[string]string{createEnv, ptyEnv} {
				if env["TERM"] != "xterm-256color" {
					t.Fatalf("TERM = %q, want xterm-256color in %#v", env["TERM"], env)
				}
				if env["LOOM_BACKEND"] != tc.wantBackend {
					t.Fatalf("LOOM_BACKEND = %q, want %q", env["LOOM_BACKEND"], tc.wantBackend)
				}
				if env["LOOM_AGENT_ROLE"] != tc.wantRole {
					t.Fatalf("LOOM_AGENT_ROLE = %q, want %q", env["LOOM_AGENT_ROLE"], tc.wantRole)
				}
			}
		})
	}
}

func TestBrokerSettledDefaults(t *testing.T) {
	broker := mustBroker(t, memstore.New(), &fakeProvider{})

	if broker.nodeTTL != 10*time.Minute {
		t.Fatalf("node ttl = %v, want 10m", broker.nodeTTL)
	}
	if broker.provisioningTimeout != 10*time.Minute {
		t.Fatalf("provisioning timeout = %v, want 10m", broker.provisioningTimeout)
	}
	if broker.leadHeartbeatStaleAfter != 5*time.Minute {
		t.Fatalf("lead heartbeat stale after = %v, want 5m", broker.leadHeartbeatStaleAfter)
	}
	if broker.parkingAutostopInterval != 2*time.Minute {
		t.Fatalf("parking autostop interval = %v, want 2m", broker.parkingAutostopInterval)
	}
	if broker.leadBootPrepTimeout != 5*time.Minute {
		t.Fatalf("lead boot prep timeout = %v, want 5m", broker.leadBootPrepTimeout)
	}
	if broker.deploymentID != testDeploymentID {
		t.Fatalf("deployment id = %q, want %q", broker.deploymentID, testDeploymentID)
	}
}

func TestBrokerRequiresExplicitDeploymentID(t *testing.T) {
	t.Setenv(deploymentIDEnv, "")

	_, err := NewBroker(Config{Store: memstore.New(), Provider: &fakeProvider{}, TokenKey: testTokenKey})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("NewBroker without deployment id = %v, want ErrInvalid", err)
	}
	if err == nil || !strings.Contains(err.Error(), deploymentIDEnv) {
		t.Fatalf("NewBroker error = %v, want %s guidance", err, deploymentIDEnv)
	}
}

func TestBrokerUsesDeploymentIDFromEnv(t *testing.T) {
	t.Setenv(deploymentIDEnv, "env-deployment")

	broker, err := NewBroker(Config{Store: memstore.New(), Provider: &fakeProvider{}, TokenKey: testTokenKey})
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}
	if broker.deploymentID != "env-deployment" {
		t.Fatalf("deployment id = %q, want env-deployment", broker.deploymentID)
	}
}

func TestLeadEnvInjectsLeadAPIURLWhenConfigured(t *testing.T) {
	env := leadEnv(
		map[string]string{"CUSTOM": "value"},
		"WS",
		"nova",
		"placement-1",
		"occupant-token",
		" https://serve.example.com ",
		leadBootPlan{},
	)

	if got := env["LOOM_LEAD_API_URL"]; got != "https://serve.example.com" {
		t.Fatalf("LOOM_LEAD_API_URL = %q, want public serve URL", got)
	}
	if got := env["CUSTOM"]; got != "value" {
		t.Fatalf("CUSTOM = %q, want value", got)
	}
	if got := env[OccupantTokenEnv]; got != "occupant-token" {
		t.Fatalf("%s was not propagated", OccupantTokenEnv)
	}
}

func TestLeadEnvOmitsLeadAPIURLWhenUnconfigured(t *testing.T) {
	env := leadEnv(nil, "WS", "nova", "placement-1", "occupant-token", "", leadBootPlan{})

	if _, ok := env["LOOM_LEAD_API_URL"]; ok {
		t.Fatalf("LOOM_LEAD_API_URL injected despite empty LeadAPIBaseURL: %#v", env)
	}
}

func TestProvisionCompensatesWhenRecordIDWriteFails(t *testing.T) {
	ctx := context.Background()
	base := memstore.New()
	st := storeWithNodes{
		Store: base,
		nodes: failingUpdateNodeStore{
			NodeStore: base.Nodes(),
			fail: func(_ context.Context, _ string, _ string, patch store.NodeUpdate) error {
				if patchSandboxID(patch) != "" {
					return errors.New("fleet-db update failed")
				}
				return nil
			},
		},
	}
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)

	_, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4))
	if err == nil {
		t.Fatal("Provision succeeded, want record-id write error")
	}
	assertStringSlicesEqual(t, provider.deleteCallsSnapshot(), []string{"sandbox-1"})
	nodes, err := base.Nodes().List(ctx, "WS")
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want one provisioning record", len(nodes))
	}
	assertPlacement(t, nodes[0], domain.PlacementStateProvisioning, "")
	assertStringSlicesEqual(t, nodes[0].Placement.AbandonedSandboxIDs, []string{"sandbox-1"})
}

func TestProvisionCreateErrorWithIDCompensates(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	created := CreateResult{SandboxID: "created-before-error"}
	provider := &fakeProvider{createResult: &created, createErr: errors.New("post-create setup failed")}
	broker := mustBroker(t, st, provider)

	_, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4))
	if err == nil {
		t.Fatal("Provision succeeded, want create error")
	}
	assertStringSlicesEqual(t, provider.deleteCallsSnapshot(), []string{"created-before-error"})
	nodes, err := st.Nodes().List(ctx, "WS")
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want one provisioning record", len(nodes))
	}
	assertPlacement(t, nodes[0], domain.PlacementStateProvisioning, "")
	assertStringSlicesEqual(t, nodes[0].Placement.AbandonedSandboxIDs, []string{"created-before-error"})
}

func TestProvisionCancelledCallerContextStillRecordsSandboxID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	base := memstore.New()
	st := storeWithNodes{
		Store: base,
		nodes: failingUpdateNodeStore{
			NodeStore: base.Nodes(),
			fail: func(ctx context.Context, _ string, _ string, patch store.NodeUpdate) error {
				if patch.Placement != nil && ctx.Err() != nil {
					return ctx.Err()
				}
				return nil
			},
		},
	}
	provider := &fakeProvider{createHook: func(string) { cancel() }}
	broker := mustBroker(t, st, provider)

	result, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4))
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	assertPlacement(t, result.Node, domain.PlacementStateActive, "sandbox-1")
	if err := provider.createContextErr(); err != nil {
		t.Fatalf("Create saw caller cancellation: %v", err)
	}
}

func TestReleaseOrdering(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)
	result := mustProvision(t, ctx, broker, "nova", 2, 4)
	seen := make(chan domain.PlacementState, 1)
	provider.deleteHook = func(string) { seen <- getNodeState(t, st, "WS", result.Node.NodeID) }

	released, err := broker.Release(ctx, "WS", result.Node.NodeID, releaseFence(result.Node))
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got := receiveState(t, seen); got != domain.PlacementStateReleasing {
		t.Fatalf("state during Delete = %q, want releasing", got)
	}
	assertPlacement(t, released, domain.PlacementStateReleased, result.Node.Placement.SandboxID)
}

func TestReleaseDeleteFailureLeavesReleasing(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{deleteErr: errors.New("delete failed")}
	broker := mustBroker(t, st, provider)
	result := mustProvision(t, ctx, broker, "nova", 2, 4)

	_, err := broker.Release(ctx, "WS", result.Node.NodeID, releaseFence(result.Node))
	if err == nil {
		t.Fatal("Release succeeded, want delete error")
	}
	node := getNode(t, st, "WS", result.Node.NodeID)
	assertPlacement(t, node, domain.PlacementStateReleasing, result.Node.Placement.SandboxID)
}

func TestReleaseRequiresGetConfirmedDeletion(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{deleteLeavesSandbox: true}
	broker := mustBroker(t, st, provider)
	result := mustProvision(t, ctx, broker, "nova", 2, 4)

	_, err := broker.Release(ctx, "WS", result.Node.NodeID, releaseFence(result.Node))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Release with still-visible sandbox = %v, want ErrConflict", err)
	}
	// Confirmation polls because provider deletion is asynchronous, so the
	// exact read count is an implementation detail. What matters is that a
	// sandbox the provider still reports never confirms as deleted.
	if got := provider.getCallCount(); got == 0 {
		t.Fatal("no confirmation read was issued after delete")
	}
	node := getNode(t, st, "WS", result.Node.NodeID)
	assertPlacement(t, node, domain.PlacementStateReleasing, result.Node.Placement.SandboxID)
}

func TestReleaseGenerationFenceRejectsStaleGeneration(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)
	first := mustProvision(t, ctx, broker, "nova", 2, 4)
	if _, err := broker.Release(ctx, "WS", first.Node.NodeID, releaseFence(first.Node)); err != nil {
		t.Fatalf("Release first: %v", err)
	}
	second := mustProvision(t, ctx, broker, "nova", 2, 4)
	deleteCallsBefore := provider.deleteCallCount()

	_, err := broker.Release(ctx, "WS", second.Node.NodeID, ReleaseFence{Generation: first.Node.Placement.Generation, SandboxID: second.Node.Placement.SandboxID})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Release stale generation = %v, want ErrConflict", err)
	}
	if got := provider.deleteCallCount(); got != deleteCallsBefore {
		t.Fatalf("Delete calls after stale release = %d, want %d", got, deleteCallsBefore)
	}
	node := getNode(t, st, "WS", second.Node.NodeID)
	assertPlacement(t, node, domain.PlacementStateActive, second.Node.Placement.SandboxID)
}

func TestReleaseSandboxFenceRejectsMismatchedSandboxID(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)
	result := mustProvision(t, ctx, broker, "nova", 2, 4)

	_, err := broker.Release(ctx, "WS", result.Node.NodeID, ReleaseFence{
		Generation: result.Node.Placement.Generation,
		SandboxID:  "stale-sandbox",
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Release mismatched sandbox = %v, want ErrConflict", err)
	}
	if got := provider.deleteCallCount(); got != 0 {
		t.Fatalf("Delete calls after mismatched sandbox = %d, want 0", got)
	}
	node := getNode(t, st, "WS", result.Node.NodeID)
	assertPlacement(t, node, domain.PlacementStateActive, result.Node.Placement.SandboxID)
}

func TestReleaseEmptySandboxIDDeletesLabelledOrphan(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	node := createPlacementNode(t, st, "WS", "lead-placement-orphan", "nova", domain.NodePlacement{
		Generation:     1,
		ReservedVCPU:   2,
		ReservedMemGiB: 4,
		State:          domain.PlacementStateProvisioning,
		SnapshotRef:    "snapshot://lead",
	})
	provider := &fakeProvider{}
	provider.addSandbox(ProviderSandbox{
		ID: "orphan-1",
		Labels: map[string]string{
			PlacementLabelKey:   node.NodeID,
			EnvironmentLabelKey: testDeploymentID,
			"loom-workspace":    "WS",
		},
		State: ProviderSandboxRunning,
	})
	broker := mustBroker(t, st, provider)

	released, err := broker.Release(ctx, "WS", node.NodeID, releaseFence(node))
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	assertPlacement(t, released, domain.PlacementStateReleased, "orphan-1")
	assertStringSlicesEqual(t, provider.deleteCallsSnapshot(), []string{"orphan-1"})
	if got := provider.listCallCount(); got != 1 {
		t.Fatalf("List calls = %d, want 1", got)
	}
}

func TestReleaseUnknownSandboxIDAfterDeadlineMarksReleased(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	now := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	node := createPlacementNode(t, st, "WS", "lead-placement-lost", "nova", domain.NodePlacement{
		Generation:             1,
		ReservedVCPU:           2,
		ReservedMemGiB:         4,
		State:                  domain.PlacementStateProvisioning,
		ProvisioningDeadlineAt: &past,
		SnapshotRef:            "snapshot://lead",
	})
	provider := &fakeProvider{}
	broker := mustBrokerWithNow(t, st, provider, now)

	released, err := broker.Release(ctx, "WS", node.NodeID, releaseFence(node))
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	assertPlacement(t, released, domain.PlacementStateReleased, "")
	if got := provider.deleteCallCount(); got != 0 {
		t.Fatalf("Delete calls = %d, want 0 for unknown sandbox id", got)
	}
}

func TestReleaseUnknownSandboxIDBeforeDeadlineIsRetryable(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	now := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	future := now.Add(time.Minute)
	node := createPlacementNode(t, st, "WS", "lead-placement-pending", "nova", domain.NodePlacement{
		Generation:             1,
		ReservedVCPU:           2,
		ReservedMemGiB:         4,
		State:                  domain.PlacementStateProvisioning,
		ProvisioningDeadlineAt: &future,
		SnapshotRef:            "snapshot://lead",
	})
	provider := &fakeProvider{}
	broker := mustBrokerWithNow(t, st, provider, now)

	_, err := broker.Release(ctx, "WS", node.NodeID, releaseFence(node))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Release before deadline = %v, want ErrConflict", err)
	}
	still := getNode(t, st, "WS", node.NodeID)
	assertPlacement(t, still, domain.PlacementStateProvisioning, "")
}

func TestProvisionAdoptsGetConfirmedLabelledSandboxWithinDeadline(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	now := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	future := now.Add(time.Minute)
	node := createPlacementNode(t, st, "WS", "lead-placement-adopt", "nova", domain.NodePlacement{
		Generation:             1,
		ReservedVCPU:           2,
		ReservedMemGiB:         4,
		State:                  domain.PlacementStateProvisioning,
		ProvisioningDeadlineAt: &future,
		SnapshotRef:            "snapshot://lead",
	})
	provider := &fakeProvider{}
	provider.addSandbox(ProviderSandbox{
		ID: "adopted-1",
		Labels: map[string]string{
			PlacementLabelKey:   node.NodeID,
			EnvironmentLabelKey: testDeploymentID,
			"loom-workspace":    "WS",
		},
		State: ProviderSandboxRunning,
	})
	broker := mustBrokerWithNow(t, st, provider, now)

	result := mustProvision(t, ctx, broker, "nova", 2, 4)
	if result.Node.NodeID != node.NodeID {
		t.Fatalf("node id = %q, want adopted predecessor %q", result.Node.NodeID, node.NodeID)
	}
	assertPlacement(t, result.Node, domain.PlacementStateActive, "adopted-1")
	if got := provider.createCallCount(); got != 0 {
		t.Fatalf("Create calls = %d, want 0 for adoption", got)
	}
	if got := provider.getCallCount(); got == 0 {
		t.Fatal("Get calls = 0, want adoption confirmation")
	}
}

func TestProvisionDoesNotAdoptListOnlyDeletedSandbox(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	now := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	future := now.Add(time.Minute)
	node := createPlacementNode(t, st, "WS", "lead-placement-list-stale", "nova", domain.NodePlacement{
		Generation:             1,
		ReservedVCPU:           2,
		ReservedMemGiB:         4,
		State:                  domain.PlacementStateProvisioning,
		ProvisioningDeadlineAt: &future,
		SnapshotRef:            "snapshot://lead",
	})
	provider := &fakeProvider{}
	provider.addListOnlySandbox(ProviderSandbox{
		ID: "deleted-but-listed",
		Labels: map[string]string{
			PlacementLabelKey:   node.NodeID,
			EnvironmentLabelKey: testDeploymentID,
			"loom-workspace":    "WS",
		},
		State: ProviderSandboxRunning,
	})
	broker := mustBrokerWithNow(t, st, provider, now)

	_, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Provision = %v, want ErrConflict before deadline", err)
	}
	assertPlacement(t, getNode(t, st, "WS", node.NodeID), domain.PlacementStateProvisioning, "")
	if got := provider.getCallCount(); got != 1 {
		t.Fatalf("Get calls = %d, want stale list confirmation", got)
	}
	if got := provider.createCallCount(); got != 0 {
		t.Fatalf("Create calls = %d, want 0 before deadline", got)
	}
}

func TestProvisionActiveGetConfirmedMissingMarksLostNoRetry(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	node := createPlacementNode(t, st, "WS", "lead-placement-missing", "nova", domain.NodePlacement{
		SandboxID:      "missing-sandbox",
		Generation:     1,
		ReservedVCPU:   2,
		ReservedMemGiB: 4,
		State:          domain.PlacementStateActive,
		SnapshotRef:    "snapshot://lead",
	})
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)

	_, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Provision = %v, want ErrConflict for active missing sandbox", err)
	}
	assertPlacement(t, getNode(t, st, "WS", node.NodeID), domain.PlacementStateLost, "missing-sandbox")
	if got := provider.createCallCount(); got != 0 {
		t.Fatalf("Create calls = %d, want no retry", got)
	}
}

func TestRecordSandboxIDRejectsRepoint(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	node := createPlacementNode(t, st, "WS", "lead-placement-write-once", "nova", domain.NodePlacement{
		SandboxID:      "sandbox-1",
		Generation:     1,
		ReservedVCPU:   2,
		ReservedMemGiB: 4,
		State:          domain.PlacementStateActive,
		SnapshotRef:    "snapshot://lead",
	})
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)

	_, err := broker.recordSandboxID(ctx, node, "sandbox-2")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("recordSandboxID repoint = %v, want ErrConflict", err)
	}
	assertPlacement(t, getNode(t, st, "WS", node.NodeID), domain.PlacementStateActive, "sandbox-1")
}

func TestProvisionFailsClosedWithTwoLiveRows(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createPlacementNode(t, st, "WS", "lead-placement-live-1", "nova", domain.NodePlacement{
		SandboxID:      "sandbox-1",
		Generation:     1,
		ReservedVCPU:   2,
		ReservedMemGiB: 4,
		State:          domain.PlacementStateActive,
		SnapshotRef:    "snapshot://lead",
	})
	createPlacementNode(t, st, "WS", "lead-placement-live-2", "nova", domain.NodePlacement{
		SandboxID:      "sandbox-2",
		Generation:     2,
		ReservedVCPU:   2,
		ReservedMemGiB: 4,
		State:          domain.PlacementStateActive,
		SnapshotRef:    "snapshot://lead",
	})
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)

	_, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Provision = %v, want ErrConflict for duplicate live rows", err)
	}
	if got := provider.createCallCount(); got != 0 {
		t.Fatalf("Create calls = %d, want fail-closed before provider create", got)
	}
}

func TestLeadProcessAlreadyExistsIsSuccess(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{startProcessErr: ErrPtySessionAlreadyExists}
	broker := mustBroker(t, st, provider)

	result := mustProvision(t, ctx, broker, "nova", 2, 4)
	assertPlacement(t, result.Node, domain.PlacementStateActive, "sandbox-1")
	if !result.LeadStarted || result.LeadStartError != "" {
		t.Fatalf("lead boot result = started %v error %q, want already-exists success", result.LeadStarted, result.LeadStartError)
	}
	if result.Node.Placement.LeadProcessStartedAt == nil {
		t.Fatal("LeadProcessStartedAt = nil, want already-exists recorded as started")
	}
}

func TestStaleHeartbeatUsesPtyObservationBeforeRedrive(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	startedAt := time.Now().UTC().Add(-10 * time.Minute)
	node := createPlacementNode(t, st, "WS", "lead-placement-stale-heartbeat", "nova", domain.NodePlacement{
		SandboxID:            "sandbox-1",
		Generation:           1,
		ReservedVCPU:         2,
		ReservedMemGiB:       4,
		State:                domain.PlacementStateActive,
		LeadProcessStartedAt: &startedAt,
		SnapshotRef:          "snapshot://lead",
	})
	provider := &fakeProvider{}
	provider.addSandbox(ProviderSandbox{
		ID: "sandbox-1",
		Labels: map[string]string{
			PlacementLabelKey:   node.NodeID,
			EnvironmentLabelKey: testDeploymentID,
			"loom-workspace":    "WS",
		},
		State: ProviderSandboxRunning,
	})
	provider.addPtySession("sandbox-1", LeadPTYSessionID)
	broker := mustBrokerWithNow(t, st, provider, time.Now().UTC().Add(10*time.Minute))

	result := mustProvision(t, ctx, broker, "nova", 2, 4)
	assertPlacement(t, result.Node, domain.PlacementStateActive, "sandbox-1")
	if got := provider.listPtySessionCallCount(); got != 1 {
		t.Fatalf("ListPtySessions calls = %d, want 1 stale-heartbeat probe", got)
	}
	if got := provider.startProcessCallCount(); got != 0 {
		t.Fatalf("StartProcess calls = %d, want no duplicate lead PTY", got)
	}
}

func TestProvisionResumeFreshHeartbeatRunningSandboxIsUnchanged(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	startedAt := time.Now().UTC().Add(-time.Minute)
	node := createPlacementNode(t, st, "WS", "lead-placement-running", "nova", domain.NodePlacement{
		SandboxID:            "sandbox-1",
		Generation:           1,
		ReservedVCPU:         2,
		ReservedMemGiB:       4,
		State:                domain.PlacementStateActive,
		LeadProcessStartedAt: &startedAt,
		SnapshotRef:          "snapshot://lead",
	})
	provider := &fakeProvider{}
	provider.addSandbox(ProviderSandbox{
		ID: "sandbox-1",
		Labels: map[string]string{
			PlacementLabelKey:   node.NodeID,
			EnvironmentLabelKey: testDeploymentID,
			"loom-workspace":    "WS",
		},
		State: ProviderSandboxRunning,
	})
	broker := mustBroker(t, st, provider)

	result := mustProvision(t, ctx, broker, "nova", 2, 4)
	assertPlacement(t, result.Node, domain.PlacementStateActive, "sandbox-1")
	if got := provider.ensureRunningCallCount(); got != 1 {
		t.Fatalf("EnsureRunning calls = %d, want 1", got)
	}
	if got := provider.listPtySessionCallCount(); got != 0 {
		t.Fatalf("ListPtySessions calls = %d, want no fresh-heartbeat RUNNING probe", got)
	}
	if got := provider.startProcessCallCount(); got != 0 {
		t.Fatalf("CreatePty calls = %d, want 0", got)
	}
	if got := provider.deleteCallCount(); got != 0 {
		t.Fatalf("Delete calls = %d, want 0", got)
	}
}

func TestProvisionRevivedSandboxForcesLeadProbeAndRestoresParking(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createRepo(t, st, "WS", "app", "https://github.com/acme/app", "main")
	startedAt := time.Now().UTC().Add(-time.Minute)
	node := createPlacementNode(t, st, "WS", "lead-placement-parked", "nova", domain.NodePlacement{
		SandboxID:            "sandbox-1",
		Generation:           7,
		ReservedVCPU:         2,
		ReservedMemGiB:       4,
		State:                domain.PlacementStateActive,
		LeadProcessStartedAt: &startedAt,
		SnapshotRef:          "snapshot://lead",
	})
	provider := &fakeProvider{}
	provider.addSandbox(ProviderSandbox{
		ID: "sandbox-1",
		Labels: map[string]string{
			PlacementLabelKey:   node.NodeID,
			EnvironmentLabelKey: testDeploymentID,
			"loom-workspace":    "WS",
		},
		State: ProviderSandboxStopped,
	})
	broker := mustBroker(t, st, provider)
	req := testProvisionRequest("nova", 2, 4)
	req.PromptText = "lead prompt"
	req.NetworkDomainAllowlist = []string{"github.com"}

	result, err := broker.Provision(ctx, req)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if result.Node.NodeID != node.NodeID || result.Node.Placement.Generation != 7 {
		t.Fatalf("placement = %q generation %d, want same %q generation 7", result.Node.NodeID, result.Node.Placement.Generation, node.NodeID)
	}
	if !result.LeadStarted || result.LeadStartError != "" {
		t.Fatalf("lead boot = started %v error %q, want revived", result.LeadStarted, result.LeadStartError)
	}
	if got := provider.startProcessCallCount(); got != 1 {
		t.Fatalf("CreatePty calls = %d, want forced probe followed by recreate", got)
	}
	assertDurationSlicesEqual(t, provider.setAutostopCallsSnapshot(), []time.Duration{30 * time.Minute, 2 * time.Minute})
	assertStringSlicesEqual(t, provider.eventsSnapshot(), []string{
		"autostop:30m0s",
		"ensureRunning:sandbox-1",
		"prep:sandbox-1",
		"startProcess:sandbox-1",
		"autostop:2m0s",
	})
	if got := provider.createCallCount(); got != 0 {
		t.Fatalf("Create calls = %d, want 0", got)
	}
	if got := provider.deleteCallCount(); got != 0 {
		t.Fatalf("Delete calls = %d, want 0", got)
	}
}

func TestProvisionRevivedSandboxRestoresParkingAfterPrepFailure(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createRepo(t, st, "WS", "app", "https://github.com/acme/app", "main")
	node := createPlacementNode(t, st, "WS", "lead-placement-parked", "nova", domain.NodePlacement{
		SandboxID:      "sandbox-1",
		Generation:     3,
		State:          domain.PlacementStateActive,
		SnapshotRef:    "snapshot://lead",
		ReservedVCPU:   2,
		ReservedMemGiB: 4,
	})
	provider := &fakeProvider{prepErr: errors.New("prep failed")}
	provider.addSandbox(ProviderSandbox{
		ID: "sandbox-1",
		Labels: map[string]string{
			PlacementLabelKey: node.NodeID,
			"loom-workspace":  "WS",
		},
		State: ProviderSandboxStopped,
	})
	broker := mustBroker(t, st, provider)
	req := testProvisionRequest("nova", 2, 4)
	req.PromptText = "lead prompt"
	req.NetworkDomainAllowlist = []string{"github.com"}

	result, err := broker.Provision(ctx, req)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if result.LeadStarted || !strings.Contains(result.LeadStartError, "prep failed") {
		t.Fatalf("lead boot = started %v error %q, want prep failure", result.LeadStarted, result.LeadStartError)
	}
	assertDurationSlicesEqual(t, provider.setAutostopCallsSnapshot(), []time.Duration{30 * time.Minute, 2 * time.Minute})
	if got := provider.createCallCount(); got != 0 {
		t.Fatalf("Create calls = %d, want 0", got)
	}
	if got := provider.deleteCallCount(); got != 0 {
		t.Fatalf("Delete calls = %d, want 0", got)
	}
}

func TestProvisionForcedLeadProbeRestoresParkingOnEveryBootOutcome(t *testing.T) {
	tests := []struct {
		name              string
		configureProvider func(*fakeProvider)
		wantStarted       bool
		wantError         string
		wantCreatePty     int
	}{
		{
			name: "CreatePty failure",
			configureProvider: func(provider *fakeProvider) {
				provider.startProcessErr = errors.New("create PTY failed")
			},
			wantError:     "create PTY failed",
			wantCreatePty: 1,
		},
		{
			name: "PTY exits immediately",
			configureProvider: func(provider *fakeProvider) {
				provider.dropPtyAfterCreate = true
			},
			wantError:     "exited immediately",
			wantCreatePty: 1,
		},
		{
			name: "lead PTY already exists",
			configureProvider: func(provider *fakeProvider) {
				provider.addPtySession("sandbox-1", LeadPTYSessionID)
			},
			wantStarted: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			st := memstore.New()
			startedAt := time.Now().UTC()
			node := createPlacementNode(t, st, "WS", "lead-placement-running", "nova", domain.NodePlacement{
				SandboxID:            "sandbox-1",
				Generation:           3,
				State:                domain.PlacementStateActive,
				SnapshotRef:          "snapshot://lead",
				ReservedVCPU:         2,
				ReservedMemGiB:       4,
				LeadProcessStartedAt: &startedAt,
			})
			provider := &fakeProvider{}
			provider.addSandbox(ProviderSandbox{
				ID: "sandbox-1",
				Labels: map[string]string{
					PlacementLabelKey: node.NodeID,
					"loom-workspace":  "WS",
				},
				State: ProviderSandboxRunning,
			})
			tt.configureProvider(provider)
			broker := mustBroker(t, st, provider)
			req := testProvisionRequest("nova", 2, 4)
			req.ForceLeadProbe = true

			result, err := broker.Provision(ctx, req)
			if err != nil {
				t.Fatalf("Provision: %v", err)
			}
			wrongError := result.LeadStartError != ""
			if tt.wantError != "" {
				wrongError = !strings.Contains(result.LeadStartError, tt.wantError)
			}
			if result.LeadStarted != tt.wantStarted || wrongError {
				t.Fatalf("lead boot = started %v error %q, want started %v error containing %q", result.LeadStarted, result.LeadStartError, tt.wantStarted, tt.wantError)
			}
			if got := provider.startProcessCallCount(); got != tt.wantCreatePty {
				t.Fatalf("CreatePty calls = %d, want %d", got, tt.wantCreatePty)
			}
			assertDurationSlicesEqual(t, provider.setAutostopCallsSnapshot(), []time.Duration{30 * time.Minute, 2 * time.Minute})
		})
	}
}

func TestProvisionConfirmedAbsentSandboxSkipsParkingRestore(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	node := createPlacementNode(t, st, "WS", "lead-placement-missing", "nova", domain.NodePlacement{
		SandboxID:      "sandbox-1",
		Generation:     3,
		State:          domain.PlacementStateActive,
		SnapshotRef:    "snapshot://lead",
		ReservedVCPU:   2,
		ReservedMemGiB: 4,
	})
	provider := &fakeProvider{ensureRunningErr: ErrSandboxNotFound}
	provider.addSandbox(ProviderSandbox{
		ID: "sandbox-1",
		Labels: map[string]string{
			PlacementLabelKey: node.NodeID,
			"loom-workspace":  "WS",
		},
		State: ProviderSandboxRunning,
	})
	broker := mustBroker(t, st, provider)

	_, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Provision = %v, want marked-lost conflict", err)
	}
	assertDurationSlicesEqual(t, provider.setAutostopCallsSnapshot(), []time.Duration{30 * time.Minute})
}

func TestResumeLeadProcessStartFailureReturnsPlacementResult(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	node := createPlacementNode(t, st, "WS", "lead-placement-boot-retry", "nova", domain.NodePlacement{
		SandboxID:      "sandbox-1",
		Generation:     1,
		ReservedVCPU:   2,
		ReservedMemGiB: 4,
		State:          domain.PlacementStateActive,
		SnapshotRef:    "snapshot://lead",
	})
	provider := &fakeProvider{startProcessErr: errors.New("boot 503")}
	provider.addSandbox(ProviderSandbox{
		ID: "sandbox-1",
		Labels: map[string]string{
			PlacementLabelKey:   node.NodeID,
			EnvironmentLabelKey: testDeploymentID,
			"loom-workspace":    "WS",
		},
		State: ProviderSandboxRunning,
	})
	broker := mustBroker(t, st, provider)

	result, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4))
	if err != nil {
		t.Fatalf("Provision resume boot failure: %v", err)
	}
	if result.Node.NodeID != node.NodeID {
		t.Fatalf("node id = %q, want resumed placement %q", result.Node.NodeID, node.NodeID)
	}
	if result.LeadStarted || !strings.Contains(result.LeadStartError, "boot 503") {
		t.Fatalf("lead boot result = started %v error %q, want surfaced boot error", result.LeadStarted, result.LeadStartError)
	}
	assertPlacement(t, getNode(t, st, "WS", node.NodeID), domain.PlacementStateActive, "sandbox-1")
	if got := provider.createCallCount(); got != 0 {
		t.Fatalf("Create calls = %d, want 0 for resumed placement boot failure", got)
	}
}

func TestResumeRecordedSandboxIgnoresDeploymentLabelDrift(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	node := createPlacementNode(t, st, "WS", "lead-placement-old-env", "nova", domain.NodePlacement{
		SandboxID:      "sandbox-1",
		Generation:     1,
		ReservedVCPU:   2,
		ReservedMemGiB: 4,
		State:          domain.PlacementStateActive,
		SnapshotRef:    "snapshot://lead",
	})
	provider := &fakeProvider{}
	provider.addSandbox(ProviderSandbox{
		ID: "sandbox-1",
		Labels: map[string]string{
			PlacementLabelKey:   node.NodeID,
			EnvironmentLabelKey: "previous-deployment",
			"loom-workspace":    "WS",
		},
		State: ProviderSandboxRunning,
	})
	broker := mustBroker(t, st, provider)

	result := mustProvision(t, ctx, broker, "nova", 2, 4)
	if result.Node.NodeID != node.NodeID {
		t.Fatalf("node id = %q, want resumed placement %q", result.Node.NodeID, node.NodeID)
	}
	assertPlacement(t, result.Node, domain.PlacementStateActive, "sandbox-1")
	if got := provider.createCallCount(); got != 0 {
		t.Fatalf("Create calls = %d, want 0 for recorded sandbox env drift", got)
	}
	if got := provider.startProcessCallCount(); got != 1 {
		t.Fatalf("StartProcess calls = %d, want lead boot in existing sandbox", got)
	}
}

func TestLeadProcessNilStartedAtUsesPtyObservationBeforeCreate(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	node := createPlacementNode(t, st, "WS", "lead-placement-nil-started", "nova", domain.NodePlacement{
		SandboxID:      "sandbox-1",
		Generation:     1,
		ReservedVCPU:   2,
		ReservedMemGiB: 4,
		State:          domain.PlacementStateActive,
		SnapshotRef:    "snapshot://lead",
	})
	provider := &fakeProvider{}
	provider.addSandbox(ProviderSandbox{
		ID: "sandbox-1",
		Labels: map[string]string{
			PlacementLabelKey:   node.NodeID,
			EnvironmentLabelKey: testDeploymentID,
			"loom-workspace":    "WS",
		},
		State: ProviderSandboxRunning,
	})
	provider.addPtySession("sandbox-1", LeadPTYSessionID)
	broker := mustBroker(t, st, provider)

	result := mustProvision(t, ctx, broker, "nova", 2, 4)
	assertPlacement(t, result.Node, domain.PlacementStateActive, "sandbox-1")
	if !result.LeadStarted || result.LeadStartError != "" {
		t.Fatalf("lead boot result = started %v error %q, want observed existing PTY", result.LeadStarted, result.LeadStartError)
	}
	if result.Node.Placement.LeadProcessStartedAt == nil {
		t.Fatal("LeadProcessStartedAt = nil, want existing PTY observation recorded")
	}
	if got := provider.listPtySessionCallCount(); got != 1 {
		t.Fatalf("ListPtySessions calls = %d, want 1 idempotency probe", got)
	}
	if got := provider.startProcessCallCount(); got != 0 {
		t.Fatalf("StartProcess calls = %d, want no duplicate lead PTY", got)
	}
}

func TestReleaseForceUnknownSandboxDeletesLabelledOrphan(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	node := createPlacementNode(t, st, "WS", "lead-placement-force-unknown", "nova", domain.NodePlacement{
		Generation:     1,
		ReservedVCPU:   2,
		ReservedMemGiB: 4,
		State:          domain.PlacementStateProvisioning,
		SnapshotRef:    "snapshot://lead",
	})
	provider := &fakeProvider{}
	provider.addSandbox(ProviderSandbox{
		ID: "orphan-1",
		Labels: map[string]string{
			PlacementLabelKey:   node.NodeID,
			EnvironmentLabelKey: "previous-deployment",
			"loom-workspace":    "WS",
		},
		State: ProviderSandboxRunning,
	})
	seen := make(chan domain.PlacementState, 1)
	provider.deleteHook = func(string) { seen <- getNodeState(t, st, "WS", node.NodeID) }
	broker := mustBroker(t, st, provider)

	forced, err := broker.Release(ctx, "WS", node.NodeID, ReleaseFence{Force: true})
	if err != nil {
		t.Fatalf("Force Release: %v", err)
	}
	if got := receiveState(t, seen); got != domain.PlacementStateReleasing {
		t.Fatalf("state during Delete = %q, want releasing", got)
	}
	assertPlacement(t, forced, domain.PlacementStateReleased, "orphan-1")
	assertStringSlicesEqual(t, forced.Placement.AbandonedSandboxIDs, []string{"orphan-1"})
	if got := provider.deleteCallCount(); got != 1 {
		t.Fatalf("Delete calls = %d, want 1 for adopted labeled sandbox", got)
	}
	if got := provider.listCallCount(); got != 1 {
		t.Fatalf("List calls = %d, want 1 label sweep", got)
	}
}

func TestReleaseForceConfirmedDeleteFreesReservation(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)
	result := mustProvision(t, ctx, broker, "nova", 2, 4)

	forced, err := broker.Release(ctx, "WS", result.Node.NodeID, ReleaseFence{Force: true})
	if err != nil {
		t.Fatalf("Force Release: %v", err)
	}
	assertPlacement(t, forced, domain.PlacementStateReleased, "sandbox-1")
	assertStringSlicesEqual(t, forced.Placement.AbandonedSandboxIDs, []string{"sandbox-1"})
	if got := provider.deleteCallCount(); got != 1 {
		t.Fatalf("Delete calls during force release = %d, want 1", got)
	}
	list, err := broker.List(ctx, "WS")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if list.LiveReserved != (ResourceSize{}) {
		t.Fatalf("live reserved = %+v, want zero after confirmed force delete", list.LiveReserved)
	}
}

func TestReleaseForceUnconfirmedDeleteLeavesReleasingAndHoldsReservation(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{deleteLeavesSandbox: true}
	broker := mustBroker(t, st, provider)
	result := mustProvision(t, ctx, broker, "nova", 2, 4)
	seen := make(chan domain.PlacementState, 1)
	provider.deleteHook = func(string) { seen <- getNodeState(t, st, "WS", result.Node.NodeID) }

	forced, err := broker.Release(ctx, "WS", result.Node.NodeID, ReleaseFence{Force: true})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Force Release = %v, want ErrConflict", err)
	}
	if got := <-seen; got != domain.PlacementStateReleasing {
		t.Fatalf("state during Delete = %q, want releasing", got)
	}
	assertPlacement(t, forced, domain.PlacementStateReleasing, "sandbox-1")
	assertStringSlicesEqual(t, forced.Placement.AbandonedSandboxIDs, []string{"sandbox-1"})
	list, listErr := broker.List(ctx, "WS")
	if listErr != nil {
		t.Fatalf("List: %v", listErr)
	}
	if list.LiveReserved != (ResourceSize{VCPU: 2, MemGiB: 4}) {
		t.Fatalf("live reserved = %+v, want held releasing reservation", list.LiveReserved)
	}
}

func TestReprovisionBumpsGeneration(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)
	first := mustProvision(t, ctx, broker, "nova", 2, 4)
	oldClaims, err := leadtoken.ParseOccupantToken(first.Token, testTokenKey)
	if err != nil {
		t.Fatalf("parse first token: %v", err)
	}
	if _, err := broker.Release(ctx, "WS", first.Node.NodeID, releaseFence(first.Node)); err != nil {
		t.Fatalf("Release: %v", err)
	}

	second := mustProvision(t, ctx, broker, "nova", 2, 4)
	if second.Node.NodeID == first.Node.NodeID {
		t.Fatalf("node id = %q, want new successor row", second.Node.NodeID)
	}
	if second.Node.Placement.Generation != first.Node.Placement.Generation+1 {
		t.Fatalf("generation = %d, want %d", second.Node.Placement.Generation, first.Node.Placement.Generation+1)
	}
	if oldClaims.PlacementID == second.Node.NodeID || oldClaims.Generation == second.Node.Placement.Generation {
		t.Fatal("old token still matches re-provisioned placement")
	}
	assertPlacement(t, getNode(t, st, "WS", first.Node.NodeID), domain.PlacementStateReleased, first.Node.Placement.SandboxID)
}

func TestProvisionLostRecordBlocksUntilForceRelease(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)
	first := mustProvision(t, ctx, broker, "nova", 2, 4)
	markPlacementState(t, st, "WS", first.Node.NodeID, domain.PlacementStateLost)
	provider.resetEvents()

	_, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Provision over lost = %v, want ErrConflict", err)
	}
	if err == nil || !strings.Contains(err.Error(), "force release") {
		t.Fatalf("Provision error = %v, want manual force release guidance", err)
	}
	if got := provider.deleteCallCount(); got != 0 {
		t.Fatalf("Delete calls = %d, want 0 for lost manual-resolve block", got)
	}
	if got := provider.createCallCount(); got != 1 {
		t.Fatalf("Create calls = %d, want only original create", got)
	}
	assertPlacement(t, getNode(t, st, "WS", first.Node.NodeID), domain.PlacementStateLost, first.Node.Placement.SandboxID)
}

func TestForceReleaseLostRecordEnablesSuccessor(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)
	first := mustProvision(t, ctx, broker, "nova", 2, 4)
	markPlacementState(t, st, "WS", first.Node.NodeID, domain.PlacementStateLost)

	released, err := broker.Release(ctx, "WS", first.Node.NodeID, ReleaseFence{Force: true})
	if err != nil {
		t.Fatalf("Force Release lost: %v", err)
	}
	assertPlacement(t, released, domain.PlacementStateReleased, first.Node.Placement.SandboxID)
	second := mustProvision(t, ctx, broker, "nova", 2, 4)
	if second.Node.NodeID == first.Node.NodeID {
		t.Fatalf("node id = %q, want successor row after manual force release", second.Node.NodeID)
	}
	if second.Node.Placement.Generation != first.Node.Placement.Generation+1 {
		t.Fatalf("generation = %d, want %d", second.Node.Placement.Generation, first.Node.Placement.Generation+1)
	}
}

func TestProvisionLostRecordKeepsOwnReservation(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createPlacementNode(t, st, "WS", "lost-placement", "nova", domain.NodePlacement{
		SandboxID:      "lost-sandbox",
		Generation:     1,
		ReservedVCPU:   10,
		ReservedMemGiB: 10,
		State:          domain.PlacementStateLost,
		SnapshotRef:    "snapshot://lead",
	})
	provider := &fakeProvider{}
	provider.addSandbox(ProviderSandbox{
		ID: "lost-sandbox",
		Labels: map[string]string{
			PlacementLabelKey:   "lost-placement",
			EnvironmentLabelKey: testDeploymentID,
			"loom-workspace":    "WS",
		},
		State: ProviderSandboxRunning,
	})
	broker := mustBrokerWithMax(t, st, provider, ResourceSize{VCPU: 10, MemGiB: 10})

	_, err := broker.Provision(ctx, testProvisionRequest("nova", 10, 10))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Provision over lost = %v, want ErrConflict", err)
	}
	if got := provider.deleteCallCount(); got != 0 {
		t.Fatalf("Delete calls = %d, want 0 while lost requires manual resolve", got)
	}
	if got := provider.createCallCount(); got != 0 {
		t.Fatalf("Create calls = %d, want 0 while lost reservation is held", got)
	}
	assertPlacement(t, getNode(t, st, "WS", "lost-placement"), domain.PlacementStateLost, "lost-sandbox")
}

func TestProvisionStaleUnknownIDReleasesAndReprovisions(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	now := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	node := createPlacementNode(t, st, "WS", "lead-placement-stale", "nova", domain.NodePlacement{
		Generation:             1,
		ReservedVCPU:           2,
		ReservedMemGiB:         4,
		State:                  domain.PlacementStateProvisioning,
		ProvisioningDeadlineAt: &past,
		SnapshotRef:            "snapshot://lead",
	})
	provider := &fakeProvider{}
	broker := mustBrokerWithNow(t, st, provider, now)

	result, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4))
	if err != nil {
		t.Fatalf("Provision stale unknown id: %v", err)
	}
	updated := getNode(t, st, "WS", node.NodeID)
	assertPlacement(t, updated, domain.PlacementStateReleased, "")
	assertPlacement(t, result.Node, domain.PlacementStateActive, "sandbox-1")
	if result.Node.NodeID == node.NodeID {
		t.Fatalf("node id = %q, want successor row", result.Node.NodeID)
	}
	if got := provider.createCallCount(); got != 1 {
		t.Fatalf("Create calls = %d, want 1", got)
	}
}

func TestProvisionPtyNotFoundGetConfirmedMissingMarksLostNoRetry(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	startedAt := time.Now().UTC().Add(-10 * time.Minute)
	node := createPlacementNode(t, st, "WS", "lead-placement-dead", "nova", domain.NodePlacement{
		SandboxID:            "dead-sandbox",
		Generation:           1,
		ReservedVCPU:         2,
		ReservedMemGiB:       4,
		State:                domain.PlacementStateActive,
		LeadProcessStartedAt: &startedAt,
		SnapshotRef:          "snapshot://lead",
	})
	provider := &fakeProvider{startProcessErrs: []error{ErrSandboxNotFound, nil}}
	provider.addSandbox(ProviderSandbox{
		ID: "dead-sandbox",
		Labels: map[string]string{
			PlacementLabelKey:   node.NodeID,
			EnvironmentLabelKey: testDeploymentID,
			"loom-workspace":    "WS",
		},
		State: ProviderSandboxRunning,
	})
	broker := mustBrokerWithNow(t, st, provider, time.Now().UTC().Add(10*time.Minute))

	_, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Provision = %v, want ErrConflict", err)
	}
	assertStringSlicesEqual(t, provider.startProcessSandboxIDsSnapshot(), []string{"dead-sandbox"})
	assertPlacement(t, getNode(t, st, "WS", node.NodeID), domain.PlacementStateLost, "dead-sandbox")
	if got := provider.createCallCount(); got != 0 {
		t.Fatalf("Create calls = %d, want no replacement create", got)
	}
}

func TestProvisionRedrivesStuckReleasingThenCreates(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{deleteErr: errors.New("delete failed")}
	broker := mustBroker(t, st, provider)
	first := mustProvision(t, ctx, broker, "nova", 2, 4)
	if _, err := broker.Release(ctx, "WS", first.Node.NodeID, releaseFence(first.Node)); err == nil {
		t.Fatal("Release succeeded, want delete failure")
	}
	provider.deleteErr = nil
	provider.resetEvents()

	second := mustProvision(t, ctx, broker, "nova", 2, 4)
	if second.Node.NodeID == first.Node.NodeID {
		t.Fatalf("node id = %q, want new successor row", second.Node.NodeID)
	}
	events := provider.eventsSnapshot()
	if len(events) < 2 {
		t.Fatalf("provider events = %v, want delete then create", events)
	}
	assertStringSlicesEqual(t, events[:2], []string{"delete:sandbox-1", "create:sandbox-2"})
	assertPlacement(t, getNode(t, st, "WS", first.Node.NodeID), domain.PlacementStateReleased, first.Node.Placement.SandboxID)
}

func TestLeadProcessStartFailureLeavesActiveAndRetryRedrives(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{startProcessErrs: []error{errors.New("boot failed"), nil}}
	broker := mustBroker(t, st, provider)

	first := mustProvision(t, ctx, broker, "nova", 2, 4)
	assertPlacement(t, first.Node, domain.PlacementStateActive, "sandbox-1")
	if first.Node.Placement.LeadProcessStartedAt != nil {
		t.Fatalf("LeadProcessStartedAt = %v, want nil at spend boundary", first.Node.Placement.LeadProcessStartedAt)
	}
	if first.LeadStarted || first.LeadStartError == "" {
		t.Fatalf("lead boot result = started %v error %q, want surfaced boot failure", first.LeadStarted, first.LeadStartError)
	}
	if got := provider.createCallCount(); got != 1 {
		t.Fatalf("Create calls = %d, want 1", got)
	}
	if got := provider.startProcessCallCount(); got != 1 {
		t.Fatalf("StartProcess calls at spend boundary = %d, want 1", got)
	}

	result, err := broker.Provision(ctx, testProvisionRequest("nova", 2, 4))
	if err != nil {
		t.Fatalf("retry Provision: %v", err)
	}
	node := onlyNode(t, st, "WS")
	assertPlacement(t, node, domain.PlacementStateActive, "sandbox-1")
	if node.Placement.LeadProcessStartedAt == nil {
		t.Fatalf("LeadProcessStartedAt = nil, want observed start time")
	}
	if !result.LeadStarted || result.LeadStartError != "" {
		t.Fatalf("lead boot retry result = started %v error %q, want success", result.LeadStarted, result.LeadStartError)
	}
	if got := provider.createCallCount(); got != 1 {
		t.Fatalf("Create calls after retry = %d, want 1", got)
	}
	if got := provider.startProcessCallCount(); got != 2 {
		t.Fatalf("StartProcess calls after retry = %d, want 2", got)
	}
}

func TestListSumsLiveReservations(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	provider := &fakeProvider{}
	broker := mustBroker(t, st, provider)
	first := mustProvision(t, ctx, broker, "nova", 2, 4)
	_ = mustProvision(t, ctx, broker, "atlas", 1, 2)
	if _, err := broker.Release(ctx, "WS", first.Node.NodeID, releaseFence(first.Node)); err != nil {
		t.Fatalf("Release: %v", err)
	}

	list, err := broker.List(ctx, "WS")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Placements) != 2 {
		t.Fatalf("placements = %d, want 2", len(list.Placements))
	}
	if list.LiveReserved != (ResourceSize{VCPU: 1, MemGiB: 2}) {
		t.Fatalf("live reserved = %+v, want 1 vCPU / 2 GiB", list.LiveReserved)
	}
}

func TestLostCountsTowardQuota(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createPlacementNode(t, st, "WS", "lost-placement", "nova", domain.NodePlacement{
		SandboxID:      "lost-sandbox",
		Generation:     1,
		ReservedVCPU:   2,
		ReservedMemGiB: 4,
		State:          domain.PlacementStateLost,
		SnapshotRef:    "snapshot://lead",
	})
	provider := &fakeProvider{}
	broker := mustBrokerWithMax(t, st, provider, ResourceSize{VCPU: 2, MemGiB: 4})

	_, err := broker.Provision(ctx, testProvisionRequest("atlas", 1, 1))
	if !errors.Is(err, domain.ErrUnschedulable) {
		t.Fatalf("Provision = %v, want ErrUnschedulable", err)
	}
	if got := provider.createCallCount(); got != 0 {
		t.Fatalf("Create calls = %d, want 0", got)
	}
}

func TestConcurrentDifferentAgentProvisionDoesNotOvershootQuota(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	enteredCreate := make(chan struct{})
	releaseCreate := make(chan struct{})
	provider := &fakeProvider{
		createHook: func(string) {
			enteredCreate <- struct{}{}
			<-releaseCreate
		},
	}
	broker := mustBrokerWithMax(t, st, provider, ResourceSize{VCPU: 10, MemGiB: 20})

	firstErr := make(chan error, 1)
	go func() {
		_, err := broker.Provision(ctx, testProvisionRequest("nova", 6, 10))
		firstErr <- err
	}()
	select {
	case <-enteredCreate:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first create")
	}

	_, err := broker.Provision(ctx, testProvisionRequest("atlas", 6, 10))
	if !errors.Is(err, domain.ErrUnschedulable) {
		t.Fatalf("second Provision = %v, want ErrUnschedulable", err)
	}
	close(releaseCreate)
	if err := <-firstErr; err != nil {
		t.Fatalf("first Provision: %v", err)
	}
	if got := provider.createCallCount(); got != 1 {
		t.Fatalf("Create calls = %d, want only admitted placement", got)
	}
}

func TestQuotaSpansWorkspaces(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createWorkspace(t, st, "WS")
	createWorkspace(t, st, "OTHER")
	createPlacementNode(t, st, "OTHER", "other-placement", "atlas", domain.NodePlacement{
		SandboxID:      "other-sandbox",
		Generation:     1,
		ReservedVCPU:   2,
		ReservedMemGiB: 4,
		State:          domain.PlacementStateActive,
		SnapshotRef:    "snapshot://lead",
	})
	provider := &fakeProvider{}
	broker := mustBrokerWithMax(t, st, provider, ResourceSize{VCPU: 2, MemGiB: 4})

	_, err := broker.Provision(ctx, testProvisionRequest("nova", 1, 1))
	if !errors.Is(err, domain.ErrUnschedulable) {
		t.Fatalf("Provision = %v, want ErrUnschedulable", err)
	}
	if got := provider.createCallCount(); got != 0 {
		t.Fatalf("Create calls = %d, want 0", got)
	}
}

func TestQuotaIgnoresNonDaytonaPlacements(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	placement := &domain.NodePlacement{
		SandboxID:      "local-sandbox",
		Generation:     1,
		ReservedVCPU:   10,
		ReservedMemGiB: 10,
		State:          domain.PlacementStateActive,
		SnapshotRef:    "snapshot://local",
	}
	if _, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    "WS",
		NodeID:          "local-placement",
		OwnerActor:      agentOwnerActor("local"),
		RuntimeProvider: domain.RuntimeProviderLocal,
		Placement:       placement,
		DrainState:      domain.NodeDrainActive,
		TTL:             time.Hour,
	}); err != nil {
		t.Fatalf("create local placement: %v", err)
	}
	provider := &fakeProvider{}
	broker := mustBrokerWithMax(t, st, provider, ResourceSize{VCPU: 2, MemGiB: 4})

	result := mustProvision(t, ctx, broker, "nova", 2, 4)
	assertPlacement(t, result.Node, domain.PlacementStateActive, "sandbox-1")
}

func mustBroker(t *testing.T, st store.Store, provider *fakeProvider) *Broker {
	t.Helper()
	return mustBrokerWithMax(t, st, provider, ResourceSize{})
}

func mustBrokerWithMax(t *testing.T, st store.Store, provider *fakeProvider, max ResourceSize) *Broker {
	t.Helper()
	broker, err := NewBroker(Config{
		Store:        st,
		Provider:     provider,
		TokenKey:     testTokenKey,
		MaxLive:      max,
		DeploymentID: testDeploymentID,
		// The delete-confirm poll is real; keep its sleeps out of unit tests.
		DeleteConfirmBackoff: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}
	return broker
}

func mustBrokerWithNow(t *testing.T, st store.Store, provider *fakeProvider, now time.Time) *Broker {
	t.Helper()
	broker, err := NewBroker(Config{
		Store:                st,
		Provider:             provider,
		TokenKey:             testTokenKey,
		DeploymentID:         testDeploymentID,
		DeleteConfirmBackoff: time.Millisecond,
		Now:                  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}
	return broker
}

func testProvisionRequest(agent string, vcpu, memGiB int) ProvisionRequest {
	return ProvisionRequest{
		WorkspaceKey:           "WS",
		AgentName:              agent,
		SnapshotRef:            "snapshot://lead",
		Resource:               ResourceSize{VCPU: vcpu, MemGiB: memGiB},
		NetworkDomainAllowlist: []string{"api.loom.invalid"},
		Env:                    map[string]string{"CUSTOM": "value"},
		Labels:                 map[string]string{"custom": "value"},
	}
}

func mustProvision(t *testing.T, ctx context.Context, broker *Broker, agent string, vcpu, memGiB int) *ProvisionResult {
	t.Helper()
	result, err := broker.Provision(ctx, testProvisionRequest(agent, vcpu, memGiB))
	if err != nil {
		t.Fatalf("Provision %s: %v", agent, err)
	}
	return result
}

func releaseFence(node *domain.Node) ReleaseFence {
	return ReleaseFence{
		Generation: node.Placement.Generation,
		SandboxID:  node.Placement.SandboxID,
	}
}

func receiveResult(t *testing.T, results <-chan *ProvisionResult, errs <-chan error) *ProvisionResult {
	t.Helper()
	select {
	case err := <-errs:
		t.Fatalf("Provision error: %v", err)
	case result := <-results:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for provision result")
	}
	return nil
}

func receiveCreateEntered(t *testing.T, entered <-chan string) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for concurrent provider Create")
	}
}

func receiveState(t *testing.T, states <-chan domain.PlacementState) domain.PlacementState {
	t.Helper()
	select {
	case state := <-states:
		return state
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for provider Delete")
	}
	return ""
}

func parseCreateToken(t *testing.T, call CreateRequest) *leadtoken.OccupantClaims {
	t.Helper()
	token := call.Env[OccupantTokenEnv]
	if token == "" {
		t.Fatalf("create env missing %s", OccupantTokenEnv)
	}
	claims, err := leadtoken.ParseOccupantToken(token, testTokenKey)
	if err != nil {
		t.Fatalf("parse occupant token: %v", err)
	}
	return claims
}

func assertPlacement(t *testing.T, node *domain.Node, state domain.PlacementState, sandboxID string) {
	t.Helper()
	if node == nil || node.Placement == nil {
		t.Fatal("node placement missing")
	}
	if node.Placement.State != state || node.Placement.SandboxID != sandboxID {
		t.Fatalf("placement = state %q sandbox %q, want %q/%q",
			node.Placement.State, node.Placement.SandboxID, state, sandboxID)
	}
}

func assertStringSlicesEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slice = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("slice = %v, want %v", got, want)
		}
	}
}

func assertDurationSlicesEqual(t *testing.T, got, want []time.Duration) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("durations = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("durations = %v, want %v", got, want)
		}
	}
}

func getNodeState(t *testing.T, st store.Store, workspaceKey, nodeID string) domain.PlacementState {
	t.Helper()
	return getNode(t, st, workspaceKey, nodeID).Placement.State
}

func getNode(t *testing.T, st store.Store, workspaceKey, nodeID string) *domain.Node {
	t.Helper()
	node, err := st.Nodes().Get(context.Background(), workspaceKey, nodeID)
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	return node
}

func onlyNode(t *testing.T, st store.Store, workspaceKey string) *domain.Node {
	t.Helper()
	nodes, err := st.Nodes().List(context.Background(), workspaceKey)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}
	return nodes[0]
}

func createWorkspace(t *testing.T, st store.Store, key string) {
	t.Helper()
	if _, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: key, Name: key}); err != nil {
		t.Fatalf("create workspace %s: %v", key, err)
	}
}

func createRepo(t *testing.T, st store.Store, workspace, name, remoteURL, defaultBranch string) {
	t.Helper()
	if _, err := st.Repos().Create(context.Background(), store.RepoCreate{
		WorkspaceKey:  workspace,
		Name:          name,
		RemoteURL:     remoteURL,
		DefaultBranch: defaultBranch,
	}); err != nil {
		t.Fatalf("create repo %s/%s: %v", workspace, name, err)
	}
}

func createPlacementNode(t *testing.T, st store.Store, workspaceKey, nodeID, agentName string, placement domain.NodePlacement) *domain.Node {
	t.Helper()
	placementPtr := &placement
	node, err := st.Nodes().Create(context.Background(), store.NodeCreate{
		WorkspaceKey:    workspaceKey,
		NodeID:          nodeID,
		OwnerActor:      agentOwnerActor(agentName),
		RuntimeProvider: domain.RuntimeProviderDaytona,
		Placement:       placementPtr,
		Labels: []string{
			"loom-lead-placement",
			"loom-workspace=" + workspaceKey,
			"loom-agent=" + agentName,
		},
		Capabilities:  []string{CapLeadSession},
		ToolInventory: []string{"loom-lead"},
		DrainState:    domain.NodeDrainDrained,
		TTL:           defaultNodeTTL,
	})
	if err != nil {
		t.Fatalf("create placement node: %v", err)
	}
	return node
}

func markPlacementState(t *testing.T, st store.Store, workspaceKey, nodeID string, state domain.PlacementState) {
	t.Helper()
	node := getNode(t, st, workspaceKey, nodeID)
	placement := clonePlacement(node.Placement)
	placement.State = state
	placementPtr := &placement
	if _, err := st.Nodes().Update(context.Background(), workspaceKey, nodeID, store.NodeUpdate{Placement: &placementPtr}); err != nil {
		t.Fatalf("mark placement state: %v", err)
	}
}

func patchSandboxID(patch store.NodeUpdate) string {
	if patch.Placement == nil || *patch.Placement == nil {
		return ""
	}
	return strings.TrimSpace((*patch.Placement).SandboxID)
}

type storeWithNodes struct {
	store.Store
	nodes store.NodeStore
}

func (s storeWithNodes) Nodes() store.NodeStore {
	return s.nodes
}

type failingUpdateNodeStore struct {
	store.NodeStore
	fail func(context.Context, string, string, store.NodeUpdate) error
}

func (s failingUpdateNodeStore) Update(ctx context.Context, workspaceKey, nodeID string, patch store.NodeUpdate) (*domain.Node, error) {
	if s.fail != nil {
		if err := s.fail(ctx, workspaceKey, nodeID, patch); err != nil {
			return nil, err
		}
	}
	return s.NodeStore.Update(ctx, workspaceKey, nodeID, patch)
}

type fakeProvider struct {
	mu                     sync.Mutex
	createCalls            []CreateRequest
	prepCalls              []LeadBootPrep
	prepSandboxIDs         []string
	startProcessCalls      []ProcessSpec
	startProcessSandboxIDs []string
	deleteCalls            []string
	getCalls               []string
	ensureRunningCalls     []string
	listCalls              []map[string]string
	listPtySessionCalls    []string
	setAutostopCalls       []time.Duration
	events                 []string
	sandboxes              map[string]ProviderSandbox
	listOnlySandboxes      []ProviderSandbox
	ptySessions            map[string]map[string]PtySession
	createResult           *CreateResult
	createErr              error
	createCtxErr           error
	deleteErr              error
	prepErr                error
	getErr                 error
	getHook                func(string, int)
	ensureRunningErr       error
	listErr                error
	listPtySessionsErr     error
	setAutostopErr         error
	startProcessErr        error
	startProcessErrs       []error
	createDelay            time.Duration
	deleteLeavesSandbox    bool
	dropPtyAfterCreate     bool
	createHook             func(string)
	deleteHook             func(string)
}

func (f *fakeProvider) Create(ctx context.Context, req CreateRequest) (CreateResult, error) {
	if f.createDelay > 0 {
		time.Sleep(f.createDelay)
	}
	f.mu.Lock()
	f.createCalls = append(f.createCalls, cloneCreateRequest(req))
	result := CreateResult{}
	if f.createResult != nil {
		result = *f.createResult
	} else if f.createErr == nil {
		result.SandboxID = fmt.Sprintf("sandbox-%d", len(f.createCalls))
	}
	if result.SandboxID != "" {
		f.ensureSandboxesLocked()
		f.sandboxes[result.SandboxID] = ProviderSandbox{
			ID:     result.SandboxID,
			Labels: copyMap(req.Labels),
			State:  ProviderSandboxRunning,
		}
		f.events = append(f.events, "create:"+result.SandboxID)
	}
	hook := f.createHook
	err := f.createErr
	f.mu.Unlock()
	if hook != nil {
		hook(result.SandboxID)
	}
	f.mu.Lock()
	f.createCtxErr = ctx.Err()
	f.mu.Unlock()
	return result, err
}

func (f *fakeProvider) Get(_ context.Context, sandboxID string) (ProviderSandbox, error) {
	f.mu.Lock()
	f.getCalls = append(f.getCalls, sandboxID)
	call := len(f.getCalls)
	hook := f.getHook
	if f.getErr != nil {
		err := f.getErr
		f.mu.Unlock()
		if hook != nil {
			hook(sandboxID, call)
		}
		return ProviderSandbox{}, err
	}
	sandbox, ok := f.sandboxes[sandboxID]
	f.mu.Unlock()
	if hook != nil {
		hook(sandboxID, call)
	}
	if !ok {
		return ProviderSandbox{}, ErrSandboxNotFound
	}
	return cloneProviderSandbox(sandbox), nil
}

func (f *fakeProvider) EnsureRunning(_ context.Context, sandboxID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureRunningCalls = append(f.ensureRunningCalls, sandboxID)
	f.events = append(f.events, "ensureRunning:"+sandboxID)
	if f.ensureRunningErr != nil {
		return false, f.ensureRunningErr
	}
	sandbox, ok := f.sandboxes[sandboxID]
	if !ok || sandbox.State == ProviderSandboxAbsent {
		return false, ErrSandboxNotFound
	}
	if sandbox.State != ProviderSandboxStopped {
		return false, nil
	}
	sandbox.State = ProviderSandboxRunning
	f.sandboxes[sandboxID] = sandbox
	delete(f.ptySessions, sandboxID)
	return true, nil
}

func (f *fakeProvider) Delete(_ context.Context, sandboxID string) error {
	if f.deleteHook != nil {
		f.deleteHook(sandboxID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls = append(f.deleteCalls, sandboxID)
	f.events = append(f.events, "delete:"+sandboxID)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if !f.deleteLeavesSandbox {
		delete(f.sandboxes, sandboxID)
		delete(f.ptySessions, sandboxID)
	}
	return nil
}

func (f *fakeProvider) UpdateLastActivity(context.Context, string) error {
	return nil
}

func (f *fakeProvider) SetAutostopInterval(_ context.Context, sandboxID string, interval time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setAutostopErr != nil {
		return f.setAutostopErr
	}
	if _, ok := f.sandboxes[sandboxID]; !ok {
		return ErrSandboxNotFound
	}
	f.setAutostopCalls = append(f.setAutostopCalls, interval)
	f.events = append(f.events, "autostop:"+interval.String())
	return nil
}

func (f *fakeProvider) PrepareLeadBoot(_ context.Context, sandboxID string, prep LeadBootPrep) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepSandboxIDs = append(f.prepSandboxIDs, sandboxID)
	f.prepCalls = append(f.prepCalls, cloneLeadBootPrep(prep))
	f.events = append(f.events, "prep:"+sandboxID)
	if f.prepErr != nil {
		return f.prepErr
	}
	if _, ok := f.sandboxes[sandboxID]; !ok {
		return ErrSandboxNotFound
	}
	return nil
}

func (f *fakeProvider) CreatePty(_ context.Context, sandboxID string, spec ProcessSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startProcessCalls = append(f.startProcessCalls, cloneProcessSpec(spec))
	f.startProcessSandboxIDs = append(f.startProcessSandboxIDs, sandboxID)
	f.events = append(f.events, "startProcess:"+sandboxID)
	if len(f.startProcessErrs) > 0 {
		err := f.startProcessErrs[0]
		f.startProcessErrs = f.startProcessErrs[1:]
		if err != nil {
			if errors.Is(err, ErrSandboxNotFound) {
				delete(f.sandboxes, sandboxID)
				delete(f.ptySessions, sandboxID)
			}
			if errors.Is(err, ErrPtySessionAlreadyExists) {
				f.addPtySessionLocked(sandboxID, LeadPTYSessionID)
			}
			return err
		}
	}
	if f.startProcessErr != nil {
		if errors.Is(f.startProcessErr, ErrSandboxNotFound) {
			delete(f.sandboxes, sandboxID)
			delete(f.ptySessions, sandboxID)
		}
		if errors.Is(f.startProcessErr, ErrPtySessionAlreadyExists) {
			f.addPtySessionLocked(sandboxID, LeadPTYSessionID)
		}
		return f.startProcessErr
	}
	if _, ok := f.sandboxes[sandboxID]; !ok {
		return ErrSandboxNotFound
	}
	if f.dropPtyAfterCreate {
		return nil
	}
	sessionID := strings.TrimSpace(spec.SessionID)
	if sessionID == "" {
		sessionID = LeadPTYSessionID
	}
	if f.addPtySessionLocked(sandboxID, sessionID) {
		return ErrPtySessionAlreadyExists
	}
	return nil
}

func (f *fakeProvider) ListManaged(_ context.Context, labels map[string]string) ([]ProviderSandbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls = append(f.listCalls, copyMap(labels))
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]ProviderSandbox, 0, len(f.sandboxes)+len(f.listOnlySandboxes))
	for _, sandbox := range f.sandboxes {
		if !providerSandboxHasLabels(sandbox, labels) {
			continue
		}
		out = append(out, cloneProviderSandbox(sandbox))
	}
	for _, sandbox := range f.listOnlySandboxes {
		if providerSandboxHasLabels(sandbox, labels) {
			out = append(out, cloneProviderSandbox(sandbox))
		}
	}
	return out, nil
}

func (f *fakeProvider) ListPtySessions(_ context.Context, sandboxID string) ([]PtySession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listPtySessionCalls = append(f.listPtySessionCalls, sandboxID)
	if f.listPtySessionsErr != nil {
		return nil, f.listPtySessionsErr
	}
	if _, ok := f.sandboxes[sandboxID]; !ok {
		return nil, ErrSandboxNotFound
	}
	out := make([]PtySession, 0, len(f.ptySessions[sandboxID]))
	for _, session := range f.ptySessions[sandboxID] {
		out = append(out, session)
	}
	return out, nil
}

func (f *fakeProvider) KillPtySession(_ context.Context, sandboxID, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sandboxes[sandboxID]; !ok {
		return ErrSandboxNotFound
	}
	delete(f.ptySessions[sandboxID], sessionID)
	return nil
}

func (f *fakeProvider) addSandbox(sandbox ProviderSandbox) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureSandboxesLocked()
	f.sandboxes[sandbox.ID] = cloneProviderSandbox(sandbox)
}

func (f *fakeProvider) addListOnlySandbox(sandbox ProviderSandbox) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listOnlySandboxes = append(f.listOnlySandboxes, cloneProviderSandbox(sandbox))
}

func (f *fakeProvider) addPtySession(sandboxID, sessionID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addPtySessionLocked(sandboxID, sessionID)
}

func (f *fakeProvider) addPtySessionLocked(sandboxID, sessionID string) bool {
	f.ensurePtySessionsLocked()
	if f.ptySessions[sandboxID] == nil {
		f.ptySessions[sandboxID] = make(map[string]PtySession)
	}
	if _, ok := f.ptySessions[sandboxID][sessionID]; ok {
		return true
	}
	f.ptySessions[sandboxID][sessionID] = PtySession{SessionID: sessionID}
	return false
}

func (f *fakeProvider) resetEvents() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = nil
}

func (f *fakeProvider) createCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.createCalls)
}

func (f *fakeProvider) prepCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.prepCalls)
}

func (f *fakeProvider) deleteCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.deleteCalls)
}

func (f *fakeProvider) startProcessCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.startProcessCalls)
}

func (f *fakeProvider) listCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.listCalls)
}

func (f *fakeProvider) listPtySessionCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.listPtySessionCalls)
}

func (f *fakeProvider) getCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.getCalls)
}

func (f *fakeProvider) ensureRunningCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.ensureRunningCalls)
}

func (f *fakeProvider) createContextErr() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createCtxErr
}

func (f *fakeProvider) createCall(t *testing.T, idx int) CreateRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.createCalls) <= idx {
		t.Fatalf("create calls = %d, want index %d", len(f.createCalls), idx)
	}
	return cloneCreateRequest(f.createCalls[idx])
}

func (f *fakeProvider) startProcessCall(t *testing.T, idx int) ProcessSpec {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.startProcessCalls) <= idx {
		t.Fatalf("start process calls = %d, want index %d", len(f.startProcessCalls), idx)
	}
	return cloneProcessSpec(f.startProcessCalls[idx])
}

func (f *fakeProvider) prepCall(t *testing.T, idx int) LeadBootPrep {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.prepCalls) <= idx {
		t.Fatalf("prep calls = %d, want index %d", len(f.prepCalls), idx)
	}
	return cloneLeadBootPrep(f.prepCalls[idx])
}

func (f *fakeProvider) deleteCallsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleteCalls...)
}

func (f *fakeProvider) eventsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

func (f *fakeProvider) setAutostopCallsSnapshot() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.setAutostopCalls...)
}

func (f *fakeProvider) startProcessSandboxIDsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.startProcessSandboxIDs...)
}

func (f *fakeProvider) ensureSandboxesLocked() {
	if f.sandboxes == nil {
		f.sandboxes = make(map[string]ProviderSandbox)
	}
}

func (f *fakeProvider) ensurePtySessionsLocked() {
	if f.ptySessions == nil {
		f.ptySessions = make(map[string]map[string]PtySession)
	}
}

func cloneCreateRequest(in CreateRequest) CreateRequest {
	in.Labels = copyMap(in.Labels)
	in.Env = copyMap(in.Env)
	in.NetworkDomainAllowlist = append([]string(nil), in.NetworkDomainAllowlist...)
	return in
}

func cloneProcessSpec(in ProcessSpec) ProcessSpec {
	in.Command = append([]string(nil), in.Command...)
	in.Env = copyMap(in.Env)
	return in
}

func countPromptArgs(command []string) int {
	count := 0
	for _, arg := range command {
		if strings.TrimSpace(arg) == "--prompt" || strings.HasPrefix(strings.TrimSpace(arg), "--prompt=") {
			count++
		}
	}
	return count
}

func cloneLeadBootPrep(in LeadBootPrep) LeadBootPrep {
	out := in
	if in.Repo != nil {
		repo := *in.Repo
		out.Repo = &repo
	}
	return out
}

func cloneProviderSandbox(in ProviderSandbox) ProviderSandbox {
	in.Labels = copyMap(in.Labels)
	return in
}

func providerSandboxHasLabels(sandbox ProviderSandbox, labels map[string]string) bool {
	for key, value := range labels {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if sandbox.Labels[key] != value {
			return false
		}
	}
	return true
}
