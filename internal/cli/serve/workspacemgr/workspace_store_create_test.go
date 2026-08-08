package workspacemgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/gitbranch"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"
	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

type testRepositoryMaterializer struct {
	infrafleetdb.RepositoryAdmissionTransport
	store                    store.Store
	mu                       sync.Mutex
	commands                 []sourcecontrol.RepositoryAdmissionCheckoutCommand
	prepared                 []testPreparedRepositoryCheckout
	records                  map[string]*infrafleetdb.RepositoryAdmissionRecord
	byOp                     map[string]string
	creates                  map[string]bool
	nextID                   int
	beginLeases              []time.Duration
	renewals                 int
	recoveryClaims           int
	now                      func() time.Time
	failNextRepositoryRef    string
	failNextMaterializeError error
	blockNextRepositoryRef   string
	blockMaterializeStarted  chan struct{}
	blockMaterializeCanceled chan struct{}
	blockMaterializeRelease  chan struct{}
}

type testPreparedRepositoryCheckout struct {
	command sourcecontrol.RepositoryAdmissionCheckoutCommand
	receipt sourcecontrol.PreparedRepositoryCheckout
}

func (materializer *testRepositoryMaterializer) PrepareRepositoryAdmissionCheckout(
	ctx context.Context,
	command sourcecontrol.RepositoryAdmissionCheckoutCommand,
) (*sourcecontrol.PreparedRepositoryCheckout, error) {
	if materializer == nil || materializer.store == nil {
		return nil, sourcecontrol.ErrUnavailable
	}
	materializer.mu.Lock()
	materializer.commands = append(materializer.commands, command)
	record := materializer.records[command.AdmissionID]
	if command.RepositoryRef == materializer.blockNextRepositoryRef {
		materializer.blockNextRepositoryRef = ""
		started := materializer.blockMaterializeStarted
		canceled := materializer.blockMaterializeCanceled
		release := materializer.blockMaterializeRelease
		materializer.mu.Unlock()
		if started != nil {
			close(started)
		}
		<-ctx.Done()
		if canceled != nil {
			close(canceled)
		}
		if release != nil {
			<-release
		}
		return nil, context.Cause(ctx)
	}
	if command.RepositoryRef == materializer.failNextRepositoryRef {
		err := materializer.failNextMaterializeError
		materializer.failNextRepositoryRef = ""
		materializer.failNextMaterializeError = nil
		materializer.mu.Unlock()
		if err == nil {
			err = sourcecontrol.ErrUnavailable
		}
		return nil, err
	}
	if record == nil ||
		record.State != "pending" ||
		record.WorkspaceKey != command.WorkspaceKey ||
		record.OwnerID != command.OwnerID ||
		record.OwnerGenerationID != command.OwnerGenerationID ||
		record.SpecFingerprint != command.SpecFingerprint ||
		!record.OwnerLeaseExpiresAt.After(materializer.currentTime()) {
		materializer.mu.Unlock()
		return nil, sourcecontrol.ErrRepositoryAdmissionNotFound
	}
	materializer.mu.Unlock()
	var spec *infrafleetdb.RepositoryAdmissionRepoSpec
	for index := range record.Spec.Repositories {
		candidate := &record.Spec.Repositories[index]
		if candidate.Name == command.RepositoryRef ||
			candidate.SourceRepoID == command.RepositoryRef {
			spec = candidate
			break
		}
	}
	if spec == nil {
		return nil, sourcecontrol.ErrRepositoryAdmissionNotFound
	}
	stateCache, err := bootstrap.LoadStateCache()
	if err != nil {
		return nil, err
	}
	workspacePath := stateCache.Workspaces[command.WorkspaceKey].Path
	targetPath := filepath.Join(workspacePath, spec.Name)
	reused := false
	if _, err := os.Stat(filepath.Join(targetPath, ".git")); err == nil {
		reused = true
	} else {
		clone := exec.CommandContext(ctx, "git", "clone", "--", spec.RemoteURL, targetPath) //nolint:norawexec // test fixture.
		if output, err := clone.CombinedOutput(); err != nil {
			return nil, errors.New(strings.TrimSpace(string(output)) + ": " + err.Error())
		}
	}
	receipt := sourcecontrol.PreparedRepositoryCheckout{
		WorkspaceKey:  command.WorkspaceKey,
		AdmissionID:   command.AdmissionID,
		RepositoryRef: command.RepositoryRef,
		CheckoutPath:  targetPath,
		Reused:        reused,
	}
	materializer.mu.Lock()
	materializer.prepared = append(
		materializer.prepared,
		testPreparedRepositoryCheckout{command: command, receipt: receipt},
	)
	materializer.mu.Unlock()
	return &receipt, nil
}

func sourceControlFor(store store.Store) *testRepositoryMaterializer {
	return &testRepositoryMaterializer{
		store: store, records: make(map[string]*infrafleetdb.RepositoryAdmissionRecord),
		byOp: make(map[string]string), creates: make(map[string]bool),
	}
}

func (materializer *testRepositoryMaterializer) currentTime() time.Time {
	if materializer.now != nil {
		return materializer.now().UTC()
	}
	return time.Now().UTC()
}

func (materializer *testRepositoryMaterializer) CreateWorkspaceWithRepositoryAdmission(
	ctx context.Context,
	input infrafleetdb.WorkspaceRepositoryAdmissionBeginInput,
) (*infrafleetdb.WorkspaceRepositoryAdmissionBeginResult, error) {
	workspace, err := materializer.store.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key: input.Workspace.Key, Name: input.Workspace.Name,
		Description:   input.Workspace.Description,
		DefaultBranch: input.Workspace.DefaultBranch,
		DesignFormat:  input.Workspace.DesignFormat,
	})
	if err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return nil, infrafleetdb.ErrRepositoryAdmissionConflict
		}
		return nil, err
	}
	state := workspacemodule.State(input.Workspace.State)
	if state != "" {
		workspace, err = materializer.store.Workspaces().Update(
			ctx,
			input.Workspace.Key,
			store.WorkspaceUpdate{State: &state},
		)
		if err != nil {
			return nil, err
		}
	}
	record, err := materializer.begin(
		input.Workspace.Key,
		input.OperationID,
		input.OwnerID,
		input.OwnerLease,
		input.Repositories,
		true,
	)
	if err != nil {
		return nil, err
	}
	return &infrafleetdb.WorkspaceRepositoryAdmissionBeginResult{
		Workspace: workspace, Admission: record, WorkspaceEventID: "workspace-event",
	}, nil
}

func (materializer *testRepositoryMaterializer) BeginRepositoryAdmission(
	_ context.Context,
	workspace string,
	input infrafleetdb.RepositoryAdmissionBeginInput,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	return materializer.begin(
		workspace,
		input.OperationID,
		input.OwnerID,
		input.OwnerLease,
		input.Repositories,
		false,
	)
}

func (materializer *testRepositoryMaterializer) begin(
	workspace,
	operationID,
	ownerID string,
	ownerLease time.Duration,
	repositories []infrafleetdb.RepositoryAdmissionRepoSpec,
	createsWorkspace bool,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	materializer.mu.Lock()
	defer materializer.mu.Unlock()
	if existingID := materializer.byOp[operationID]; existingID != "" {
		return cloneTestAdmission(materializer.records[existingID]), nil
	}
	materializer.nextID++
	admissionID := fmt.Sprintf("%032x", materializer.nextID)
	ownerGenerationID := fmt.Sprintf("%032x", materializer.nextID+1000)
	canonical := append([]infrafleetdb.RepositoryAdmissionRepoSpec(nil), repositories...)
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Name < canonical[j].Name })
	encoded, _ := json.Marshal(canonical)
	sum := sha256.Sum256(encoded)
	now := materializer.currentTime()
	if ownerLease == 0 {
		ownerLease = repositoryAdmissionLease
	}
	record := &infrafleetdb.RepositoryAdmissionRecord{
		AdmissionID: admissionID, WorkspaceKey: workspace,
		OperationID: operationID, OwnerID: ownerID,
		OwnerGenerationID:   ownerGenerationID,
		OwnerLeaseExpiresAt: now.Add(ownerLease),
		SpecFingerprint:     "sha256:" + hex.EncodeToString(sum[:]),
		Spec: infrafleetdb.RepositoryAdmissionSpec{
			WorkspaceKey: workspace, OperationID: operationID,
			Repositories: canonical,
		},
		State: "pending", Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	materializer.records[admissionID] = record
	materializer.byOp[operationID] = admissionID
	materializer.creates[admissionID] = createsWorkspace
	materializer.beginLeases = append(materializer.beginLeases, ownerLease)
	return cloneTestAdmission(record), nil
}

func (materializer *testRepositoryMaterializer) GetRepositoryAdmission(
	_ context.Context,
	_ string,
	admissionID string,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	materializer.mu.Lock()
	defer materializer.mu.Unlock()
	record := materializer.records[admissionID]
	if record == nil {
		return nil, infrafleetdb.ErrRepositoryAdmissionNotFound
	}
	return cloneTestAdmission(record), nil
}

