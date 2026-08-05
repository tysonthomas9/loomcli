package connectors

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// Service implements the minimal credential-broker surface over one bounded
// Git executor. It never receives or returns plaintext provider credentials.
type Service struct {
	executor    GitReadExecutor
	grants      ConnectorGrantStore
	admission   *authority.Admission
	coordinator *gitReadCoordinator
}

var (
	_ GitReadBroker = (*Service)(nil)
	_ GrantCommands = (*Service)(nil)
)

func New(executor GitReadExecutor, admission *authority.Admission) (*Service, error) {
	if executor == nil || admission == nil {
		return nil, fmt.Errorf("compose Connectors Git broker: executor and admission are required: %w", ErrUnavailable)
	}
	return &Service{
		executor:    executor,
		admission:   admission,
		coordinator: newGitReadCoordinator(),
	}, nil
}

// NewWithGrants composes the complete Phase 5 Connectors boundary. New
// remains available for the existing Git-only composition while migration is
// in progress; its EnsureGrant method fails closed with ErrUnavailable.
func NewWithGrants(
	executor GitReadExecutor,
	grants ConnectorGrantStore,
	admission *authority.Admission,
) (*Service, error) {
	service, err := New(executor, admission)
	if err != nil {
		return nil, err
	}
	if grants == nil {
		return nil, fmt.Errorf("compose Connectors grants: grant store is required: %w", ErrUnavailable)
	}
	service.grants = grants
	return service, nil
}

