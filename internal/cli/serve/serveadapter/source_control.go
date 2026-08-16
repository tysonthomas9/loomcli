package serveadapter

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"

	"github.com/tysonthomas9/loomcli/internal/app/agentprovisioning"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

type workspaceDirectoryResolver func(string) string
type directoryEnsurer func(string, fs.FileMode) error

const sourceControlRepositoryAdmissionMaximumLeaseWindow = 30 * time.Minute

// sourceControlRepositoryResolver is the Workspace-owned machine-local
// projection behind Source Control. The public materialization command carries
// only an opaque repository ref; this adapter is the sole place that expands
// it to the durable token-free remote and local workspace root.
type sourceControlRepositoryResolver struct {
	workspaces      workspaceowner.WorkspaceStore
	repositories    workspaceowner.RepoStore
	admissions      infrafleetdb.RepositoryAdmissionTransport
	localAdmissions sourcecontrol.RepositoryAdmissionLocalResolver
	workspaceDir    workspaceDirectoryResolver
	ensureDir       directoryEnsurer
}

var _ sourcecontrol.RepositoryResolver = (*sourceControlRepositoryResolver)(nil)

func newSourceControlRepositoryResolver(
	workspaces workspaceowner.WorkspaceStore,
	repositories workspaceowner.RepoStore,
	workspaceDir workspaceDirectoryResolver,
	ensureDir directoryEnsurer,
) sourcecontrol.RepositoryResolver {
	if workspaces == nil || repositories == nil ||
		workspaceDir == nil || ensureDir == nil {
		return nil
	}
	return &sourceControlRepositoryResolver{
		workspaces: workspaces, repositories: repositories,
		workspaceDir: workspaceDir, ensureDir: ensureDir,
	}
}

func newSourceControlRepositoryResolverWithAdmissions(
	workspaces workspaceowner.WorkspaceStore,
	repositories workspaceowner.RepoStore,
	admissions infrafleetdb.RepositoryAdmissionTransport,
	localAdmissions sourcecontrol.RepositoryAdmissionLocalResolver,
	workspaceDir workspaceDirectoryResolver,
	ensureDir directoryEnsurer,
) sourcecontrol.RepositoryResolver {
	resolver, ok := newSourceControlRepositoryResolver(
		workspaces,
		repositories,
		workspaceDir,
		ensureDir,
	).(*sourceControlRepositoryResolver)
	if !ok || admissions == nil || localAdmissions == nil {
		return nil
	}
	resolver.admissions = admissions
	resolver.localAdmissions = localAdmissions
	return resolver
}

//nolint:funlen // Checkout resolution keeps remote normalization, local containment, and repository-admission routing in one fail-closed boundary.
func (resolver *sourceControlRepositoryResolver) ResolveRepositoryCheckout(
	ctx context.Context,
	workspaceKey,
	materializationID,
	repositoryRef string,
) (sourcecontrol.RepositoryCheckout, error) {
	if resolver == nil || resolver.workspaces == nil || resolver.repositories == nil ||
		resolver.workspaceDir == nil || resolver.ensureDir == nil {
		return sourcecontrol.RepositoryCheckout{}, sourcecontrol.ErrUnavailable
	}
	workspaceKey = strings.TrimSpace(workspaceKey)
	materializationID = strings.TrimSpace(materializationID)
	repositoryRef = strings.TrimSpace(repositoryRef)
	if workspaceKey == "" || materializationID == "" || repositoryRef == "" {
		return sourcecontrol.RepositoryCheckout{}, fmt.Errorf(
			"workspace, materialization, and repository reference are required: %w",
			sourcecontrol.ErrInvalid,
		)
	}
	admissionID, admissionMaterialization, err :=
		sourcecontrol.ParseRepositoryAdmissionMaterializationID(
			materializationID,
		)
	if err != nil {
		return sourcecontrol.RepositoryCheckout{}, fmt.Errorf(
			"repository admission materialization identity is invalid: %w",
			err,
		)
	}
	if admissionMaterialization {
		return resolver.resolveRepositoryAdmissionCheckout(
			ctx,
			workspaceKey,
			materializationID,
			admissionID,
			repositoryRef,
		)
	}
	workspace, err := resolver.workspaces.Get(ctx, workspaceKey)
	if err != nil {
		return sourcecontrol.RepositoryCheckout{}, fmt.Errorf("resolve workspace %q: %w", workspaceKey, err)
	}
	if workspace == nil || workspace.Key != workspaceKey ||
		!safeLocalPathSegment(workspace.Name) {
		return sourcecontrol.RepositoryCheckout{}, fmt.Errorf(
			"workspace %q returned invalid local projection: %w",
			workspaceKey,
			sourcecontrol.ErrInvalid,
		)
	}
	workspacePath, err := resolver.resolveWorkspacePath(workspaceKey, workspace.Name)
	if err != nil {
		return sourcecontrol.RepositoryCheckout{}, err
	}
	repository, err := resolver.resolveRepository(ctx, workspaceKey, repositoryRef)
	if err != nil {
		return sourcecontrol.RepositoryCheckout{}, err
	}
	return sourcecontrol.RepositoryCheckout{
		WorkspaceKey: workspaceKey, RepositoryRef: repositoryRef,
		RemoteURL: repository.RemoteURL, RemoteName: repository.Remote,
		WorkspacePath: workspacePath,
		CheckoutName:  repository.Name,
	}, nil
}