func (materializer *testRepositoryMaterializer) GetRepositoryAdmissionByOperation(
	_ context.Context,
	_ string,
	operationID string,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	materializer.mu.Lock()
	defer materializer.mu.Unlock()
	record := materializer.records[materializer.byOp[operationID]]
	if record == nil {
		return nil, infrafleetdb.ErrRepositoryAdmissionNotFound
	}
	result := cloneTestAdmission(record)
	result.OwnerID = ""
	result.OwnerGenerationID = ""
	return result, nil
}

func (materializer *testRepositoryMaterializer) ListRecoverableRepositoryAdmissions(
	_ context.Context,
	workspace string,
	limit int,
) ([]*infrafleetdb.RepositoryAdmissionRecord, error) {
	materializer.mu.Lock()
	defer materializer.mu.Unlock()
	result := make([]*infrafleetdb.RepositoryAdmissionRecord, 0)
	for _, record := range materializer.records {
		if record.WorkspaceKey != workspace ||
			(record.State != "retryable_failed" &&
				record.State != "pending") {
			continue
		}
		result = append(result, cloneTestAdmission(record))
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].AdmissionID < result[j].AdmissionID
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (materializer *testRepositoryMaterializer) RenewRepositoryAdmission(
	_ context.Context,
	input infrafleetdb.RepositoryAdmissionRenewInput,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	materializer.mu.Lock()
	defer materializer.mu.Unlock()
	record := materializer.records[input.AdmissionID]
	if record == nil {
		return nil, infrafleetdb.ErrRepositoryAdmissionNotFound
	}
	now := materializer.currentTime()
	if record.State != "pending" ||
		record.WorkspaceKey != input.WorkspaceKey ||
		record.OwnerID != input.OwnerID ||
		record.OwnerGenerationID != input.OwnerGenerationID ||
		record.SpecFingerprint != input.SpecFingerprint ||
		record.Version != input.ExpectedVersion ||
		now.After(record.OwnerLeaseExpiresAt) {
		return nil, infrafleetdb.ErrRepositoryAdmissionFenceLost
	}
	record.Version++
	record.UpdatedAt = now
	record.OwnerLeaseExpiresAt = now.Add(input.Lease)
	materializer.renewals++
	return cloneTestAdmission(record), nil
}

func (materializer *testRepositoryMaterializer) ClaimRepositoryAdmissionRecovery(
	_ context.Context,
	input infrafleetdb.RepositoryAdmissionRecoveryClaimInput,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	materializer.mu.Lock()
	defer materializer.mu.Unlock()
	record := materializer.records[input.AdmissionID]
	if record == nil {
		return nil, infrafleetdb.ErrRepositoryAdmissionNotFound
	}
	now := materializer.currentTime()
	if record.WorkspaceKey != input.WorkspaceKey ||
		record.SpecFingerprint != input.ExpectedSpecFingerprint ||
		record.Version != input.ExpectedVersion ||
		(record.State != "retryable_failed" &&
			(record.State != "pending" ||
				now.Before(record.OwnerLeaseExpiresAt))) {
		return nil, infrafleetdb.ErrRepositoryAdmissionFenceLost
	}
	materializer.recoveryClaims++
	record.OwnerID = input.NewOwnerID
	record.OwnerGenerationID = fmt.Sprintf(
		"%032x",
		materializer.nextID+2000+materializer.recoveryClaims,
	)
	record.OwnerLeaseExpiresAt = now.Add(input.Lease)
	record.State = "pending"
	record.LastErrorClass = ""
	record.Version++
	record.UpdatedAt = now
	return cloneTestAdmission(record), nil
}

func (materializer *testRepositoryMaterializer) CommitRepositoryAdmission(
	ctx context.Context,
	input infrafleetdb.RepositoryAdmissionCommitInput,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	materializer.mu.Lock()
	record := materializer.records[input.AdmissionID]
	if record == nil {
		materializer.mu.Unlock()
		return nil, infrafleetdb.ErrRepositoryAdmissionNotFound
	}
	if record.Version != input.ExpectedVersion ||
		record.OwnerID != input.OwnerID ||
		record.OwnerGenerationID != input.OwnerGenerationID ||
		record.SpecFingerprint != input.SpecFingerprint ||
		materializer.currentTime().After(record.OwnerLeaseExpiresAt) {
		materializer.mu.Unlock()
		return nil, infrafleetdb.ErrRepositoryAdmissionFenceLost
	}
	resolved := make(map[string]string, len(input.ResolvedDefaultBranches))
	for _, branch := range input.ResolvedDefaultBranches {
		resolved[branch.Name] = branch.DefaultBranch
	}
	specs := append([]infrafleetdb.RepositoryAdmissionRepoSpec(nil), record.Spec.Repositories...)
	materializer.mu.Unlock()

	receipts := make([]infrafleetdb.RepositoryAdmissionRepoReceipt, 0, len(specs))
	for _, spec := range specs {
		repository, err := materializer.store.Repos().Create(ctx, store.RepoCreate{
			WorkspaceKey: record.WorkspaceKey, Name: spec.Name,
			RemoteURL: spec.RemoteURL, Remote: spec.Remote,
			DefaultBranch: resolved[spec.Name], Groups: spec.Groups,
			SourceRepoID: spec.SourceRepoID,
		})
		if errors.Is(err, domain.ErrAlreadyExists) {
			return nil, infrafleetdb.ErrRepositoryAdmissionConflict
		}
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, infrafleetdb.RepositoryAdmissionRepoReceipt{
			Repository: *repository, EventID: "repo-event-" + spec.Name,
		})
	}
	if input.WorkspaceFinalization != nil {
		state := workspacemodule.StateReady
		branch := input.WorkspaceFinalization.DefaultBranch
		empty := ""
		if _, err := materializer.store.Workspaces().Update(
			ctx,
			record.WorkspaceKey,
			store.WorkspaceUpdate{
				State: &state, DefaultBranch: &branch, ErrorMessage: &empty,
			},
		); err != nil {
			return nil, err
		}
	}
	materializer.mu.Lock()
	defer materializer.mu.Unlock()
	record = materializer.records[input.AdmissionID]
	now := materializer.currentTime()
	record.State = "committed"
	record.Version++
	record.UpdatedAt = now
	record.TerminalAt = &now
	record.Receipt = &infrafleetdb.RepositoryAdmissionReceipt{
		AdmissionID: record.AdmissionID, SpecFingerprint: record.SpecFingerprint,
		Repositories: receipts, WorkspaceFinalization: input.WorkspaceFinalization,
		CommittedAt: now,
	}
	return cloneTestAdmission(record), nil
}

func (materializer *testRepositoryMaterializer) FailRepositoryAdmission(
	ctx context.Context,
	input infrafleetdb.RepositoryAdmissionFailInput,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	materializer.mu.Lock()
	record := materializer.records[input.AdmissionID]
	if record == nil {
		materializer.mu.Unlock()
		return nil, infrafleetdb.ErrRepositoryAdmissionNotFound
	}
	now := materializer.currentTime()
	if record.Version != input.ExpectedVersion ||
		record.OwnerID != input.OwnerID ||
		record.OwnerGenerationID != input.OwnerGenerationID ||
		record.SpecFingerprint != input.SpecFingerprint ||
		now.After(record.OwnerLeaseExpiresAt) {
		materializer.mu.Unlock()
		return nil, infrafleetdb.ErrRepositoryAdmissionFenceLost
	}
	record.Version++
	record.UpdatedAt = now
	record.LastErrorClass = input.ErrorClass
	if input.Retryable {
		record.State = "retryable_failed"
	} else {
		record.State = "permanent_failed"
		record.TerminalAt = &now
	}
	createsWorkspace := materializer.creates[input.AdmissionID]
	result := cloneTestAdmission(record)
	materializer.mu.Unlock()
	if createsWorkspace && !input.Retryable {
		state := workspacemodule.StateError
		message := input.ErrorClass
		_, _ = materializer.store.Workspaces().Update(
			ctx,
			record.WorkspaceKey,
			store.WorkspaceUpdate{State: &state, ErrorMessage: &message},
		)
	}
	return result, nil
}

func cloneTestAdmission(
	record *infrafleetdb.RepositoryAdmissionRecord,
) *infrafleetdb.RepositoryAdmissionRecord {
	if record == nil {
		return nil
	}
	encoded, _ := json.Marshal(record)
	var result infrafleetdb.RepositoryAdmissionRecord
	_ = json.Unmarshal(encoded, &result)
	return &result
}

func buildTestAddReposWithAdmission(
	t *testing.T,
	st store.Store,
	materializer *testRepositoryMaterializer,
) workspacecoord.WorkspaceAddReposFn {
	t.Helper()
	journal, err := newLocalRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "admissions"),
		time.Now,
	)
	if err != nil {
		t.Fatal(err)
	}
	return BuildStoreBackedAddReposWithAdmission(
		st,
		materializer,
		journal,
		materializer,
	)
}

func buildTestCreateWithAdmission(
	t *testing.T,
	st store.Store,
	materializer *testRepositoryMaterializer,
) workspacecoord.WorkspaceCreateFn {
	t.Helper()
	journal, err := newLocalRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "admissions"),
		time.Now,
	)
	if err != nil {
		t.Fatal(err)
	}
	return BuildStoreBackedCreateWorkspaceWithAdmission(
		st,
		materializer,
		journal,
		materializer,
	)
}