// EnsureGrant creates or reuses one exact active binding-scoped Connector
// grant. FleetDB exposes Create and binding-filtered List, but no Get route.
// The initial list avoids unnecessary conflicts; the second list resolves a
// concurrent-create race without weakening immutable GrantID semantics.
func (s *Service) EnsureGrant(
	ctx context.Context,
	auth authority.SystemAuthority,
	command EnsureGrantCommand,
) (*ConnectorGrant, error) {
	command, err := normalizeEnsureGrantCommand(command)
	if err != nil {
		return nil, err
	}
	if s == nil || s.admission == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.admission.RequireSystem(ActionEnsureGrant, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if s.grants == nil {
		return nil, ErrUnavailable
	}

	existing, err := s.findGrant(ctx, command)
	if err != nil || existing != nil {
		return existing, err
	}
	created, err := s.grants.CreateGrant(ctx, CreateGrantMutation(command))
	if err == nil {
		if err := validateExactGrant(created, command); err != nil {
			return nil, err
		}
		return cloneConnectorGrant(created), nil
	}
	if !errors.Is(err, ErrGrantConflict) {
		return nil, fmt.Errorf("create connector grant %q: %w", command.GrantID, err)
	}

	// A concurrent creator may have committed the same immutable tuple after
	// our first list. Re-read the only backend query the Fleet contract
	// exposes and accept only exact active reuse.
	existing, readErr := s.findGrant(ctx, command)
	if readErr != nil {
		return nil, readErr
	}
	if existing == nil {
		return nil, fmt.Errorf("create connector grant %q raced without an exact active result: %w", command.GrantID, ErrGrantConflict)
	}
	return existing, nil
}

func (s *Service) findGrant(ctx context.Context, command EnsureGrantCommand) (*ConnectorGrant, error) {
	values, err := s.grants.ListGrantsByBinding(ctx, command.WorkspaceKey, command.BindingID)
	if err != nil {
		return nil, fmt.Errorf("list connector grants for binding %q: %w", command.BindingID, err)
	}
	seen := make(map[string]struct{}, len(values))
	var matched *ConnectorGrant
	for _, value := range values {
		if err := validatePersistedGrant(value, command.WorkspaceKey, command.BindingID); err != nil {
			return nil, err
		}
		if _, duplicate := seen[value.GrantID]; duplicate {
			return nil, fmt.Errorf("duplicate active connector grant id %q: %w", value.GrantID, ErrInvalidPersistedState)
		}
		seen[value.GrantID] = struct{}{}
		if value.GrantID != command.GrantID {
			continue
		}
		if !grantMatchesCommand(value, command) {
			return nil, fmt.Errorf("connector grant id %q has different immutable coordinates: %w", command.GrantID, ErrGrantConflict)
		}
		matched = cloneConnectorGrant(value)
	}
	return matched, nil
}

//nolint:funlen // Execution keeps authority, coordination, credential brokerage, and bounded-result validation together.
func (s *Service) ExecuteGitRead(
	ctx context.Context,
	auth authority.SystemAuthority,
	command GitReadCommand,
) (GitReadReceipt, error) {
	command, err := normalizeGitReadCommand(command)
	if err != nil {
		return GitReadReceipt{}, err
	}
	if s == nil || s.admission == nil {
		return GitReadReceipt{}, authority.ErrAdmissionDenied
	}
	if err := s.admission.RequireSystem(ActionExecuteGitRead, command.WorkspaceKey, auth); err != nil {
		return GitReadReceipt{}, err
	}
	if s.executor == nil {
		return GitReadReceipt{}, ErrUnavailable
	}
	canonicalTarget, err := s.validatedGitReadTarget(ctx, command)
	if err != nil {
		return GitReadReceipt{}, err
	}
	releaseOperation, replay, err := s.coordinator.acquireOperation(ctx, gitReadCoordinates{
		workspaceKey:    command.WorkspaceKey,
		operationID:     command.OperationID,
		repositoryRef:   command.RepositoryRef,
		operation:       command.Operation,
		remoteURL:       command.RemoteURL,
		workspacePath:   command.WorkspacePath,
		targetPath:      command.TargetPath,
		remoteName:      command.RemoteName,
		sourceRef:       command.SourceRef,
		destinationRef:  command.DestinationRef,
		canonicalTarget: canonicalTarget,
	})
	if err != nil {
		return GitReadReceipt{}, err
	}
	if replay {
		return gitReadReceiptFor(command), nil
	}
	completed := false
	defer func() {
		releaseOperation(completed)
	}()
	releaseTarget, err := s.coordinator.acquireTarget(ctx, canonicalTarget)
	if err != nil {
		return GitReadReceipt{}, err
	}
	defer releaseTarget()
	revalidated, err := s.validatedGitReadTarget(ctx, command)
	if err != nil {
		return GitReadReceipt{}, err
	}
	if revalidated != canonicalTarget {
		return GitReadReceipt{}, fmt.Errorf("%w: Git read target identity changed", ErrInvalid)
	}
	if err := s.executor.ExecuteGitRead(ctx, command); err != nil {
		return GitReadReceipt{}, fmt.Errorf(
			"execute %s for repository %q operation %q: %w",
			command.Operation,
			command.RepositoryRef,
			command.OperationID,
			err,
		)
	}
	completed = true
	return gitReadReceiptFor(command), nil
}

func (s *Service) validatedGitReadTarget(
	ctx context.Context,
	command GitReadCommand,
) (string, error) {
	canonicalTarget, err := s.executor.ValidateGitRead(ctx, command)
	if err != nil {
		return "", fmt.Errorf("validate Git read containment: %w", err)
	}
	if canonicalTarget == "" ||
		!filepath.IsAbs(canonicalTarget) ||
		filepath.Clean(canonicalTarget) != canonicalTarget {
		return "", fmt.Errorf("%w: executor returned an invalid canonical target", ErrInvalid)
	}
	return canonicalTarget, nil
}

func gitReadReceiptFor(command GitReadCommand) GitReadReceipt {
	return GitReadReceipt{
		WorkspaceKey:   command.WorkspaceKey,
		OperationID:    command.OperationID,
		RepositoryRef:  command.RepositoryRef,
		Operation:      command.Operation,
		TargetPath:     command.TargetPath,
		RemoteName:     command.RemoteName,
		SourceRef:      command.SourceRef,
		DestinationRef: command.DestinationRef,
	}
}

//nolint:funlen // Command normalization validates the complete bounded git-read coordinate and option contract.
func normalizeGitReadCommand(command GitReadCommand) (GitReadCommand, error) {
	var err error
	if command.WorkspaceKey, err = requireCanonical("workspace", command.WorkspaceKey); err != nil {
		return GitReadCommand{}, err
	}
	if command.OperationID, err = requireCanonical("operation id", command.OperationID); err != nil {
		return GitReadCommand{}, err
	}
	if command.RepositoryRef, err = requireCanonical("repository ref", command.RepositoryRef); err != nil {
		return GitReadCommand{}, err
	}
	command.Operation = GitReadOperation(strings.TrimSpace(string(command.Operation)))
	if command.Operation != GitReadClone && command.Operation != GitReadFetchRef {
		return GitReadCommand{}, fmt.Errorf("%w: Git read %q", ErrUnsupportedOperation, command.Operation)
	}
	command.RemoteURL, err = normalizeTokenFreeRemote(command.RemoteURL)
	if err != nil {
		return GitReadCommand{}, err
	}
	command.WorkspacePath, err = requireAbsolutePath("workspace path", command.WorkspacePath)
	if err != nil {
		return GitReadCommand{}, err
	}
	command.TargetPath, err = requireAbsolutePath("target path", command.TargetPath)
	if err != nil {
		return GitReadCommand{}, err
	}
	if command.TargetPath == command.WorkspacePath || !pathContains(command.WorkspacePath, command.TargetPath) {
		return GitReadCommand{}, fmt.Errorf("%w: target path must be inside the workspace path", ErrInvalid)
	}
	switch command.Operation {
	case GitReadClone:
		if command.RemoteName == "" {
			command.RemoteName = "origin"
		}
		if command.RemoteName, err = requireRemoteName(command.RemoteName); err != nil {
			return GitReadCommand{}, err
		}
		if command.SourceRef != "" || command.DestinationRef != "" {
			return GitReadCommand{}, fmt.Errorf("%w: clone cannot carry fetch refs", ErrInvalid)
		}
	case GitReadFetchRef:
		if command.RemoteName, err = requireRemoteName(command.RemoteName); err != nil {
			return GitReadCommand{}, err
		}
		if command.SourceRef, err = requireFetchSourceRef(command.SourceRef); err != nil {
			return GitReadCommand{}, err
		}
		if command.DestinationRef, err = requireFetchDestinationRef(command.DestinationRef); err != nil {
			return GitReadCommand{}, err
		}
	}
	return command, nil
}

func requireRemoteName(value string) (string, error) {
	value, err := requireCanonical("remote name", value)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(value, "-") || strings.ContainsAny(value, `/\:@{}~^?*[`) ||
		value == "." || value == ".." {
		return "", fmt.Errorf("%w: remote name is not a safe Git remote", ErrInvalid)
	}
	return value, nil
}

func requireFetchSourceRef(value string) (string, error) {
	value, err := requireGitRef("source ref", value)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(value, "refs/heads/") {
		return value, nil
	}
	segments := strings.Split(value, "/")
	if len(segments) != 4 ||
		segments[0] != "refs" ||
		segments[1] != "pull" ||
		segments[3] != "head" ||
		!isPositiveCanonicalDecimal(segments[2]) {
		return "", fmt.Errorf("%w: source ref is outside the admitted branch or pull-head namespaces", ErrInvalid)
	}
	return value, nil
}

func isPositiveCanonicalDecimal(value string) bool {
	if value == "" || value[0] < '1' || value[0] > '9' {
		return false
	}
	for index := 1; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func requireFetchDestinationRef(value string) (string, error) {
	value, err := requireGitRef("destination ref", value)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(value, "refs/loom/") {
		return "", fmt.Errorf("%w: destination ref is outside refs/loom", ErrInvalid)
	}
	return value, nil
}

func requireGitRef(label, value string) (string, error) {
	value, err := requireCanonical(label, value)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(value, "refs/") ||
		strings.HasSuffix(value, "/") ||
		strings.HasSuffix(value, ".") ||
		strings.Contains(value, "..") ||
		strings.Contains(value, "@{") ||
		strings.Contains(value, "//") ||
		strings.ContainsAny(value, ` \~^:?*[\`) {
		return "", fmt.Errorf("%w: %s is not a canonical Git ref", ErrInvalid, label)
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." ||
			strings.HasPrefix(segment, ".") || strings.HasSuffix(segment, ".lock") {
			return "", fmt.Errorf("%w: %s is not a canonical Git ref", ErrInvalid, label)
		}
	}
	return value, nil
}

func normalizeEnsureGrantCommand(command EnsureGrantCommand) (EnsureGrantCommand, error) {
	var err error
	if command.WorkspaceKey, err = requireCanonical("workspace", command.WorkspaceKey); err != nil {
		return EnsureGrantCommand{}, err
	}
	if command.GrantID, err = requireCanonical("grant id", command.GrantID); err != nil {
		return EnsureGrantCommand{}, err
	}
	if command.ConnectorID, err = requireCanonical("connector id", command.ConnectorID); err != nil {
		return EnsureGrantCommand{}, err
	}
	if command.BindingID, err = requireCanonical("binding id", command.BindingID); err != nil {
		return EnsureGrantCommand{}, err
	}
	if command.Action, err = normalizeConnectorAction(command.Action); err != nil {
		return EnsureGrantCommand{}, err
	}
	if command.ResourcePattern, err = requireCanonical("resource pattern", command.ResourcePattern); err != nil {
		return EnsureGrantCommand{}, err
	}
	return command, nil
}

func normalizeConnectorAction(value string) (string, error) {
	action, err := requireCanonical("connector action", value)
	if err != nil {
		return "", err
	}
	segments := strings.Split(action, ".")
	if len(segments) < 2 {
		return "", fmt.Errorf("%w: connector action needs at least provider and verb segments", ErrInvalid)
	}
	for _, segment := range segments {
		if segment == "" {
			return "", fmt.Errorf("%w: connector action has an empty segment", ErrInvalid)
		}
		for _, character := range segment {
			if (character < 'a' || character > 'z') &&
				(character < '0' || character > '9') &&
				character != '_' && character != '-' {
				return "", fmt.Errorf("%w: connector action contains an invalid character", ErrInvalid)
			}
		}
	}
	return action, nil
}

func validatePersistedGrant(value *ConnectorGrant, workspace, bindingID string) error {
	if value == nil || value.WorkspaceKey != workspace || value.BindingID != bindingID ||
		value.CreatedAt.IsZero() || value.RevokedAt != nil {
		return ErrInvalidPersistedState
	}
	command := EnsureGrantCommand{
		WorkspaceKey: value.WorkspaceKey, GrantID: value.GrantID,
		ConnectorID: value.ConnectorID, BindingID: value.BindingID,
		Action: value.Action, ResourcePattern: value.ResourcePattern,
	}
	if _, err := normalizeEnsureGrantCommand(command); err != nil {
		return fmt.Errorf("invalid persisted connector grant %q: %w", value.GrantID, ErrInvalidPersistedState)
	}
	return nil
}

func validateExactGrant(value *ConnectorGrant, command EnsureGrantCommand) error {
	if err := validatePersistedGrant(value, command.WorkspaceKey, command.BindingID); err != nil {
		return err
	}
	if !grantMatchesCommand(value, command) {
		return fmt.Errorf("created connector grant does not match request: %w", ErrInvalidPersistedState)
	}
	return nil
}

func grantMatchesCommand(value *ConnectorGrant, command EnsureGrantCommand) bool {
	return value != nil &&
		value.WorkspaceKey == command.WorkspaceKey &&
		value.GrantID == command.GrantID &&
		value.ConnectorID == command.ConnectorID &&
		value.BindingID == command.BindingID &&
		value.Action == command.Action &&
		value.ResourcePattern == command.ResourcePattern &&
		value.RevokedAt == nil
}

func requireCanonical(label, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed != value {
		return "", fmt.Errorf("%w: %s is required and canonical", ErrInvalid, label)
	}
	for _, r := range trimmed {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: %s contains a control character", ErrInvalid, label)
		}
	}
	return trimmed, nil
}

func requireAbsolutePath(label, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !filepath.IsAbs(value) {
		return "", fmt.Errorf("%w: %s must be absolute", ErrInvalid, label)
	}
	clean := filepath.Clean(value)
	if clean != value {
		return "", fmt.Errorf("%w: %s must be canonical", ErrInvalid, label)
	}
	return clean, nil
}

func pathContains(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative == "." ||
		(relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
