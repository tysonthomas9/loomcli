package sourcecontrol

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// Browse is Source Control's read-only interface. Callers identify product
// resources; checkout paths, filesystem mechanics, and Git mechanics remain
// private to the module.
type Browse interface {
	DiffStat(context.Context, AgentQuery) (AgentDiffStat, error)
	DiffCommits(context.Context, DiffCommitsQuery) ([]DiffCommit, error)
	DiffFiles(context.Context, DiffFilesQuery) ([]DiffFile, error)
	DiffFilePatch(context.Context, DiffFilePatchQuery) (*DiffFilePatch, error)
	ListDirectory(context.Context, PathQuery) (*FileTreeResult, error)
	ReadFile(context.Context, PathQuery) (*FileReadResult, error)
	StatPath(context.Context, PathQuery) (*FileStatResult, error)
	ReadFileAtRevision(context.Context, RevisionQuery) (*FileReadResult, error)
	IndexFiles(context.Context, LocationQuery) (*FileIndexResult, error)
	SearchFiles(context.Context, SearchQuery) (*FileSearchResult, error)
	DiffPath(context.Context, PathDiffQuery) (*FileDiffResult, error)
	BlamePath(context.Context, PathQuery) (*FileBlameResult, error)
	PathHistory(context.Context, PathQuery) (*FileHistoryResult, error)
}

// WorkspaceLayout is the opaque Workspace-placement port consumed by Source
// Control. Workspace owns repository membership and approved local placement;
// Source Control never reconstructs checkout paths from naming conventions.
type WorkspaceLayout interface {
	ResolveAgentCheckout(context.Context, string, string) (AgentCheckout, error)
}

// GitBrowseMechanics is the private adapter seam for machine-local Git reads.
// Source Control owns validation, default-base selection, and error policy.
type GitBrowseMechanics interface {
	DiffStat(context.Context, string, string) (DiffStat, error)
	ResolveMergeBase(context.Context, string, string) (string, error)
	DiffCommits(context.Context, string, string, int) ([]DiffCommit, error)
	DiffFiles(context.Context, string, string, string) ([]DiffFile, error)
	DiffFilePatch(context.Context, string, string, string, string) (*DiffFilePatch, error)
}

// AgentQuery identifies an agent-scoped Source Control read.
type AgentQuery struct {
	WorkspaceKey string
	AgentID      string
}

// DiffStat is the private Git adapter's checkout-relative summary.
type DiffStat struct {
	FilesChanged int
	LinesAdded   int
	LinesRemoved int
}

// AgentDiffStat adds caller-meaningful branch identity without revealing the
// machine-local checkout path.
type AgentDiffStat struct {
	Branch       string
	FilesChanged int
	LinesAdded   int
	LinesRemoved int
}

// AgentCheckout is the trusted machine-local placement returned by Workspace.
// It is an adapter type, not a caller-supplied command.
type AgentCheckout struct {
	WorkspaceKey  string
	AgentID       string
	RepositoryRef string
	CheckoutPath  string
	Branch        string
	DefaultBranch string
	Remote        string
	IsWorkspace   bool
}

// DiffCommitsQuery identifies an agent checkout without exposing its path.
// An empty From asks Source Control to resolve the merge base against the
// checkout's Workspace-owned default branch.
type DiffCommitsQuery struct {
	WorkspaceKey string
	AgentID      string
	From         string
	Limit        int
}

// DiffCommit is transport-neutral commit metadata.
type DiffCommit struct {
	Hash      string
	ShortHash string
	Subject   string
	Author    string
	Email     string
	Date      string
}

// DiffFilesQuery selects the changed files between two refs. To is required;
// an empty From uses the merge base against Workspace's default branch.
type DiffFilesQuery struct {
	WorkspaceKey string
	AgentID      string
	From         string
	To           string
}

// DiffFile is transport-neutral changed-file metadata.
type DiffFile struct {
	Path      string
	Status    string
	OldPath   string
	Additions int
	Deletions int
}

// DiffFilePatchQuery selects one repository-relative file patch.
type DiffFilePatchQuery struct {
	WorkspaceKey string
	AgentID      string
	From         string
	To           string
	Path         string
}

// DiffFilePatch is a bounded, transport-neutral unified diff result.
type DiffFilePatch struct {
	Patch      string
	IsBinary   bool
	IsTooLarge bool
	Additions  int
	Deletions  int
}

type browseModule struct {
	layout WorkspaceLayout
	git    GitBrowseMechanics
}