func TestStoreBackedCreateEmptyWorkspaceCreatesStoreAndLocalState(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "app")
	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")

	result, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name:   "my-ws",
		Type:   "empty",
		Repos:  []string{src},
		Branch: "feature-work",
		Path:   wsPath,
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if result.WorkspaceID != "MY-WS" {
		t.Fatalf("WorkspaceID = %q, want MY-WS", result.WorkspaceID)
	}
	if result.WorkspacePath != wsPath {
		t.Fatalf("WorkspacePath = %q, want %q", result.WorkspacePath, wsPath)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "app", ".git")); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}

	ws, err := st.Workspaces().Get(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("workspace not stored: %v", err)
	}
	if ws.Name != "my-ws" {
		t.Fatalf("workspace name = %q, want my-ws", ws.Name)
	}
	repos, err := st.Repos().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "app" {
		t.Fatalf("repos = %#v, want app", repos)
	}
	roles, err := st.Roles().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	if len(roles) != 3 || !hasRole(roles, "plan") || !hasRole(roles, "task") || !hasRole(roles, "lead") {
		t.Fatalf("roles = %#v, want plan, task, and lead", roles)
	}
	roleByName := rolesByName(roles)
	if roleByName["plan"].TaskFilter != "needs_plan" {
		t.Fatalf("plan task filter = %q, want needs_plan", roleByName["plan"].TaskFilter)
	}
	if roleByName["task"].TaskFilter != "has_design" {
		t.Fatalf("task task filter = %q, want has_design", roleByName["task"].TaskFilter)
	}
	if roleByName["lead"].Kind != domain.RoleKindInteractive {
		t.Fatalf("lead kind = %q, want interactive", roleByName["lead"].Kind)
	}

	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if sc.LastWorkspace != "MY-WS" {
		t.Fatalf("LastWorkspace = %q, want MY-WS", sc.LastWorkspace)
	}
	local := sc.Workspaces["MY-WS"]
	if local.Path != wsPath {
		t.Fatalf("local path = %q, want %q", local.Path, wsPath)
	}
	if local.Repos["app"] != filepath.Join(wsPath, "app") {
		t.Fatalf("local repo path = %q", local.Repos["app"])
	}
}

func TestStoreBackedCreateEmptyWorkspaceRejectsCredentialBearingLocalRemote(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "private-local")
	runGit(
		t,
		src,
		"remote",
		"add",
		"origin",
		"https://operator:plaintext-secret@example.test/private.git",
	)
	st := memstore.New()
	wsPath := filepath.Join(loomDir, "workspaces", "private-ws")
	_, err := BuildStoreBackedCreateWorkspace(st)(
		t.Context(),
		workspacecoord.WorkspaceCreateRequest{
			Name:  "private-ws",
			Type:  "empty",
			Repos: []string{src},
			Path:  wsPath,
		},
	)
	var createErr *workspaceerrors.CreateError
	if !errors.As(err, &createErr) ||
		createErr.Code != workspaceerrors.SecurityViolation ||
		!errors.Is(err, sourcecontrol.ErrInvalid) {
		t.Fatalf("credential-bearing local remote error = %v, want security violation", err)
	}
	if strings.Contains(err.Error(), "plaintext-secret") {
		t.Fatalf("credential-bearing local remote leaked secret in error: %v", err)
	}
	if _, getErr := st.Workspaces().Get(t.Context(), "PRIVATE-WS"); !errors.Is(getErr, domain.ErrNotFound) {
		t.Fatalf("invalid local remote persisted workspace: %v", getErr)
	}
	if _, statErr := os.Stat(wsPath); !os.IsNotExist(statErr) {
		t.Fatalf("invalid local remote left workspace checkout: %v", statErr)
	}
}

func TestStoreBackedCreateEmptyWorkspaceAllowsExternalEmptyPath(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	externalPath := filepath.Join(t.TempDir(), "picked-workspace")
	if err := os.MkdirAll(externalPath, 0755); err != nil {
		t.Fatalf("mkdir external path: %v", err)
	}

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)

	result, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name: "external-ws",
		Type: "empty",
		Path: externalPath,
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if result.WorkspaceID != "EXTERNAL-WS" || result.WorkspacePath != externalPath {
		t.Fatalf("result = %#v, want EXTERNAL-WS at %s", result, externalPath)
	}

	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if sc.Workspaces["EXTERNAL-WS"].Path != externalPath {
		t.Fatalf("local path = %q, want %q", sc.Workspaces["EXTERNAL-WS"].Path, externalPath)
	}
}

func TestStoreBackedCreateWorkspaceRejectsExternalNonEmptyPath(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	externalPath := filepath.Join(t.TempDir(), "documents")
	if err := os.MkdirAll(externalPath, 0755); err != nil {
		t.Fatalf("mkdir external path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(externalPath, "keep.txt"), []byte("do not remove\n"), 0644); err != nil {
		t.Fatalf("write external file: %v", err)
	}

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)

	_, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name: "external-ws",
		Type: "empty",
		Path: externalPath,
	})
	if err == nil {
		t.Fatal("create workspace succeeded, want non-empty path validation error")
	}
	if _, statErr := os.Stat(filepath.Join(externalPath, "keep.txt")); statErr != nil {
		t.Fatalf("non-empty external path was modified, stat err=%v", statErr)
	}
}

func TestAddWorktreesRecoversCorruptBranchRef(t *testing.T) {
	src := initTestGitRepo(t, t.TempDir(), "app")
	baseBranch := strings.TrimSpace(gitOutput(t, src, "branch", "--show-current"))
	runGit(t, src, "checkout", "-b", "local-coder")
	if err := os.WriteFile(filepath.Join(src, "agent.txt"), []byte("agent\n"), 0o644); err != nil {
		t.Fatalf("write agent file: %v", err)
	}
	runGit(t, src, "add", "agent.txt")
	runGit(t, src, "commit", "-m", "agent")
	agentSHA := strings.TrimSpace(gitOutput(t, src, "rev-parse", "HEAD"))
	runGit(t, src, "checkout", baseBranch)
	corruptWorkspaceBranchRef(t, src, "local-coder")

	wsDir := filepath.Join(t.TempDir(), "workspace")
	ctx := workspacecoord.WithCreateWarnings(context.Background())
	created, repos, err := addWorktrees(ctx, []resolvedRepo{{path: src, name: "app"}}, wsDir, "local-coder")
	if err != nil {
		t.Fatalf("addWorktrees: %v", err)
	}
	if len(created) != 1 || len(repos) != 1 {
		t.Fatalf("created=%d repos=%d, want one each", len(created), len(repos))
	}
	if warnings := workspacecoord.GetCreateWarnings(ctx); len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if got := strings.TrimSpace(gitOutput(t, filepath.Join(wsDir, "app"), "rev-parse", "HEAD")); got != agentSHA {
		t.Fatalf("worktree HEAD = %s, want recovered reflog SHA %s", got, agentSHA)
	}
}

func TestAddWorktreesSkipsUnrecoverableCheckoutWithWarning(t *testing.T) {
	src := initTestGitRepo(t, t.TempDir(), "app")
	wsDir := filepath.Join(t.TempDir(), "workspace")
	blockedPath := filepath.Join(wsDir, "app")
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatalf("mkdir blocked checkout path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockedPath, "not-a-checkout.txt"), []byte("blocked\n"), 0o644); err != nil {
		t.Fatalf("write blocked checkout marker: %v", err)
	}

	ctx := workspacecoord.WithCreateWarnings(context.Background())
	created, repos, err := addWorktrees(ctx, []resolvedRepo{{path: src, name: "app"}}, wsDir, "local-coder")
	if err != nil {
		t.Fatalf("addWorktrees returned fatal error: %v", err)
	}
	if len(created) != 0 {
		t.Fatalf("created = %v, want no created worktrees", created)
	}
	if len(repos) != 0 {
		t.Fatalf("repos = %#v, want skipped checkout omitted from runnable state", repos)
	}
	warnings := workspacecoord.GetCreateWarnings(ctx)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Skipped checkout") {
		t.Fatalf("warnings = %v, want skipped checkout warning", warnings)
	}
}

func TestStoreBackedAddReposAttachesLocalRepoToEmptyWorkspace(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")

	if _, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	src := initTestGitRepo(t, t.TempDir(), "api")
	addFn := BuildStoreBackedAddRepos(st)
	result, err := addFn(context.Background(), workspacecoord.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		Repos:       []string{src},
		Branch:      "feature-work",
	})
	if err != nil {
		t.Fatalf("add repo: %v", err)
	}
	if result.WorkspaceID != "MY-WS" || result.WorkspacePath != wsPath {
		t.Fatalf("result = %#v, want MY-WS at %s", result, wsPath)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "api", ".git")); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}

	repos, err := st.Repos().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "api" || repos[0].DefaultBranch != "feature-work" {
		t.Fatalf("repos = %#v, want api on feature-work", repos)
	}

	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	local := sc.Workspaces["MY-WS"]
	if local.Path != wsPath {
		t.Fatalf("local path = %q, want %q", local.Path, wsPath)
	}
	if local.Repos["api"] != filepath.Join(wsPath, "api") {
		t.Fatalf("local repo path = %q", local.Repos["api"])
	}
}

