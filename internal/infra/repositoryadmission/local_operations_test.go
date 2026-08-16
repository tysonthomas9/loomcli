package repositoryadmissioninfra

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/repositoryadmission"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

type repositoryAdmissionCatalogFake struct {
	workspacemodule.API
	workspace    workspacemodule.Reference
	repos        []workspacemodule.Repository
	created      []string
	deleted      []string
	unregistered []string
}

func (fake *repositoryAdmissionCatalogFake) Create(
	_ context.Context,
	command workspacemodule.CreateCommand,
) (*workspacemodule.Reference, error) {
	fake.created = append(fake.created, command.Key)
	created := workspacemodule.Reference{
		Key: command.Key, Name: command.Name, DefaultBranch: command.DefaultBranch,
	}
	return &created, nil
}

func (fake *repositoryAdmissionCatalogFake) Resolve(_ context.Context, query workspacemodule.ResolveQuery) (*workspacemodule.Reference, error) {
	if query.Reference != fake.workspace.Key && query.Reference != fake.workspace.Name {
		return nil, workspacemodule.ErrNotFound
	}
	workspace := fake.workspace
	return &workspace, nil
}

func (fake *repositoryAdmissionCatalogFake) ListRepositories(context.Context, workspacemodule.ListRepositoriesQuery) ([]workspacemodule.Repository, error) {
	return append([]workspacemodule.Repository(nil), fake.repos...), nil
}

func (fake *repositoryAdmissionCatalogFake) Delete(
	_ context.Context,
	command workspacemodule.DeleteCommand,
) (*workspacemodule.Reference, error) {
	fake.deleted = append(fake.deleted, command.Reference)
	deleted := workspacemodule.Reference{Key: command.Reference}
	return &deleted, nil
}

func (fake *repositoryAdmissionCatalogFake) UnregisterRepository(
	_ context.Context,
	command workspacemodule.UnregisterRepositoryCommand,
) (*workspacemodule.Repository, error) {
	fake.unregistered = append(fake.unregistered, command.Name)
	return &workspacemodule.Repository{WorkspaceKey: command.WorkspaceReference, Name: command.Name}, nil
}

type repositoryAdmissionAgentsFake struct {
	repositoryadmission.ManagedAgentsCommands
}