//nolint:cyclop,funlen // Admission checkout must validate every durable coordinate and containment outcome before publishing a repository path.
func (resolver *sourceControlRepositoryResolver) resolveRepositoryAdmissionCheckout(
	ctx context.Context,
	workspaceKey,
	materializationID,
	admissionID,
	repositoryRef string,
) (sourcecontrol.RepositoryCheckout, error) {
	if resolver == nil || resolver.admissions == nil ||
		resolver.localAdmissions == nil || resolver.ensureDir == nil {
		return sourcecontrol.RepositoryCheckout{}, sourcecontrol.ErrUnavailable
	}
	if ctx == nil {
		return sourcecontrol.RepositoryCheckout{}, sourcecontrol.ErrInvalid
	}
	if cause := context.Cause(ctx); cause != nil {
		return sourcecontrol.RepositoryCheckout{}, cause
	}
	local, err := resolver.localAdmissions.ResolveLocalRepositoryAdmission(
		ctx,
		admissionID,
	)
	if err != nil {
		return sourcecontrol.RepositoryCheckout{}, fmt.Errorf(
			"resolve local repository admission %q: %w",
			admissionID,
			err,
		)
	}
	if cause := context.Cause(ctx); cause != nil {
		return sourcecontrol.RepositoryCheckout{}, cause
	}
	record, err := resolver.admissions.GetRepositoryAdmission(
		ctx,
		workspaceKey,
		admissionID,
	)
	if err != nil {
		return sourcecontrol.RepositoryCheckout{}, fmt.Errorf(
			"resolve FleetDB repository admission %q: %w",
			admissionID,
			err,
		)
	}
	if cause := context.Cause(ctx); cause != nil {
		return sourcecontrol.RepositoryCheckout{}, cause
	}
	if record == nil ||
		record.State != "pending" ||
		!validRepositoryAdmissionLeaseShape(record) ||
		local.WorkspaceKey != workspaceKey ||
		local.AdmissionID != admissionID ||
		local.OperationID != record.OperationID ||
		local.OwnerID != record.OwnerID ||
		local.OwnerGenerationID != record.OwnerGenerationID ||
		local.SpecFingerprint != record.SpecFingerprint ||
		record.WorkspaceKey != workspaceKey ||
		record.AdmissionID != admissionID {
		return sourcecontrol.RepositoryCheckout{}, fmt.Errorf(
			"repository admission %q returned divergent durable coordinates: %w",
			admissionID,
			sourcecontrol.ErrInvalidMaterialization,
		)
	}
	expectedMaterializationID, err :=
		sourcecontrol.RepositoryAdmissionMaterializationID(
			sourcecontrol.RepositoryAdmissionCheckoutCommand{
				WorkspaceKey:      workspaceKey,
				AdmissionID:       admissionID,
				RepositoryRef:     repositoryRef,
				OwnerID:           record.OwnerID,
				OwnerGenerationID: record.OwnerGenerationID,
				SpecFingerprint:   record.SpecFingerprint,
			},
		)
	if err != nil || expectedMaterializationID != materializationID {
		return sourcecontrol.RepositoryCheckout{}, fmt.Errorf(
			"repository admission %q owner generation is no longer current: %w",
			admissionID,
			sourcecontrol.ErrInvalidMaterialization,
		)
	}
	workspacePath := filepath.Clean(strings.TrimSpace(local.WorkspacePath))
	if !filepath.IsAbs(workspacePath) ||
		workspacePath == "." ||
		workspacePath == string(filepath.Separator) ||
		workspacePath != local.WorkspacePath {
		return sourcecontrol.RepositoryCheckout{}, fmt.Errorf(
			"repository admission %q has invalid local checkout root: %w",
			admissionID,
			sourcecontrol.ErrInvalidMaterialization,
		)
	}
	if cause := context.Cause(ctx); cause != nil {
		return sourcecontrol.RepositoryCheckout{}, cause
	}
	if err := resolver.ensureDir(workspacePath, 0o700); err != nil {
		return sourcecontrol.RepositoryCheckout{}, fmt.Errorf(
			"prepare repository admission %q checkout root: %w",
			admissionID,
			err,
		)
	}
	if cause := context.Cause(ctx); cause != nil {
		return sourcecontrol.RepositoryCheckout{}, cause
	}
	var matched *infrafleetdb.RepositoryAdmissionRepoSpec
	for index := range record.Spec.Repositories {
		candidate := &record.Spec.Repositories[index]
		sourceID := strings.TrimSpace(candidate.SourceRepoID)
		if sourceID == "" {
			sourceID = candidate.Name
		}
		if candidate.Name != repositoryRef && sourceID != repositoryRef {
			continue
		}
		if matched != nil {
			return sourcecontrol.RepositoryCheckout{}, fmt.Errorf(
				"repository admission %q reference %q is ambiguous: %w",
				admissionID,
				repositoryRef,
				sourcecontrol.ErrInvalidMaterialization,
			)
		}
		matched = candidate
	}
	if matched == nil {
		return sourcecontrol.RepositoryCheckout{}, fmt.Errorf(
			"repository admission %q has no repository reference %q: %w",
			admissionID,
			repositoryRef,
			sourcecontrol.ErrRepositoryAdmissionNotFound,
		)
	}
	remoteName := strings.TrimSpace(matched.Remote)
	if remoteName == "" {
		remoteName = "origin"
	}
	if cause := context.Cause(ctx); cause != nil {
		return sourcecontrol.RepositoryCheckout{}, cause
	}
	return sourcecontrol.RepositoryCheckout{
		WorkspaceKey: workspaceKey, RepositoryRef: repositoryRef,
		RemoteURL: matched.RemoteURL, RemoteName: remoteName,
		WorkspacePath: workspacePath, CheckoutName: matched.Name,
	}, nil
}