func TestStoreBackedAddReposAutoDetectsLocalRepoDefaultBranch(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	src := initTestGitRepo(t, t.TempDir(), "api")
	runGit(t, src, "branch", "-M", "main")
	origin := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, "", "init", "--bare", origin)
	// Match local-mode fixtures whose bare origin HEAD is stale while the
	// attached source checkout and origin/main correctly identify the base.
	runGit(t, origin, "symbolic-ref", "HEAD", "refs/heads/master")
	runGit(t, src, "remote", "add", "origin", origin)
	runGit(t, src, "push", "--set-upstream", "origin", "main")
	runGit(t, src, "checkout", "-b", "feature/current-work")
	addFn := BuildStoreBackedAddRepos(st)
	if _, err := addFn(context.Background(), workspacecoord.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		Repos:       []string{src},
	}); err != nil {
		t.Fatalf("add local repo without branch override: %v", err)
	}

	repos, err := st.Repos().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].DefaultBranch != "main" || repos[0].RemoteURL != origin {
		t.Fatalf("repos = %#v, want detected source default branch main", repos)
	}
	if got := strings.TrimSpace(gitOutput(t, src, "branch", "--show-current")); got != "feature/current-work" {
		t.Fatalf("source checkout branch = %q, want feature/current-work preserved", got)
	}
	checkout := filepath.Join(wsPath, "api")
	if got := strings.TrimSpace(gitOutput(t, checkout, "branch", "--show-current")); got != "my-ws" {
		t.Fatalf("workspace checkout branch = %q, want isolation branch my-ws", got)
	}
}

func TestDetectRepoDefaultBranchFailsClosedForUnadvertisedNoncanonicalRemote(t *testing.T) {
	src := initTestGitRepo(t, t.TempDir(), "api")
	runGit(t, src, "branch", "-M", "develop")
	origin := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, "", "init", "--bare", origin)
	runGit(t, origin, "symbolic-ref", "HEAD", "refs/heads/missing")
	runGit(t, src, "remote", "add", "origin", origin)
	runGit(t, src, "push", "--set-upstream", "origin", "develop")

	branch, err := detectRepoDefaultBranch(src)
	if err == nil || branch != "" {
		t.Fatalf("detect default branch = %q, err=%v; want fail-closed explicit-override error", branch, err)
	}
	if !strings.Contains(err.Error(), "specify one explicitly") {
		t.Fatalf("error = %q, want explicit default-branch guidance", err)
	}
}

func TestDetectRepoDefaultBranchRejectsTagThatLooksLikeRemoteMain(t *testing.T) {
	src := initTestGitRepo(t, t.TempDir(), "api")
	runGit(t, src, "branch", "-M", "develop")
	origin := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, "", "init", "--bare", origin)
	runGit(t, origin, "symbolic-ref", "HEAD", "refs/heads/missing")
	runGit(t, src, "remote", "add", "origin", origin)
	runGit(t, src, "push", "--set-upstream", "origin", "develop")
	runGit(t, src, "tag", "origin/main")
	runGit(t, src, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/tags/origin/main")

	branch, err := detectRepoDefaultBranch(src)
	if err == nil || branch != "" {
		t.Fatalf("detect default branch = %q, err=%v; want tag-shaped remote ref rejected", branch, err)
	}
	if !strings.Contains(err.Error(), "specify one explicitly") {
		t.Fatalf("error = %q, want explicit default-branch guidance", err)
	}
}

func TestDetectRepoDefaultBranchRejectsSymbolicRemoteMainToTag(t *testing.T) {
	src := initTestGitRepo(t, t.TempDir(), "api")
	runGit(t, src, "branch", "-M", "develop")
	origin := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, "", "init", "--bare", origin)
	runGit(t, origin, "symbolic-ref", "HEAD", "refs/heads/missing")
	runGit(t, src, "remote", "add", "origin", origin)
	runGit(t, src, "push", "--set-upstream", "origin", "develop")
	runGit(t, src, "tag", "evil")
	runGit(t, src, "symbolic-ref", "refs/remotes/origin/main", "refs/tags/evil")

	branch, err := detectRepoDefaultBranch(src)
	if err == nil || branch != "" {
		t.Fatalf("detect default branch = %q, err=%v; want symbolic remote main-to-tag rejected", branch, err)
	}
	if !strings.Contains(err.Error(), "specify one explicitly") {
		t.Fatalf("error = %q, want explicit default-branch guidance", err)
	}
}

func TestDetectRepoDefaultBranchRejectsNoOriginHeadThroughBranchToTag(t *testing.T) {
	src := initTestGitRepo(t, t.TempDir(), "api")
	runGit(t, src, "branch", "-M", "main")
	runGit(t, src, "tag", "evil")
	runGit(t, src, "symbolic-ref", "refs/heads/main", "refs/tags/evil")

	branch, err := detectRepoDefaultBranch(src)
	if err == nil || branch != "" {
		t.Fatalf("detect default branch = %q, err=%v; want no-origin HEAD-to-tag rejected", branch, err)
	}
}

func TestStoreBackedAddReposAttachesCheckedOutExistingDefaultBranch(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	src := initTestGitRepo(t, t.TempDir(), "api")
	branch := strings.TrimSpace(gitOutput(t, src, "branch", "--show-current"))
	sourceSHA := strings.TrimSpace(gitOutput(t, src, "rev-parse", "HEAD"))
	addFn := BuildStoreBackedAddRepos(st)
	if _, err := addFn(context.Background(), workspacecoord.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		Repos:       []string{src},
		Branch:      branch,
	}); err != nil {
		t.Fatalf("add checked-out default branch: %v", err)
	}

	checkout := filepath.Join(wsPath, "api")
	if got := strings.TrimSpace(gitOutput(t, checkout, "rev-parse", "HEAD")); got != sourceSHA {
		t.Fatalf("workspace checkout HEAD = %s, want source branch tip %s", got, sourceSHA)
	}
	cmd := exec.Command("git", "-C", checkout, "symbolic-ref", "-q", "HEAD") //nolint:norawexec // Real Git fixture verifies detached-HEAD semantics.
	if err := cmd.Run(); err == nil {
		t.Fatal("workspace checkout unexpectedly shares the source branch; want detached HEAD")
	}
	if got := strings.TrimSpace(gitOutput(t, src, "branch", "--show-current")); got != branch {
		t.Fatalf("source checkout branch = %q, want unchanged %q", got, branch)
	}

	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if got := sc.Workspaces["MY-WS"].Repos["api"]; got != checkout {
		t.Fatalf("local repo path = %q, want %q", got, checkout)
	}
}

func TestStoreBackedAddReposDoesNotPersistSkippedCheckout(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	src := initTestGitRepo(t, t.TempDir(), "api")
	blockedPath := filepath.Join(wsPath, "api")
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatalf("mkdir blocked checkout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockedPath, "keep.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatalf("write blocked checkout marker: %v", err)
	}

	addFn := BuildStoreBackedAddRepos(st)
	if _, err := addFn(context.Background(), workspacecoord.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		Repos:       []string{src},
		Branch:      "feature-work",
	}); err == nil {
		t.Fatal("add repo succeeded despite skipped checkout")
	}
	repos, err := st.Repos().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("persisted repos = %#v, want none", repos)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if _, ok := sc.Workspaces["MY-WS"].Repos["api"]; ok {
		t.Fatal("state cache persisted the skipped checkout")
	}
	if got, err := os.ReadFile(filepath.Join(blockedPath, "keep.txt")); err != nil || string(got) != "keep\n" {
		t.Fatalf("failed attach modified the pre-existing checkout path: contents=%q err=%v", got, err)
	}
	if out := gitOutput(t, src, "branch", "--list", "feature-work"); strings.TrimSpace(out) != "" {
		t.Fatalf("failed attach left its newly-created branch behind: %q", out)
	}
}

func TestStoreBackedAddReposRollsBackPartialLocalAttach(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	firstSrc := initTestGitRepo(t, t.TempDir(), "first")
	secondSrc := initTestGitRepo(t, t.TempDir(), "second")
	const branch = "feature-work"
	runGit(t, secondSrc, "branch", branch)
	secondBranchSHA := strings.TrimSpace(gitOutput(t, secondSrc, "rev-parse", branch))

	blockedPath := filepath.Join(wsPath, "second")
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatalf("mkdir blocked checkout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockedPath, "keep.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatalf("write blocked checkout marker: %v", err)
	}

	addFn := BuildStoreBackedAddRepos(st)
	if _, err := addFn(context.Background(), workspacecoord.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		Repos:       []string{firstSrc, secondSrc},
		Branch:      branch,
	}); err == nil {
		t.Fatal("add repos succeeded despite second checkout being blocked")
	}

	if _, err := os.Stat(filepath.Join(wsPath, "first")); !os.IsNotExist(err) {
		t.Fatalf("first checkout was not rolled back, stat err=%v", err)
	}
	if got, err := os.ReadFile(filepath.Join(blockedPath, "keep.txt")); err != nil || string(got) != "keep\n" {
		t.Fatalf("failed attach modified the blocked second path: contents=%q err=%v", got, err)
	}

	repos, err := st.Repos().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("persisted repos = %#v, want none", repos)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if _, ok := sc.Workspaces["MY-WS"].Repos["first"]; ok {
		t.Fatal("state cache persisted the rolled-back first checkout")
	}
	if _, ok := sc.Workspaces["MY-WS"].Repos["second"]; ok {
		t.Fatal("state cache persisted the skipped second checkout")
	}

	if out := strings.TrimSpace(gitOutput(t, firstSrc, "branch", "--list", branch)); out != "" {
		t.Fatalf("rollback left its operation-created branch behind in first repo: %q", out)
	}
	if got := strings.TrimSpace(gitOutput(t, secondSrc, "rev-parse", branch)); got != secondBranchSHA {
		t.Fatalf("pre-existing second repo branch changed or was removed: got %s, want %s", got, secondBranchSHA)
	}
}

