package sourcecontrol

import (
	"context"
	"errors"
	"testing"
)

type portsLayout struct{}

func (portsLayout) ResolveAgentCheckout(context.Context, string, string) (AgentCheckout, error) {
	return AgentCheckout{}, errors.New("unused")
}
func (portsLayout) ListAgentCheckouts(context.Context, string) ([]AgentCheckout, error) {
	return nil, nil
}
func (portsLayout) ListRepositoryCheckouts(context.Context, string) ([]RepositoryCheckoutView, error) {
	return nil, nil
}
func (portsLayout) SetRepositoryDefaultBranch(context.Context, string, string, string) error {
	return nil
}

type portsGitBrowse struct{}

type portsFiles struct{}

func (portsFiles) ListDirectoryAuthorized(context.Context, string, FileScope, string, string, string, bool) (*FileTreeResult, error) {
	return &FileTreeResult{}, nil
}
func (portsFiles) ReadFileAuthorized(context.Context, string, FileScope, string, string, string, bool) (*FileReadResult, error) {
	return &FileReadResult{}, nil
}
func (portsFiles) StatPathAuthorized(context.Context, string, FileScope, string, string, string, bool) (*FileStatResult, error) {
	return &FileStatResult{}, nil
}
func (portsFiles) ReadFileAtRevisionAuthorized(context.Context, string, FileScope, string, string, string, string, bool) (*FileReadResult, error) {
	return &FileReadResult{}, nil
}
func (portsFiles) IndexFilesAuthorized(context.Context, string, FileScope, string, string, bool) (*FileIndexResult, error) {
	return &FileIndexResult{}, nil
}
func (portsFiles) SearchFilesAuthorized(context.Context, string, FileScope, string, string, FileSearchRequest, bool) (*FileSearchResult, error) {
	return &FileSearchResult{}, nil
}
func (portsFiles) DiffPathAuthorized(context.Context, string, FileScope, string, string, string, string, string, bool) (*FileDiffResult, error) {
	return &FileDiffResult{}, nil
}
func (portsFiles) BlamePathAuthorized(context.Context, string, FileScope, string, string, string, bool) (*FileBlameResult, error) {
	return &FileBlameResult{}, nil
}
func (portsFiles) PathHistoryAuthorized(context.Context, string, FileScope, string, string, string, bool) (*FileHistoryResult, error) {
	return &FileHistoryResult{}, nil
}
func (portsFiles) WriteFileAuthorized(context.Context, string, FileScope, string, string, string, string, FileWritePreconditions, bool) (*FileMutationResult, error) {
	return &FileMutationResult{}, nil
}
func (portsFiles) DeletePathAuthorized(context.Context, string, FileScope, string, string, string, bool, string, bool) error {
	return nil
}
func (portsFiles) CreateDirectoryAuthorized(context.Context, string, FileScope, string, string, string, bool) error {
	return nil
}
func (portsFiles) MovePathAuthorized(context.Context, string, FileScope, string, string, string, string, bool, string, string, bool) (*FileMutationResult, error) {
	return &FileMutationResult{}, nil
}
func (portsFiles) StatusAuthorized(context.Context, string, FileScope, string, string, bool) (FileGitStatusResult, error) {
	return FileGitStatusResult{}, nil
}
func (portsFiles) ListCheckoutsAuthorized(context.Context, string, bool) (*FileCheckoutsResult, error) {
	return &FileCheckoutsResult{}, nil
}
func (portsFiles) RepairCheckoutAuthorized(context.Context, string, FileCheckoutRepairRequest) (*RepairResult, error) {
	return &RepairResult{}, nil
}

func (portsGitBrowse) DiffStat(context.Context, string, string) (DiffStat, error) {
	return DiffStat{}, nil
}

func (portsGitBrowse) ResolveMergeBase(context.Context, string, string) (string, error) {
	return "main", nil
}

func (portsGitBrowse) DiffCommits(context.Context, string, string, int) ([]DiffCommit, error) {
	return nil, nil
}

func (portsGitBrowse) DiffFiles(context.Context, string, string, string) ([]DiffFile, error) {
	return nil, nil
}

func (portsGitBrowse) DiffFilePatch(context.Context, string, string, string, string) (*DiffFilePatch, error) {
	return &DiffFilePatch{}, nil
}