// validRepositoryAdmissionLeaseShape validates only the Fleet-server-relative
// response contract. It deliberately does not compare the absolute expiry to
// this host's wall clock: hosts may be skewed in either direction. Runtime
// freshness authority comes from the resolver's process-local monotonic
// owner-generation gate plus the owner-scoped operation context, whose
// watchdog cancels before recovery eligibility.
func validRepositoryAdmissionLeaseShape(
	record *infrafleetdb.RepositoryAdmissionRecord,
) bool {
	return record != nil &&
		!record.UpdatedAt.IsZero() &&
		record.OwnerLeaseExpiresAt.After(record.UpdatedAt) &&
		!record.OwnerLeaseExpiresAt.After(
			record.UpdatedAt.Add(
				sourceControlRepositoryAdmissionMaximumLeaseWindow,
			),
		)
}

func (resolver *sourceControlRepositoryResolver) resolveWorkspacePath(
	workspaceKey,
	workspaceName string,
) (string, error) {
	workspacePath := ""
	stateCache, cacheErr := bootstrap.LoadStateCache()
	if cacheErr != nil {
		return "", fmt.Errorf(
			"resolve workspace %q local state: %w",
			workspaceKey,
			cacheErr,
		)
	}
	if stateCache != nil {
		workspacePath = strings.TrimSpace(stateCache.Workspaces[workspaceKey].Path)
	}
	if workspacePath == "" {
		workspacePath = filepath.Clean(resolver.workspaceDir(workspaceName))
	}
	if !filepath.IsAbs(workspacePath) ||
		filepath.Clean(workspacePath) != workspacePath ||
		workspacePath == "." ||
		workspacePath == string(filepath.Separator) {
		return "", fmt.Errorf(
			"workspace %q has invalid local checkout root: %w",
			workspaceKey,
			sourcecontrol.ErrInvalid,
		)
	}
	if err := resolver.ensureDir(workspacePath, 0o700); err != nil {
		return "", fmt.Errorf(
			"prepare workspace %q checkout root: %w",
			workspaceKey,
			err,
		)
	}
	return workspacePath, nil
}