func TestStoreBackedAddReposClassifiesLocalRepoNameCollisionAndRollsBack(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	base := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(base)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	src := initTestGitRepo(t, t.TempDir(), "shared-repo")
	st := &repoFailStore{Store: base, err: domain.ErrAlreadyExists}
	addFn := BuildStoreBackedAddRepos(st)
	_, err := addFn(context.Background(), workspacecoord.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		Repos:       []string{src},
		Branch:      "proof-work",
	})
	var createErr *workspaceerrors.CreateError
	if !errors.As(err, &createErr) || createErr.Code != workspaceerrors.AlreadyExists {
		t.Fatalf("error = %v, want AlreadyExists workspace error", err)
	}
	if !strings.Contains(createErr.Message, "already registered in this workspace") {
		t.Fatalf("error message = %q, want same-workspace collision guidance", createErr.Message)
	}

	if _, statErr := os.Stat(filepath.Join(wsPath, "shared-repo")); !os.IsNotExist(statErr) {
		t.Fatalf("failed attach left workspace checkout behind: %v", statErr)
	}
	if out := strings.TrimSpace(gitOutput(t, src, "branch", "--list", "proof-work")); out != "" {
		t.Fatalf("failed attach left operation-created branch behind: %q", out)
	}
	assertWorkspaceHasNoRepo(t, base, "MY-WS", "shared-repo")
}

func TestStoreBackedAddReposClassifiesCloneRepoNameCollisionAndRetainsCheckoutForReview(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	base := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(base)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	remote := initTestGitRepo(t, t.TempDir(), "shared-clone")
	st := &repoFailStore{Store: base, err: domain.ErrAlreadyExists}
	addFn := buildTestAddReposWithAdmission(t, st, sourceControlFor(st))
	_, err := addFn(context.Background(), workspacecoord.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		CloneURLs:   []string{remote},
	})
	var createErr *workspaceerrors.CreateError
	if !errors.As(err, &createErr) || createErr.Code != workspaceerrors.AlreadyExists {
		t.Fatalf("error = %v, want AlreadyExists workspace error", err)
	}
	if !strings.Contains(createErr.Message, "already registered in this workspace") {
		t.Fatalf("error message = %q, want same-workspace collision guidance", createErr.Message)
	}

	if _, statErr := os.Stat(filepath.Join(wsPath, "shared-clone", ".git")); statErr != nil {
		t.Fatalf("verified checkout was not retained for admission review: %v", statErr)
	}
	repos, listErr := base.Repos().List(context.Background(), "MY-WS")
	if listErr != nil || len(repos) != 0 {
		t.Fatalf("uncommitted FleetDB repos = %#v, err=%v", repos, listErr)
	}
	state, stateErr := bootstrap.LoadStateCache()
	if stateErr != nil ||
		state.Workspaces["MY-WS"].Repos["shared-clone"] != filepath.Join(wsPath, "shared-clone") {
		t.Fatalf("durable local retry projection = %#v, err=%v", state, stateErr)
	}
}

func assertWorkspaceHasNoRepo(t *testing.T, st store.Store, workspace, repo string) {
	t.Helper()

	repos, err := st.Repos().List(context.Background(), workspace)
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("repos after rejected attach = %#v, want none", repos)
	}

	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if _, ok := sc.Workspaces[workspace].Repos[repo]; ok {
		t.Fatalf("state cache persisted rejected repo %q", repo)
	}
}

func TestStoreBackedAddReposClonesRemoteRepoToEmptyWorkspace(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")

	if _, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	src := initTestGitRepo(t, t.TempDir(), "Hello-World")
	sourceBranch := strings.TrimSpace(gitOutput(t, src, "branch", "--show-current"))
	materializer := sourceControlFor(st)
	addFn := buildTestAddReposWithAdmission(t, st, materializer)
	result, err := addFn(context.Background(), workspacecoord.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		CloneURLs:   []string{src},
	})
	if err != nil {
		t.Fatalf("add clone repo: %v", err)
	}
	if len(materializer.commands) != 1 ||
		materializer.commands[0].RepositoryRef != "hello-world" {
		t.Fatalf("Source Control commands = %#v, want hello-world admission", materializer.commands)
	}
	if result.WorkspaceID != "MY-WS" || result.WorkspacePath != wsPath {
		t.Fatalf("result = %#v, want MY-WS at %s", result, wsPath)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "hello-world", ".git")); err != nil {
		t.Fatalf("clone checkout not created: %v", err)
	}

	repos, err := st.Repos().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "hello-world" || repos[0].RemoteURL != src ||
		repos[0].SourceRepoID != "hello-world" || repos[0].DefaultBranch != sourceBranch {
		t.Fatalf("repos = %#v, want cloned hello-world repo", repos)
	}

	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	local := sc.Workspaces["MY-WS"]
	if local.Repos["hello-world"] != filepath.Join(wsPath, "hello-world") {
		t.Fatalf("local repo path = %q", local.Repos["hello-world"])
	}

	// A failed clone still crosses only the opaque repository-reference port;
	// the remote remains behind the test resolver.
	missingRemote := filepath.Join(t.TempDir(), "missing-private.git")
	if _, err := addFn(context.Background(), workspacecoord.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		CloneURLs:   []string{missingRemote},
	}); err == nil {
		t.Fatal("add missing clone repo succeeded")
	}
	if len(materializer.commands) != 2 ||
		materializer.commands[1].RepositoryRef != "missing-private" {
		t.Fatalf("Source Control commands = %#v, want opaque missing-private admission", materializer.commands)
	}
}

func TestStoreBackedRemoteCloneFailsClosedWithoutSourceControl(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(t.Context(), workspacecoord.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}
	remote := initTestGitRepo(t, t.TempDir(), "private-repo")
	_, err := BuildStoreBackedAddRepos(st)(t.Context(), workspacecoord.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		CloneURLs:   []string{remote},
	})
	var createErr *workspaceerrors.CreateError
	if !errors.As(err, &createErr) ||
		createErr.Code != workspaceerrors.SecurityViolation ||
		!errors.Is(err, sourcecontrol.ErrUnavailable) {
		t.Fatalf("add clone error = %v, want fail-closed Source Control error", err)
	}
	assertWorkspaceHasNoRepo(t, st, "MY-WS", "private-repo")
	if _, statErr := os.Stat(filepath.Join(wsPath, "private-repo")); !os.IsNotExist(statErr) {
		t.Fatalf("fail-closed clone created a checkout: %v", statErr)
	}

	_, err = BuildStoreBackedCreateWorkspace(st)(t.Context(), workspacecoord.WorkspaceCreateRequest{
		Name:      "clone-without-owner",
		Type:      "clone",
		CloneURLs: []string{remote},
		Path:      filepath.Join(loomDir, "workspaces", "clone-without-owner"),
	})
	if !errors.As(err, &createErr) ||
		createErr.Code != workspaceerrors.SecurityViolation ||
		!errors.Is(err, sourcecontrol.ErrUnavailable) {
		t.Fatalf("create clone error = %v, want fail-closed Source Control error", err)
	}
	if _, getErr := st.Workspaces().Get(t.Context(), "CLONE-WITHOUT-OWNER"); !errors.Is(getErr, domain.ErrNotFound) {
		t.Fatalf("fail-closed clone persisted workspace: %v", getErr)
	}
}

func TestStoreBackedRemoteCloneRejectsCredentialBearingRemoteBeforePersistence(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(t.Context(), workspacecoord.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}
	materializer := sourceControlFor(st)
	_, err := buildTestAddReposWithAdmission(t, st, materializer)(
		t.Context(),
		workspacecoord.WorkspaceAddReposRequest{
			WorkspaceID: "MY-WS",
			CloneURLs: []string{
				"https://operator:plaintext-secret@example.test/private.git",
			},
		},
	)
	var createErr *workspaceerrors.CreateError
	if !errors.As(err, &createErr) ||
		createErr.Code != workspaceerrors.SecurityViolation ||
		!errors.Is(err, sourcecontrol.ErrInvalid) {
		t.Fatalf("credential-bearing remote error = %v, want security violation", err)
	}
	if len(materializer.commands) != 0 {
		t.Fatalf("Source Control commands = %#v, want none for invalid remote", materializer.commands)
	}
	repositories, listErr := st.Repos().List(t.Context(), "MY-WS")
	if listErr != nil || len(repositories) != 0 {
		t.Fatalf("repositories after invalid remote = %#v, err=%v", repositories, listErr)
	}
}