// NewBrowse composes Source Control's read-only interface over opaque
// Workspace placement and private local Git mechanics.
func newDiffBrowse(layout WorkspaceLayout, git GitBrowseMechanics) (*browseModule, error) {
	if layout == nil || git == nil {
		return nil, fmt.Errorf("compose Source Control Browse: layout and Git mechanics are required: %w", ErrUnavailable)
	}
	return &browseModule{layout: layout, git: git}, nil
}

func (module *browseModule) DiffStat(
	ctx context.Context,
	query AgentQuery,
) (AgentDiffStat, error) {
	query.WorkspaceKey = strings.TrimSpace(query.WorkspaceKey)
	query.AgentID = strings.TrimSpace(query.AgentID)
	if ctx == nil || query.WorkspaceKey == "" || query.AgentID == "" {
		return AgentDiffStat{}, fmt.Errorf("diff stat requires context, workspace, and agent: %w", ErrInvalid)
	}
	checkout, err := module.layout.ResolveAgentCheckout(ctx, query.WorkspaceKey, query.AgentID)
	if err != nil {
		return AgentDiffStat{}, fmt.Errorf("resolve agent checkout: %w", err)
	}
	if err := validateAgentCheckout(DiffCommitsQuery{
		WorkspaceKey: query.WorkspaceKey,
		AgentID:      query.AgentID,
	}, checkout); err != nil {
		return AgentDiffStat{}, err
	}
	stat, err := module.git.DiffStat(ctx, checkout.CheckoutPath, checkout.DefaultBranch)
	if err != nil {
		return AgentDiffStat{}, fmt.Errorf("read diff stat: %w", err)
	}
	return AgentDiffStat{
		Branch: checkout.Branch, FilesChanged: stat.FilesChanged,
		LinesAdded: stat.LinesAdded, LinesRemoved: stat.LinesRemoved,
	}, nil
}

func (module *browseModule) DiffCommits(
	ctx context.Context,
	query DiffCommitsQuery,
) ([]DiffCommit, error) {
	query.WorkspaceKey = strings.TrimSpace(query.WorkspaceKey)
	query.AgentID = strings.TrimSpace(query.AgentID)
	query.From = strings.TrimSpace(query.From)
	if ctx == nil || query.WorkspaceKey == "" || query.AgentID == "" || query.Limit < 0 {
		return nil, fmt.Errorf("diff commits requires context, workspace, agent, and a non-negative limit: %w", ErrInvalid)
	}
	if query.From != "" && !validBrowseGitRef(query.From) {
		return nil, fmt.Errorf("diff commits ref is invalid: %w", ErrInvalid)
	}
	checkout, err := module.layout.ResolveAgentCheckout(ctx, query.WorkspaceKey, query.AgentID)
	if err != nil {
		return nil, fmt.Errorf("resolve agent checkout: %w", err)
	}
	if err := validateAgentCheckout(query, checkout); err != nil {
		return nil, err
	}
	from := query.From
	if from == "" {
		from, err = module.git.ResolveMergeBase(ctx, checkout.CheckoutPath, checkout.DefaultBranch)
		if err != nil {
			return nil, fmt.Errorf("resolve diff base: %w", err)
		}
		from = strings.TrimSpace(from)
		if !validBrowseGitRef(from) {
			return nil, fmt.Errorf("resolved diff base is invalid: %w", ErrInvalidMaterialization)
		}
	}
	commits, err := module.git.DiffCommits(ctx, checkout.CheckoutPath, from, query.Limit)
	if err != nil {
		return nil, fmt.Errorf("read diff commits: %w", err)
	}
	if commits == nil {
		commits = []DiffCommit{}
	}
	return commits, nil
}

func (module *browseModule) DiffFiles(
	ctx context.Context,
	query DiffFilesQuery,
) ([]DiffFile, error) {
	query.WorkspaceKey = strings.TrimSpace(query.WorkspaceKey)
	query.AgentID = strings.TrimSpace(query.AgentID)
	query.From = strings.TrimSpace(query.From)
	query.To = strings.TrimSpace(query.To)
	if ctx == nil || query.WorkspaceKey == "" || query.AgentID == "" ||
		!validBrowseGitRef(query.To) ||
		(query.From != "" && !validBrowseGitRef(query.From)) {
		return nil, fmt.Errorf("diff files requires context, workspace, agent, and valid refs: %w", ErrInvalid)
	}
	checkout, err := module.layout.ResolveAgentCheckout(ctx, query.WorkspaceKey, query.AgentID)
	if err != nil {
		return nil, fmt.Errorf("resolve agent checkout: %w", err)
	}
	if err := validateAgentCheckout(DiffCommitsQuery{
		WorkspaceKey: query.WorkspaceKey,
		AgentID:      query.AgentID,
	}, checkout); err != nil {
		return nil, err
	}
	from := query.From
	if from == "" {
		from, err = module.git.ResolveMergeBase(ctx, checkout.CheckoutPath, checkout.DefaultBranch)
		if err != nil {
			return nil, fmt.Errorf("resolve diff base: %w", err)
		}
		from = strings.TrimSpace(from)
		if !validBrowseGitRef(from) {
			return nil, fmt.Errorf("resolved diff base is invalid: %w", ErrInvalidMaterialization)
		}
	}
	files, err := module.git.DiffFiles(ctx, checkout.CheckoutPath, from, query.To)
	if err != nil {
		return nil, fmt.Errorf("read diff files: %w", err)
	}
	if files == nil {
		files = []DiffFile{}
	}
	return files, nil
}