func (resolver *sourceControlRepositoryResolver) RecordRepositoryCheckout(
	ctx context.Context,
	repository sourcecontrol.RepositoryCheckout,
	targetPath string,
) error {
	if resolver == nil {
		return sourcecontrol.ErrUnavailable
	}
	if ctx == nil {
		return sourcecontrol.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	targetPath = filepath.Clean(strings.TrimSpace(targetPath))
	if targetPath == "." || targetPath != filepath.Join(
		repository.WorkspacePath,
		repository.CheckoutName,
	) {
		return sourcecontrol.ErrInvalidMaterialization
	}
	return bootstrap.MutateWorkspaceLocalState(
		repository.WorkspaceKey,
		func(local *bootstrap.WorkspaceLocalState) error {
			if strings.TrimSpace(local.Path) == "" {
				local.Path = repository.WorkspacePath
			} else if filepath.Clean(local.Path) != repository.WorkspacePath {
				return fmt.Errorf("workspace local path changed: %w", sourcecontrol.ErrCheckoutConflict)
			}
			if local.Repos == nil {
				local.Repos = make(map[string]string)
			}
			if existing := strings.TrimSpace(local.Repos[repository.CheckoutName]); existing != "" &&
				filepath.Clean(existing) != targetPath {
				return fmt.Errorf("repository local path changed: %w", sourcecontrol.ErrCheckoutConflict)
			}
			local.Repos[repository.CheckoutName] = targetPath
			return nil
		},
	)
}

func (resolver *sourceControlRepositoryResolver) resolveRepository(
	ctx context.Context,
	workspaceKey,
	repositoryRef string,
) (*workspaceowner.Repository, error) {
	repository, err := resolver.repositories.Get(ctx, workspaceKey, repositoryRef)
	if err == nil {
		return validateSourceControlRepository(repository, workspaceKey, repositoryRef)
	}
	if !errors.Is(err, persistence.ErrNotFound) {
		return nil, fmt.Errorf("resolve repository %q: %w", repositoryRef, err)
	}
	repositories, listErr := resolver.repositories.List(ctx, workspaceKey)
	if listErr != nil {
		return nil, fmt.Errorf("list repositories for reference %q: %w", repositoryRef, listErr)
	}
	var matched *workspaceowner.Repository
	for _, candidate := range repositories {
		if candidate == nil {
			return nil, fmt.Errorf("repository list contains a nil projection: %w", sourcecontrol.ErrInvalid)
		}
		sourceID := strings.TrimSpace(candidate.SourceRepoID)
		if sourceID == "" {
			sourceID = candidate.Name
		}
		if sourceID != repositoryRef {
			continue
		}
		if matched != nil {
			return nil, fmt.Errorf(
				"repository reference %q is ambiguous: %w",
				repositoryRef,
				sourcecontrol.ErrInvalid,
			)
		}
		matched = candidate
	}
	if matched == nil {
		return nil, fmt.Errorf("repository reference %q: %w", repositoryRef, workspaceowner.ErrNotFound)
	}
	return validateSourceControlRepository(matched, workspaceKey, repositoryRef)
}

func validateSourceControlRepository(
	repository *workspaceowner.Repository,
	workspaceKey,
	repositoryRef string,
) (*workspaceowner.Repository, error) {
	if repository == nil || repository.WorkspaceKey != workspaceKey ||
		!safeLocalPathSegment(repository.Name) ||
		!safeTokenFreeRemote(repository.RemoteURL) {
		return nil, fmt.Errorf(
			"repository reference %q returned an invalid projection: %w",
			repositoryRef,
			sourcecontrol.ErrInvalid,
		)
	}
	out := *repository
	out.Groups = append([]string(nil), repository.Groups...)
	return &out, nil
}

func safeLocalPathSegment(value string) bool {
	return value != "" &&
		value == strings.TrimSpace(value) &&
		value != "." &&
		value != ".." &&
		filepath.Base(value) == value &&
		!strings.ContainsAny(value, "\x00\r\n")
}

func safeTokenFreeRemote(value string) bool {
	if value == "" || value != strings.TrimSpace(value) ||
		strings.ContainsAny(value, "\x00\r\n") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return true
}

// agentProvisioningWorkspaceLister provides the manager's recovery component
// with a fresh, sorted workspace set on every pass.
type agentProvisioningWorkspaceLister struct {
	workspaces workspaceowner.WorkspaceStore
}

var _ agentprovisioning.WorkspaceLister = (*agentProvisioningWorkspaceLister)(nil)

func newAgentProvisioningWorkspaceLister(
	workspaces workspaceowner.WorkspaceStore,
) agentprovisioning.WorkspaceLister {
	if workspaces == nil {
		return nil
	}
	return &agentProvisioningWorkspaceLister{workspaces: workspaces}
}

func (lister *agentProvisioningWorkspaceLister) ListWorkspaceKeys(
	ctx context.Context,
) ([]string, error) {
	if lister == nil || lister.workspaces == nil {
		return nil, agentprovisioning.ErrUnavailable
	}
	values, err := lister.workspaces.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list AgentProvisioning workspaces: %w", err)
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, workspace := range values {
		if workspace == nil || workspace.Key == "" ||
			workspace.Key != strings.TrimSpace(workspace.Key) {
			return nil, fmt.Errorf(
				"workspace list contains invalid state: %w",
				agentprovisioning.ErrConflict,
			)
		}
		if _, duplicate := seen[workspace.Key]; duplicate {
			return nil, fmt.Errorf(
				"workspace list contains duplicate %q: %w",
				workspace.Key,
				agentprovisioning.ErrConflict,
			)
		}
		seen[workspace.Key] = struct{}{}
		out = append(out, workspace.Key)
	}
	sort.Strings(out)
	return out, nil
}