func TestStoreBackedAddReposRejectsCredentialBearingLocalRemote(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := BuildStoreBackedCreateWorkspace(st)(
		t.Context(),
		workspacecoord.WorkspaceCreateRequest{Name: "my-ws", Type: "empty", Path: wsPath},
	); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}
	src := initTestGitRepo(t, t.TempDir(), "private-local")
	runGit(
		t,
		src,
		"remote",
		"add",
		"origin",
		"https://operator:plaintext-secret@example.test/private.git",
	)
	_, err := BuildStoreBackedAddRepos(st)(
		t.Context(),
		workspacecoord.WorkspaceAddReposRequest{
			WorkspaceID: "MY-WS",
			Repos:       []string{src},
			Branch:      strings.TrimSpace(gitOutput(t, src, "branch", "--show-current")),
		},
	)
	var createErr *workspaceerrors.CreateError
	if !errors.As(err, &createErr) ||
		createErr.Code != workspaceerrors.SecurityViolation ||
		!errors.Is(err, sourcecontrol.ErrInvalid) {
		t.Fatalf("credential-bearing local attach error = %v, want security violation", err)
	}
	if strings.Contains(err.Error(), "plaintext-secret") {
		t.Fatalf("credential-bearing local attach leaked secret in error: %v", err)
	}
	assertWorkspaceHasNoRepo(t, st, "MY-WS", "private-local")
	if _, statErr := os.Stat(filepath.Join(wsPath, "private-local")); !os.IsNotExist(statErr) {
		t.Fatalf("credential-bearing local attach left worktree: %v", statErr)
	}
}

func TestWorkspaceRepositoryOperationIDIsStableAndRemoteBound(t *testing.T) {
	first := workspaceRepositoryOperationID("WS-1", "repo", "https://example.test/repo.git")
	retry := workspaceRepositoryOperationID("WS-1", "repo", "https://example.test/repo.git")
	otherRemote := workspaceRepositoryOperationID("WS-1", "repo", "https://example.test/other.git")
	if first != retry {
		t.Fatalf("retry operation ID changed: %q != %q", first, retry)
	}
	if first == otherRemote {
		t.Fatalf("different remote reused operation ID %q", first)
	}
	if strings.Contains(first, "example.test") {
		t.Fatalf("operation ID exposes remote: %q", first)
	}
}

func TestStoreBackedAddReposPersistsEachClonesDetectedDefaultBranch(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	alpha := initTestGitRepo(t, t.TempDir(), "alpha")
	beta := initTestGitRepo(t, t.TempDir(), "beta")
	runGit(t, alpha, "branch", "-m", "main")
	runGit(t, beta, "branch", "-m", "master")

	addFn := buildTestAddReposWithAdmission(t, st, sourceControlFor(st))
	if _, err := addFn(context.Background(), workspacecoord.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		CloneURLs:   []string{alpha, beta},
	}); err != nil {
		t.Fatalf("add mixed-default clones: %v", err)
	}

	repos, err := st.Repos().List(context.Background(), "MY-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	branches := make(map[string]string, len(repos))
	for _, repo := range repos {
		branches[repo.Name] = repo.DefaultBranch
	}
	if branches["alpha"] != "main" || branches["beta"] != "master" {
		t.Fatalf("detected branches = %#v, want alpha=main beta=master", branches)
	}
}

func TestStoreBackedAddReposRejectsExplicitMissingCloneBranch(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	src := initTestGitRepo(t, t.TempDir(), "hello-world")
	runGit(t, src, "branch", "-m", "master")
	addFn := buildTestAddReposWithAdmission(t, st, sourceControlFor(st))
	_, err := addFn(context.Background(), workspacecoord.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		CloneURLs:   []string{src},
		Branch:      "main",
	})
	if err == nil || !strings.Contains(err.Error(), `default branch "main" does not exist`) {
		t.Fatalf("add clone error = %v, want missing explicit default branch", err)
	}
	if _, statErr := os.Stat(filepath.Join(wsPath, "hello-world", ".git")); statErr != nil {
		t.Fatalf("verified clone checkout was not retained for retry: %v", statErr)
	}
	repos, listErr := st.Repos().List(context.Background(), "MY-WS")
	if listErr != nil || len(repos) != 0 {
		t.Fatalf("repos after rejected clone = %#v, err=%v", repos, listErr)
	}
}

func TestStoreBackedAddReposRejectsEmptyRemoteWithoutCommittedDefaultBranch(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")
	if _, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: wsPath,
	}); err != nil {
		t.Fatalf("create empty workspace: %v", err)
	}

	emptyRemote := filepath.Join(t.TempDir(), "empty-remote")
	if err := os.MkdirAll(emptyRemote, 0o755); err != nil {
		t.Fatalf("create empty remote: %v", err)
	}
	runGit(t, emptyRemote, "init")

	addFn := buildTestAddReposWithAdmission(t, st, sourceControlFor(st))
	_, err := addFn(context.Background(), workspacecoord.WorkspaceAddReposRequest{
		WorkspaceID: "MY-WS",
		CloneURLs:   []string{emptyRemote},
	})
	if err == nil || !strings.Contains(err.Error(), "resolvable committed default branch") ||
		!strings.Contains(err.Error(), "specify one explicitly") {
		t.Fatalf("add empty remote error = %v, want committed-branch validation", err)
	}
	if _, statErr := os.Stat(filepath.Join(wsPath, "empty-remote", ".git")); statErr != nil {
		t.Fatalf("empty remote checkout was not retained for retry: %v", statErr)
	}
	repos, listErr := st.Repos().List(context.Background(), "MY-WS")
	if listErr != nil || len(repos) != 0 {
		t.Fatalf("repos after rejected empty remote = %#v, err=%v", repos, listErr)
	}
}

func TestStoreBackedCreateEmptyWorkspaceRollsBackOnRepoStoreError(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "app")
	base := memstore.New()
	st := &repoFailStore{Store: base, err: errors.New("repo create failed")}
	createFn := BuildStoreBackedCreateWorkspace(st)
	wsPath := filepath.Join(loomDir, "workspaces", "my-ws")

	if _, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name:   "my-ws",
		Type:   "empty",
		Repos:  []string{src},
		Branch: "feature-work",
		Path:   wsPath,
	}); err == nil {
		t.Fatal("create workspace succeeded, want repo store error")
	}

	if _, err := base.Workspaces().Get(context.Background(), "MY-WS"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("workspace was not rolled back, err=%v", err)
	}
	if _, err := os.Stat(wsPath); !os.IsNotExist(err) {
		t.Fatalf("workspace path still exists after rollback, stat err=%v", err)
	}
	if out := gitOutput(t, src, "branch", "--list", "feature-work"); out != "" {
		t.Fatalf("rollback left branch feature-work behind: %q", out)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if sc.LastWorkspace != "" || len(sc.Workspaces) != 0 {
		t.Fatalf("state cache was written on rollback: %#v", sc)
	}
}

func TestStoreBackedCreateEmptyWorkspaceClassifiesCreateRace(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := &workspaceCreateRaceStore{Store: memstore.New()}
	createFn := BuildStoreBackedCreateWorkspace(st)

	_, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name: "my-ws",
		Type: "empty",
		Path: filepath.Join(loomDir, "workspaces", "my-ws"),
	})
	var createErr *workspaceerrors.CreateError
	if !errors.As(err, &createErr) {
		t.Fatalf("error = %v, want workspace create error", err)
	}
	if createErr.Code != workspaceerrors.AlreadyExists {
		t.Fatalf("error code = %s, want AlreadyExists", createErr.Code)
	}
}

func TestStoreBackedCreateEmptyWorkspaceRollsBackLocalStateOnReadyUpdateError(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := &workspaceReadyUpdateFailStore{Store: memstore.New()}
	createFn := BuildStoreBackedCreateWorkspace(st)

	_, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name: "rollback-ws",
		Type: "empty",
		Path: filepath.Join(loomDir, "workspaces", "rollback-ws"),
	})
	if err == nil {
		t.Fatal("create workspace succeeded, want ready update error")
	}
	if _, getErr := st.Store.Workspaces().Get(context.Background(), "ROLLBACK-WS"); !errors.Is(getErr, domain.ErrNotFound) {
		t.Fatalf("store workspace get err = %v, want ErrNotFound", getErr)
	}
	sc, loadErr := bootstrap.LoadStateCache()
	if loadErr != nil {
		t.Fatalf("load state cache: %v", loadErr)
	}
	if _, ok := sc.Workspaces["ROLLBACK-WS"]; ok {
		t.Fatalf("local state still contains ROLLBACK-WS: %#v", sc.Workspaces["ROLLBACK-WS"])
	}
	if sc.LastWorkspace == "ROLLBACK-WS" {
		t.Fatalf("LastWorkspace = %q, want rollback to clear active workspace", sc.LastWorkspace)
	}
}