func TestRepositoryAdmissionPlanCreateAllowsOnlyExactProtectedRecovery(t *testing.T) {
	journal, err := NewRepositoryAdmissionJournalAt(filepath.Join(t.TempDir(), "journal"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := t.TempDir()
	local := NewRepositoryAdmissionLocalWorkspace(
		&repositoryAdmissionCatalogFake{},
		&repositoryAdmissionAgentsFake{},
		nil,
		journal,
	).(*repositoryAdmissionLocalWorkspace)
	command := repositoryadmission.CreateCommand{
		Name: "Recovery Workspace", Type: "clone", Path: workspacePath,
		Branch: "main", CloneURLs: []string{"https://example.com/acme/repository.git"},
	}
	plan, err := local.PlanCreate(t.Context(), command)
	if err != nil {
		t.Fatalf("initial PlanCreate() error = %v", err)
	}
	operationID, err := repositoryadmission.OperationID("create_workspace", plan.WorkspaceKey, plan.WorkspacePath, plan.Repositories)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Prepare(t.Context(), repositoryadmission.LocalIntent{
		OperationID: operationID, WorkspaceKey: plan.WorkspaceKey,
		WorkspaceName: command.Name, WorkspacePath: plan.WorkspacePath,
		Kind: repositoryadmission.KindCreateWorkspace, Branch: command.Branch,
		CloneURLs: plan.CloneURLs,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspacePath, "partial-materialization"), []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}

	recovered, err := local.PlanCreate(t.Context(), command)
	if err != nil {
		t.Fatalf("exact recovery PlanCreate() error = %v", err)
	}
	if recovered.WorkspacePath != plan.WorkspacePath || recovered.WorkspaceKey != plan.WorkspaceKey {
		t.Fatalf("recovered plan = %#v; want exact original coordinates %#v", recovered, plan)
	}

	divergent := command
	divergent.CloneURLs = []string{"https://example.com/acme/different.git"}
	if _, err := local.PlanCreate(t.Context(), divergent); err == nil {
		t.Fatal("divergent PlanCreate() unexpectedly reused a non-empty recovery path")
	}
}

func TestRepositoryAdmissionPlanAddReplaysOriginalNamesOnlyForExactProtectedIntent(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	journal, err := NewRepositoryAdmissionJournalAt(filepath.Join(t.TempDir(), "journal"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := t.TempDir()
	if err := saveLocalWorkspaceState("WORK", workspacePath, nil); err != nil {
		t.Fatal(err)
	}
	catalog := &repositoryAdmissionCatalogFake{workspace: workspacemodule.Reference{
		Key: "WORK", Name: "Work", State: workspacemodule.StateReady, DefaultBranch: "main",
	}}
	local := NewRepositoryAdmissionLocalWorkspace(
		catalog,
		&repositoryAdmissionAgentsFake{},
		nil,
		journal,
	).(*repositoryAdmissionLocalWorkspace)
	command := repositoryadmission.AddRepositoriesCommand{
		WorkspaceID: "WORK", Branch: "main",
		CloneURLs: []string{"https://example.com/acme/repository.git"},
	}
	initial, err := local.PlanAdd(t.Context(), command)
	if err != nil {
		t.Fatalf("initial PlanAdd() error = %v", err)
	}
	operationID, err := repositoryadmission.OperationID("add_repositories", initial.WorkspaceKey, initial.WorkspacePath, initial.Repositories)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Prepare(t.Context(), repositoryadmission.LocalIntent{
		OperationID: operationID, WorkspaceKey: initial.WorkspaceKey,
		WorkspacePath: initial.WorkspacePath, Kind: repositoryadmission.KindAddRepositories,
		Branch: command.Branch, CloneURLs: initial.CloneURLs,
	}); err != nil {
		t.Fatal(err)
	}
	catalog.repos = []workspacemodule.Repository{{
		WorkspaceKey: "WORK", Name: initial.Repositories[0].Name,
		RemoteURL: initial.Repositories[0].RemoteURL, Remote: initial.Repositories[0].Remote,
		DefaultBranch: "main", SourceRepoID: initial.Repositories[0].SourceRepoID,
	}}

	replayed, err := local.PlanAdd(t.Context(), command)
	if err != nil {
		t.Fatalf("exact recovery PlanAdd() error = %v", err)
	}
	if got, want := replayed.Repositories[0].Name, initial.Repositories[0].Name; got != want {
		t.Fatalf("exact replay repository name = %q, want %q", got, want)
	}

	fresh := command
	fresh.Branch = "release"
	freshPlan, err := local.PlanAdd(t.Context(), fresh)
	if err != nil {
		t.Fatalf("fresh collision PlanAdd() error = %v", err)
	}
	if got, original := freshPlan.Repositories[0].Name, initial.Repositories[0].Name; got == original {
		t.Fatalf("fresh request reused protected repository name %q without an exact journal fact", got)
	}
}

func TestValidCommittedRepositoryAdmissionReplayRequiresMatchingFinalizationShape(t *testing.T) {
	repository := workspacemodule.Repository{Name: "repository", DefaultBranch: "main"}
	committed := &repositoryadmission.Record{
		State: "committed",
		Receipt: &repositoryadmission.Receipt{
			Repositories: []repositoryadmission.RepositoryReceipt{{Repository: repository}},
		},
	}
	if !validCommittedRepositoryAdmissionReplay(committed, false) {
		t.Fatal("committed add-repositories receipt was rejected")
	}
	if validCommittedRepositoryAdmissionReplay(committed, true) {
		t.Fatal("create replay without workspace finalization was accepted")
	}
	committed.Receipt.WorkspaceFinalization = &repositoryadmission.WorkspaceFinalization{State: "ready", DefaultBranch: "main"}
	if !validCommittedRepositoryAdmissionReplay(committed, true) {
		t.Fatal("committed create receipt with matching ready finalization was rejected")
	}
	committed.Receipt.WorkspaceFinalization.DefaultBranch = "other"
	if validCommittedRepositoryAdmissionReplay(committed, true) {
		t.Fatal("create replay with divergent default branch was accepted")
	}
	committed.State = "failed"
	if validCommittedRepositoryAdmissionReplay(committed, false) {
		t.Fatal("non-committed admission was accepted for replay")
	}
}

func TestPersistAddReposRecordsRollsBackClonedCheckoutOnLocalStateFailure(t *testing.T) {
	root := t.TempDir()
	configFile := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(configFile, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_CONFIG_DIR", configFile)

	checkoutPath := filepath.Join(root, "workspace", "repository")
	if err := os.MkdirAll(checkoutPath, 0o755); err != nil {
		t.Fatal(err)
	}
	placement := repositoryadmission.RepositoryPlacement{
		Name: "repository", Path: checkoutPath, Remote: "origin",
		DefaultBranch: "main", SourceRepoID: "repository",
	}
	catalog := &repositoryAdmissionCatalogFake{}

	err := persistAddReposRecords(
		t.Context(),
		catalog,
		"WORK",
		filepath.Dir(checkoutPath),
		"main",
		nil,
		[]repositoryadmission.RepositoryPlacement{placement},
		nil,
		[]repositoryadmission.RepositoryPlacement{placement},
		[]repositoryadmission.RepositoryPlacement{placement},
	)
	if err == nil {
		t.Fatal("persistAddReposRecords() succeeded with an unusable config path")
	}
	if _, statErr := os.Stat(checkoutPath); !os.IsNotExist(statErr) {
		t.Fatalf("failed admission retained checkout %q: stat error = %v", checkoutPath, statErr)
	}
	if len(catalog.unregistered) != 1 || catalog.unregistered[0] != placement.Name {
		t.Fatalf("unregistered repositories = %v, want [%s]", catalog.unregistered, placement.Name)
	}
}

func TestCreateEmptyWorkspaceRollsBackNewRootAndCatalogOnBootstrapFailure(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	workspacePath := filepath.Join(t.TempDir(), "new-workspace")
	catalog := &repositoryAdmissionCatalogFake{}

	_, err := createStoreBackedEmptyWorkspace(
		t.Context(),
		catalog,
		nil,
		repositoryadmission.CreateCommand{
			Name: "cleanup-workspace", Type: "empty", Path: workspacePath,
		},
	)
	if err == nil {
		t.Fatal("createStoreBackedEmptyWorkspace() succeeded without Agents bootstrap")
	}
	if _, statErr := os.Stat(workspacePath); !os.IsNotExist(statErr) {
		t.Fatalf("failed workspace admission retained new root %q: stat error = %v", workspacePath, statErr)
	}
	if len(catalog.created) != 1 || catalog.created[0] != "CLEANUP-WORKSPACE" {
		t.Fatalf("created workspaces = %v, want [CLEANUP-WORKSPACE]", catalog.created)
	}
	if len(catalog.deleted) != 1 || catalog.deleted[0] != "CLEANUP-WORKSPACE" {
		t.Fatalf("deleted workspaces = %v, want [CLEANUP-WORKSPACE]", catalog.deleted)
	}
}