func (module *browseModule) DiffFilePatch(
	ctx context.Context,
	query DiffFilePatchQuery,
) (*DiffFilePatch, error) {
	query.WorkspaceKey = strings.TrimSpace(query.WorkspaceKey)
	query.AgentID = strings.TrimSpace(query.AgentID)
	query.From = strings.TrimSpace(query.From)
	query.To = strings.TrimSpace(query.To)
	query.Path = strings.TrimSpace(query.Path)
	if ctx == nil || query.WorkspaceKey == "" || query.AgentID == "" ||
		!validBrowseGitRef(query.To) ||
		(query.From != "" && !validBrowseGitRef(query.From)) ||
		!validBrowsePath(query.Path) {
		return nil, fmt.Errorf("diff file patch requires context, workspace, agent, valid refs, and a contained path: %w", ErrInvalid)
	}
	checkout, err := module.layout.ResolveAgentCheckout(ctx, query.WorkspaceKey, query.AgentID)
	if err != nil {
		return nil, fmt.Errorf("resolve agent checkout: %w", err)
	}
	if err := validateAgentCheckout(DiffCommitsQuery{
		WorkspaceKey: query.WorkspaceKey,
		AgentID:      query.AgentID,
	}, checkout); err != nil {
		return nil, err
	}
	from := query.From
	if from == "" {
		from, err = module.git.ResolveMergeBase(ctx, checkout.CheckoutPath, checkout.DefaultBranch)
		if err != nil {
			return nil, fmt.Errorf("resolve diff base: %w", err)
		}
		from = strings.TrimSpace(from)
		if !validBrowseGitRef(from) {
			return nil, fmt.Errorf("resolved diff base is invalid: %w", ErrInvalidMaterialization)
		}
	}
	patch, err := module.git.DiffFilePatch(
		ctx,
		checkout.CheckoutPath,
		from,
		query.To,
		query.Path,
	)
	if err != nil {
		return nil, fmt.Errorf("read diff file patch: %w", err)
	}
	if patch == nil {
		return nil, fmt.Errorf("git adapter returned no patch: %w", ErrInvalidMaterialization)
	}
	return patch, nil
}

func validateAgentCheckout(query DiffCommitsQuery, checkout AgentCheckout) error {
	if checkout.WorkspaceKey != query.WorkspaceKey ||
		checkout.AgentID != query.AgentID ||
		strings.TrimSpace(checkout.RepositoryRef) == "" ||
		strings.TrimSpace(checkout.CheckoutPath) == "" ||
		!filepath.IsAbs(checkout.CheckoutPath) ||
		filepath.Clean(checkout.CheckoutPath) != checkout.CheckoutPath ||
		!validBrowseGitRef(strings.TrimSpace(checkout.DefaultBranch)) {
		return fmt.Errorf("workspace returned invalid agent checkout coordinates: %w", ErrInvalidMaterialization)
	}
	return nil
}

func validBrowseGitRef(ref string) bool {
	if ref == "" || strings.Contains(ref, "..") || strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") {
		return false
	}
	if !isBrowseGitRefAlphaNumeric(rune(ref[0])) {
		return false
	}
	for _, value := range ref {
		if !isBrowseGitRefCharacter(value) {
			return false
		}
	}
	return true
}

func isBrowseGitRefCharacter(value rune) bool {
	return isBrowseGitRefAlphaNumeric(value) || value == '_' || value == '.' || value == '/' || value == '-'
}

func isBrowseGitRefAlphaNumeric(value rune) bool {
	return (value >= 'a' && value <= 'z') ||
		(value >= 'A' && value <= 'Z') ||
		(value >= '0' && value <= '9')
}

func validBrowsePath(value string) bool {
	if value == "" || filepath.IsAbs(value) || strings.ContainsRune(value, '\x00') {
		return false
	}
	clean := filepath.Clean(value)
	return clean == value && clean != "." && clean != ".." &&
		!strings.HasPrefix(clean, ".."+string(filepath.Separator))
}