func TestStoreBackedCreateCloneWorkspacePersistsLifecycleAndRepos(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "app")
	sourceBranch := strings.TrimSpace(gitOutput(t, src, "branch", "--show-current"))
	st := memstore.New()
	materializer := sourceControlFor(st)
	createFn := buildTestCreateWithAdmission(t, st, materializer)
	wsPath := filepath.Join(loomDir, "workspaces", "clone-ws")

	result, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name:      "clone-ws",
		Type:      "clone",
		CloneURLs: []string{src},
		Path:      wsPath,
	})
	if err != nil {
		t.Fatalf("clone workspace: %v", err)
	}
	if len(materializer.commands) != 1 ||
		materializer.commands[0].RepositoryRef != "app" {
		t.Fatalf("Source Control commands = %#v, want app admission", materializer.commands)
	}
	if result.WorkspaceID != "CLONE-WS" {
		t.Fatalf("WorkspaceID = %q, want CLONE-WS", result.WorkspaceID)
	}
	ws, err := st.Workspaces().Get(context.Background(), "CLONE-WS")
	if err != nil {
		t.Fatalf("workspace not stored: %v", err)
	}
	if ws.State != workspacemodule.StateReady {
		t.Fatalf("workspace state = %q, want ready", ws.State)
	}
	repos, err := st.Repos().List(context.Background(), "CLONE-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "app" || repos[0].RemoteURL != src ||
		repos[0].SourceRepoID != "app" || repos[0].DefaultBranch != sourceBranch {
		t.Fatalf("repos = %#v, want cloned app repo with remote URL", repos)
	}
	if ws.DefaultBranch != sourceBranch {
		t.Fatalf("workspace default branch = %q, want detected %q", ws.DefaultBranch, sourceBranch)
	}
	roles, err := st.Roles().List(context.Background(), "CLONE-WS")
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	if len(roles) != 3 || !hasRole(roles, "plan") || !hasRole(roles, "task") || !hasRole(roles, "lead") {
		t.Fatalf("roles = %#v, want plan, task, and lead", roles)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "app", ".git")); err != nil {
		t.Fatalf("clone checkout not created: %v", err)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if sc.LastWorkspace != "CLONE-WS" {
		t.Fatalf("LastWorkspace = %q, want CLONE-WS", sc.LastWorkspace)
	}
	if sc.Workspaces["CLONE-WS"].Repos["app"] != filepath.Join(wsPath, "app") {
		t.Fatalf("state repo path = %q", sc.Workspaces["CLONE-WS"].Repos["app"])
	}

	// A failed clone still carries only the opaque repository reference over
	// the Workspace-to-Source-Control port.
	missingRemote := filepath.Join(t.TempDir(), "missing-private.git")
	if _, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name:      "clone-ws-auth-fallback",
		Type:      "clone",
		CloneURLs: []string{missingRemote},
		Path:      filepath.Join(loomDir, "workspaces", "clone-ws-auth-fallback"),
	}); err == nil {
		t.Fatal("create workspace from missing clone repo succeeded")
	}
	if len(materializer.commands) != 2 ||
		materializer.commands[1].RepositoryRef != "missing-private" {
		t.Fatalf("Source Control commands = %#v, want opaque missing-private admission", materializer.commands)
	}
}

func TestStoreBackedCreateCloneWorkspaceRecoversBeginLostBeforeLocalBind(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "app")
	st := memstore.New()
	materializer := sourceControlFor(st)
	journal, err := newLocalRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "admissions"),
		time.Now,
	)
	if err != nil {
		t.Fatalf("new admission journal: %v", err)
	}
	wsPath := filepath.Join(loomDir, "workspaces", "crash-recovery")
	planned, err := planCloneRepos([]string{src}, make(map[string]bool))
	if err != nil {
		t.Fatalf("plan clone: %v", err)
	}
	specs := cloneAdmissionSpecs(planned, "")
	operationID, err := repositoryAdmissionOperationID(
		"create_workspace",
		"CRASH-RECOVERY",
		wsPath,
		specs,
	)
	if err != nil {
		t.Fatalf("operation ID: %v", err)
	}
	if _, err := journal.Prepare(t.Context(), localRepositoryAdmissionIntent{
		OperationID: operationID, WorkspaceKey: "CRASH-RECOVERY",
		WorkspaceName: "crash-recovery", WorkspacePath: wsPath,
		Kind:      localRepositoryAdmissionCreateWorkspace,
		CloneURLs: []string{src},
	}); err != nil {
		t.Fatalf("prepare local intent: %v", err)
	}
	begin, err := materializer.CreateWorkspaceWithRepositoryAdmission(
		t.Context(),
		infrafleetdb.WorkspaceRepositoryAdmissionBeginInput{
			Workspace: infrafleetdb.RepositoryAdmissionWorkspaceInput{
				Key: "CRASH-RECOVERY", Name: "crash-recovery",
				State: "creating", DefaultBranch: "main",
			},
			OperationID: operationID, OwnerID: "dead-loom-process",
			OwnerLease: repositoryAdmissionLease, Repositories: specs,
		},
	)
	if err != nil {
		t.Fatalf("seed FleetDB begin: %v", err)
	}
	materializer.mu.Lock()
	materializer.records[begin.Admission.AdmissionID].OwnerLeaseExpiresAt =
		time.Now().Add(-time.Second)
	materializer.mu.Unlock()

	createFn := BuildStoreBackedCreateWorkspaceWithAdmission(
		st,
		materializer,
		journal,
		materializer,
	)
	result, err := createFn(t.Context(), workspacecoord.WorkspaceCreateRequest{
		Name: "crash-recovery", Type: "clone",
		CloneURLs: []string{src}, Path: wsPath,
	})
	if err != nil {
		t.Fatalf("recover lost begin response: %v", err)
	}
	if result.WorkspaceID != "CRASH-RECOVERY" {
		t.Fatalf("workspace ID = %q, want CRASH-RECOVERY", result.WorkspaceID)
	}
	bound, err := journal.GetByOperation(t.Context(), operationID)
	if err != nil {
		t.Fatalf("load rebound intent: %v", err)
	}
	if bound.AdmissionID != begin.Admission.AdmissionID ||
		bound.SpecFingerprint != begin.Admission.SpecFingerprint {
		t.Fatalf("bound coordinates = %#v, want seeded admission", bound)
	}
	record, err := materializer.GetRepositoryAdmission(
		t.Context(),
		"CRASH-RECOVERY",
		begin.Admission.AdmissionID,
	)
	if err != nil || record.State != "committed" {
		t.Fatalf("recovered admission = %#v, err=%v", record, err)
	}
}

func TestStoreBackedCreateCloneWorkspaceRecoversPartialCheckoutAndReplaysCommit(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	alpha := initTestGitRepo(t, t.TempDir(), "alpha")
	beta := initTestGitRepo(t, t.TempDir(), "beta")
	st := memstore.New()
	materializer := sourceControlFor(st)
	journal, err := newLocalRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "admissions"),
		time.Now,
	)
	if err != nil {
		t.Fatalf("new admission journal: %v", err)
	}
	wsPath := filepath.Join(loomDir, "workspaces", "partial-recovery")
	request := workspacecoord.WorkspaceCreateRequest{
		Name: "partial-recovery", Type: "clone",
		CloneURLs: []string{alpha, beta}, Path: wsPath,
	}
	materializer.failNextRepositoryRef = "beta"
	materializer.failNextMaterializeError = sourcecontrol.ErrUnavailable

	firstCreate := BuildStoreBackedCreateWorkspaceWithAdmission(
		st,
		materializer,
		journal,
		materializer,
	)
	if _, err := firstCreate(t.Context(), request); !errors.Is(err, sourcecontrol.ErrUnavailable) {
		t.Fatalf("first create error = %v, want retryable Source Control outage", err)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "alpha", ".git")); err != nil {
		t.Fatalf("first verified checkout was not retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "beta", ".git")); !os.IsNotExist(err) {
		t.Fatalf("failed checkout unexpectedly exists: %v", err)
	}
	materializer.mu.Lock()
	var admissionID string
	for id, record := range materializer.records {
		admissionID = id
		if record.State != "retryable_failed" {
			t.Fatalf("first admission state = %q, want retryable_failed", record.State)
		}
	}
	materializer.mu.Unlock()
	if admissionID == "" {
		t.Fatal("first attempt did not persist an admission")
	}

	restartedCreate := BuildStoreBackedCreateWorkspaceWithAdmission(
		st,
		materializer,
		journal,
		materializer,
	)
	result, err := restartedCreate(t.Context(), request)
	if err != nil {
		t.Fatalf("restart recovery: %v", err)
	}
	if result.WorkspaceID != "PARTIAL-RECOVERY" {
		t.Fatalf("workspace ID = %q, want PARTIAL-RECOVERY", result.WorkspaceID)
	}
	for _, name := range []string{"alpha", "beta"} {
		if _, err := os.Stat(filepath.Join(wsPath, name, ".git")); err != nil {
			t.Fatalf("recovered checkout %s missing: %v", name, err)
		}
	}
	record, err := materializer.GetRepositoryAdmission(
		t.Context(),
		"PARTIAL-RECOVERY",
		admissionID,
	)
	if err != nil || record.State != "committed" {
		t.Fatalf("recovered admission = %#v, err=%v", record, err)
	}
	materializer.mu.Lock()
	commandCount := len(materializer.commands)
	materializer.mu.Unlock()

	replayCreate := BuildStoreBackedCreateWorkspaceWithAdmission(
		st,
		materializer,
		journal,
		materializer,
	)
	replayed, err := replayCreate(t.Context(), request)
	if err != nil {
		t.Fatalf("committed replay: %v", err)
	}
	if replayed != result {
		t.Fatalf("replayed result = %#v, want %#v", replayed, result)
	}
	materializer.mu.Lock()
	defer materializer.mu.Unlock()
	if len(materializer.commands) != commandCount {
		t.Fatalf(
			"committed replay issued %d new materialization commands",
			len(materializer.commands)-commandCount,
		)
	}
}