func TestNewSourceControlExposesOnlyApprovedPorts(t *testing.T) {
	grants := NewAccessGrantIssuer()
	ports, err := NewWorkspacePorts(
		grants,
		portsLayout{},
		portsGitBrowse{},
		portsFiles{},
		&branchMechanicsStub{},
		&forgeStub{},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if ports.Browse == nil || ports.Mutate == nil || ports.Checkout == nil {
		t.Fatalf("New() ports = %#v, want Browse, Mutate, and Checkout", ports)
	}
}

func TestNewSourceControlRejectsMissingPrivateAdapters(t *testing.T) {
	tests := []struct {
		name     string
		layout   CheckoutLayout
		git      GitBrowseMechanics
		files    workspaceFileAdapter
		branches BranchMechanics
		forge    ForgePublication
	}{
		{name: "layout", git: portsGitBrowse{}, files: portsFiles{}, branches: &branchMechanicsStub{}, forge: &forgeStub{}},
		{name: "git", layout: portsLayout{}, files: portsFiles{}, branches: &branchMechanicsStub{}, forge: &forgeStub{}},
		{name: "files", layout: portsLayout{}, git: portsGitBrowse{}, branches: &branchMechanicsStub{}, forge: &forgeStub{}},
		{name: "branches", layout: portsLayout{}, git: portsGitBrowse{}, files: portsFiles{}, forge: &forgeStub{}},
		{name: "forge", layout: portsLayout{}, git: portsGitBrowse{}, files: portsFiles{}, branches: &branchMechanicsStub{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewWorkspacePorts(NewAccessGrantIssuer(), test.layout, test.git, test.files, test.branches, test.forge)
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("New() error = %v, want ErrUnavailable", err)
			}
		})
	}
}

func TestNewSourceControlRejectsMissingGrantIssuer(t *testing.T) {
	_, err := NewWorkspacePorts(
		AccessGrantIssuer{}, portsLayout{}, portsGitBrowse{}, portsFiles{},
		&branchMechanicsStub{}, &forgeStub{},
	)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("NewWorkspacePorts() error = %v, want ErrUnavailable", err)
	}
}

func TestWorkspacePortsRequireExplicitAccessGrant(t *testing.T) {
	grants := NewAccessGrantIssuer()
	ports, err := NewWorkspacePorts(
		grants,
		portsLayout{},
		portsGitBrowse{},
		portsFiles{},
		&branchMechanicsStub{},
		&forgeStub{},
	)
	if err != nil {
		t.Fatalf("NewWorkspacePorts() error = %v", err)
	}
	location := FileLocation{WorkspaceKey: "workspace", Scope: ScopeWorkspace}

	if _, err := ports.Browse.ListDirectory(context.Background(), PathQuery{Location: location}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("zero-grant Browse error = %v, want ErrForbidden", err)
	}
	readGrant := grants.Read(false)
	if _, err := ports.Browse.ListDirectory(context.Background(), PathQuery{Grant: readGrant, Location: location}); err != nil {
		t.Fatalf("read-granted Browse error = %v", err)
	}
	if _, err := ports.Mutate.WriteFile(context.Background(), WriteCommand{Grant: readGrant, Location: location, Path: "new.txt", Content: "x"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("read-only Mutate error = %v, want ErrForbidden", err)
	}
	if _, err := ports.Checkout.Repair(context.Background(), RepairCommand{Grant: readGrant, Location: location}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("read-only Repair error = %v, want ErrForbidden", err)
	}
	foreignGrant := NewAccessGrantIssuer().ReadWrite(true)
	if _, err := ports.Browse.ListDirectory(context.Background(), PathQuery{Grant: foreignGrant, Location: location}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("foreign-issuer Browse error = %v, want ErrForbidden", err)
	}
}

func TestSensitivePathRequiresExplicitSensitiveGrant(t *testing.T) {
	grants := NewAccessGrantIssuer()
	if capabilities := grants.Read(false).Capabilities(); capabilities.Sensitive {
		t.Fatal("ordinary read grant unexpectedly carries sensitive access")
	}
	if capabilities := grants.Read(true).Capabilities(); !capabilities.Sensitive {
		t.Fatal("explicit sensitive read grant lost its sensitive access")
	}
}