func TestStoreBackedCreateCloneWorkspaceNormalizesRepoNameForFleetStore(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	src := initTestGitRepo(t, t.TempDir(), "Hello-World")
	sourceBranch := strings.TrimSpace(gitOutput(t, src, "branch", "--show-current"))
	st := memstore.New()
	createFn := buildTestCreateWithAdmission(t, st, sourceControlFor(st))
	wsPath := filepath.Join(loomDir, "workspaces", "clone-ws")

	_, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name:      "clone-ws",
		Type:      "clone",
		CloneURLs: []string{src},
		Path:      wsPath,
	})
	if err != nil {
		t.Fatalf("clone workspace: %v", err)
	}

	repos, err := st.Repos().List(context.Background(), "CLONE-WS")
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "hello-world" || repos[0].SourceRepoID != "hello-world" ||
		repos[0].DefaultBranch != sourceBranch {
		t.Fatalf("repos = %#v, want normalized hello-world repo", repos)
	}
	if _, err := os.Stat(filepath.Join(wsPath, "hello-world", ".git")); err != nil {
		t.Fatalf("clone checkout not created at normalized path: %v", err)
	}
}

func TestStoreBackedCreateCloneWorkspaceClassifiesCreateRace(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := &workspaceCreateRaceStore{Store: memstore.New()}
	materializer := sourceControlFor(st)
	journal, journalErr := newLocalRepositoryAdmissionJournalAt(
		filepath.Join(t.TempDir(), "admissions"),
		time.Now,
	)
	if journalErr != nil {
		t.Fatal(journalErr)
	}
	createFn := BuildStoreBackedCreateWorkspaceWithAdmission(
		st,
		materializer,
		journal,
		materializer,
	)
	src := initTestGitRepo(t, t.TempDir(), "app")

	_, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name:      "clone-ws",
		Type:      "clone",
		CloneURLs: []string{src},
		Path:      filepath.Join(loomDir, "workspaces", "clone-ws"),
	})
	var createErr *workspaceerrors.CreateError
	if !errors.As(err, &createErr) {
		t.Fatalf("error = %v, want workspace create error", err)
	}
	if createErr.Code != workspaceerrors.AlreadyExists {
		t.Fatalf("error code = %s, want AlreadyExists", createErr.Code)
	}
	records, listErr := journal.List(context.Background())
	if listErr != nil {
		t.Fatalf("list admission journal: %v", listErr)
	}
	if len(records) != 0 {
		t.Fatalf("definitive conflict retained unbound admission journal records: %#v", records)
	}
}

func TestStoreBackedCreateCloneWorkspaceRetainsDurableCreatingStateOnRetryableFailure(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	st := memstore.New()
	createFn := buildTestCreateWithAdmission(t, st, sourceControlFor(st))
	wsPath := filepath.Join(loomDir, "workspaces", "clone-ws")

	_, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name:      "clone-ws",
		Type:      "clone",
		CloneURLs: []string{filepath.Join(t.TempDir(), "missing")},
		Path:      wsPath,
	})
	if err == nil {
		t.Fatal("clone workspace succeeded, want git clone error")
	}

	workspace, getErr := st.Workspaces().Get(context.Background(), "CLONE-WS")
	if getErr != nil {
		t.Fatalf("durable creating workspace was lost: %v", getErr)
	}
	if workspace.State != workspacemodule.StateCreating {
		t.Fatalf("workspace state = %q, want creating for recovery", workspace.State)
	}
	if _, statErr := os.Stat(wsPath); statErr != nil {
		t.Fatalf("workspace recovery root was lost: %v", statErr)
	}
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatalf("load state cache: %v", err)
	}
	if sc.LastWorkspace != "CLONE-WS" ||
		sc.Workspaces["CLONE-WS"].Path != wsPath {
		t.Fatalf("state cache does not retain recovery coordinates: %#v", sc)
	}
}

func TestStoreBackedCreateCloneWorkspaceKeepsPreexistingExternalRootOnFailure(t *testing.T) {
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	externalPath := filepath.Join(t.TempDir(), "picked-workspace")
	if err := os.MkdirAll(externalPath, 0755); err != nil {
		t.Fatalf("mkdir external path: %v", err)
	}

	st := memstore.New()
	createFn := buildTestCreateWithAdmission(t, st, sourceControlFor(st))

	_, err := createFn(context.Background(), workspacecoord.WorkspaceCreateRequest{
		Name:      "clone-ws",
		Type:      "clone",
		CloneURLs: []string{filepath.Join(t.TempDir(), "missing")},
		Path:      externalPath,
	})
	if err == nil {
		t.Fatal("clone workspace succeeded, want git clone error")
	}
	if info, statErr := os.Stat(externalPath); statErr != nil || !info.IsDir() {
		t.Fatalf("pre-existing external workspace root was removed, info=%v err=%v", info, statErr)
	}
}

type repoFailStore struct {
	*memstore.Store
	err error
}

func (s *repoFailStore) Repos() store.RepoStore {
	return repoFailer{err: s.err}
}

type workspaceCreateRaceStore struct {
	*memstore.Store
}

func (s *workspaceCreateRaceStore) Workspaces() store.WorkspaceStore {
	return workspaceCreateRaceWorkspaceStore{WorkspaceStore: s.Store.Workspaces()}
}

type workspaceCreateRaceWorkspaceStore struct {
	store.WorkspaceStore
}

func (s workspaceCreateRaceWorkspaceStore) Create(context.Context, store.WorkspaceCreate) (*workspacemodule.Workspace, error) {
	return nil, domain.ErrAlreadyExists
}

type workspaceReadyUpdateFailStore struct {
	*memstore.Store
}

func (s *workspaceReadyUpdateFailStore) Workspaces() store.WorkspaceStore {
	return workspaceReadyUpdateFailWorkspaceStore{WorkspaceStore: s.Store.Workspaces()}
}

type workspaceReadyUpdateFailWorkspaceStore struct {
	store.WorkspaceStore
}

func (s workspaceReadyUpdateFailWorkspaceStore) Update(ctx context.Context, key string, patch store.WorkspaceUpdate) (*workspacemodule.Workspace, error) {
	if patch.State != nil && *patch.State == workspacemodule.StateReady {
		return nil, errors.New("ready update failed")
	}
	return s.WorkspaceStore.Update(ctx, key, patch)
}

type repoFailer struct {
	err error
}

func hasRole(roles []*domain.Role, name string) bool {
	for _, role := range roles {
		if role.Name == name {
			return true
		}
	}
	return false
}

func rolesByName(roles []*domain.Role) map[string]*domain.Role {
	out := make(map[string]*domain.Role, len(roles))
	for _, role := range roles {
		out[role.Name] = role
	}
	return out
}

func (r repoFailer) Create(context.Context, store.RepoCreate) (*workspacemodule.Repository, error) {
	return nil, r.err
}

func (r repoFailer) Get(context.Context, string, string) (*workspacemodule.Repository, error) {
	return nil, domain.ErrNotFound
}

func (r repoFailer) List(context.Context, string) ([]*workspacemodule.Repository, error) {
	return nil, nil
}

func (r repoFailer) Update(context.Context, string, string, store.RepoUpdate) (*workspacemodule.Repository, error) {
	return nil, r.err
}

func (r repoFailer) Delete(context.Context, string, string) error {
	return nil
}

func initTestGitRepo(t *testing.T, parent, name string) string {
	t.Helper()
	path := filepath.Join(parent, name)
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGit(t, path, "init")
	runGit(t, path, "config", "user.email", "test@example.com")
	runGit(t, path, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("test\n"), 0644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGit(t, path, "add", "README.md")
	runGit(t, path, "commit", "-m", "init")
	return path
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Test helper creates real git repos for workspace lifecycle coverage.
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Test helper creates real git repos for workspace lifecycle coverage.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

func corruptWorkspaceBranchRef(t *testing.T, repoPath, branch string) {
	t.Helper()
	common, err := gitbranch.CommonDir(repoPath)
	if err != nil {
		t.Fatalf("git common dir: %v", err)
	}
	refPath := filepath.Join(common, "refs", "heads", filepath.FromSlash(branch))
	if err := os.MkdirAll(filepath.Dir(refPath), 0o755); err != nil {
		t.Fatalf("mkdir branch ref parent: %v", err)
	}
	if err := os.WriteFile(refPath, nil, 0o644); err != nil {
		t.Fatalf("corrupt branch ref: %v", err)
	}
}
