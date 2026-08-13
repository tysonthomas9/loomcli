package sourcecontrol

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// Service owns checkout path policy, idempotent materialization semantics, and
// validation of the credential-free Connectors receipt.
type Service struct {
	repositories RepositoryResolver
	broker       GitReadBroker
	inspector    CheckoutInspector
	admission    *authority.Admission
	coordinator  *materializationCoordinator
	fetches      *refFetchCoordinator
}

func New(
	repositories RepositoryResolver,
	broker GitReadBroker,
	inspector CheckoutInspector,
	admission *authority.Admission,
) (*Service, error) {
	if repositories == nil || broker == nil || inspector == nil || admission == nil {
		return nil, fmt.Errorf("compose Source Control: repository resolver, broker, inspector, and admission are required: %w", ErrUnavailable)
	}
	return &Service{
		repositories: repositories,
		broker:       broker,
		inspector:    inspector,
		admission:    admission,
		coordinator:  newMaterializationCoordinator(),
		fetches:      newRefFetchCoordinator(),
	}, nil
}

//nolint:cyclop,funlen // Materialization keeps validation, authority, containment, publication, and rollback ordering together.
func (s *Service) MaterializeWorkspace(
	ctx context.Context,
	auth authority.SystemAuthority,
	command MaterializeCommand,
) (*Materialization, error) {
	command, err := normalizeMaterializeCommand(command)
	if err != nil {
		return nil, err
	}
	if s == nil || s.admission == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.admission.RequireSystem(ActionMaterializeWorkspace, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if s.repositories == nil || s.inspector == nil || s.broker == nil {
		return nil, ErrUnavailable
	}
	repository, err := s.repositories.ResolveRepositoryCheckout(
		ctx,
		command.WorkspaceKey,
		command.MaterializationID,
		command.RepositoryRef,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve repository %q checkout: %w", command.RepositoryRef, err)
	}
	repository, targetPath, err := normalizeRepositoryCheckout(command, repository)
	if err != nil {
		return nil, err
	}
	canonicalTarget, err := s.canonicalTarget(
		ctx,
		repository.WorkspacePath,
		targetPath,
	)
	if err != nil {
		return nil, err
	}
	releaseOperation, err := s.coordinator.acquireOperation(ctx, materializationCoordinates{
		workspaceKey:      command.WorkspaceKey,
		materializationID: command.MaterializationID,
		repositoryRef:     command.RepositoryRef,
		remoteURL:         repository.RemoteURL,
		remoteName:        repository.RemoteName,
		workspacePath:     repository.WorkspacePath,
		targetPath:        targetPath,
		canonicalTarget:   canonicalTarget,
	})
	if err != nil {
		return nil, err
	}
	defer releaseOperation()
	releaseTarget, err := s.coordinator.acquireTarget(ctx, canonicalTarget)
	if err != nil {
		return nil, err
	}
	defer releaseTarget()
	if err := s.revalidateTarget(
		ctx,
		repository.WorkspacePath,
		targetPath,
		canonicalTarget,
	); err != nil {
		return nil, err
	}

	match, err := s.inspector.MatchRemote(ctx, targetPath, repository.RemoteName, repository.RemoteURL)
	if err != nil {
		return nil, fmt.Errorf("inspect repository %q checkout: %w", command.RepositoryRef, err)
	}
	switch match {
	case CheckoutMatched:
		if err := s.revalidateRepositoryCheckout(
			ctx,
			command,
			repository,
			targetPath,
		); err != nil {
			return nil, err
		}
		if err := s.recordCheckout(
			ctx,
			command.MaterializationID,
			repository,
			targetPath,
		); err != nil {
			return nil, err
		}
		return materializationFor(command, targetPath, true), nil
	case CheckoutConflict:
		return nil, fmt.Errorf(
			"%w: repository %q target does not match the requested remote",
			ErrCheckoutConflict,
			command.RepositoryRef,
		)
	case CheckoutMissing:
		// Continue to the bounded broker operation.
	default:
		return nil, fmt.Errorf("%w: inspector returned %q", ErrInvalidMaterialization, match)
	}

	request := GitCloneRequest{
		WorkspaceKey:  command.WorkspaceKey,
		OperationID:   command.MaterializationID,
		RepositoryRef: command.RepositoryRef,
		RemoteURL:     repository.RemoteURL,
		RemoteName:    repository.RemoteName,
		WorkspacePath: repository.WorkspacePath,
		TargetPath:    targetPath,
	}
	receipt, err := s.broker.Clone(ctx, request)
	if err != nil {
		return nil, fmt.Errorf(
			"materialize repository %q operation %q: %w",
			command.RepositoryRef,
			command.MaterializationID,
			err,
		)
	}
	if receipt.WorkspaceKey != request.WorkspaceKey ||
		receipt.OperationID != request.OperationID ||
		receipt.RepositoryRef != request.RepositoryRef ||
		receipt.TargetPath != request.TargetPath {
		return nil, fmt.Errorf("%w: broker returned different operation coordinates", ErrInvalidBrokerReceipt)
	}
	if err := s.revalidateTarget(
		ctx,
		repository.WorkspacePath,
		targetPath,
		canonicalTarget,
	); err != nil {
		return nil, err
	}

	match, err = s.inspector.MatchRemote(ctx, targetPath, repository.RemoteName, repository.RemoteURL)
	if err != nil {
		return nil, fmt.Errorf("verify repository %q checkout: %w", command.RepositoryRef, err)
	}
	if match != CheckoutMatched {
		return nil, fmt.Errorf(
			"%w: repository %q operation %q produced %q",
			ErrInvalidMaterialization,
			command.RepositoryRef,
			command.MaterializationID,
			match,
		)
	}
	if err := s.revalidateRepositoryCheckout(
		ctx,
		command,
		repository,
		targetPath,
	); err != nil {
		return nil, err
	}
	if err := s.recordCheckout(
		ctx,
		command.MaterializationID,
		repository,
		targetPath,
	); err != nil {
		return nil, err
	}
	return materializationFor(command, targetPath, false), nil
}

func (s *Service) recordCheckout(
	ctx context.Context,
	materializationID string,
	repository RepositoryCheckout,
	targetPath string,
) error {
	_, repositoryAdmission, err :=
		ParseRepositoryAdmissionMaterializationID(materializationID)
	if err != nil {
		return fmt.Errorf("classify checkout publication: %w", err)
	}
	if repositoryAdmission {
		// A repository admission is an owner-fenced all-or-nothing batch.
		// Source Control may leave a verified checkout as durable retry
		// progress, but Workspace alone publishes the complete local repo map
		// after every member has completed and the exact Fleet generation is
		// still owned. Recording one checkout here would expose partial state
		// and create an uncloseable fence-change window before the write.
		return nil
	}
	if err := s.repositories.RecordRepositoryCheckout(ctx, repository, targetPath); err != nil {
		return fmt.Errorf("record repository %q checkout: %w", repository.RepositoryRef, err)
	}
	return nil
}

// FetchRepositoryRef executes and verifies one bounded, credential-brokered
// fetch against an already materialized exact checkout.
//
//nolint:cyclop,funlen // Fetch keeps authority, remote normalization, containment, and exact-ref verification fail-closed.
func (s *Service) FetchRepositoryRef(
	ctx context.Context,
	auth authority.SystemAuthority,
	command FetchRefCommand,
) (*FetchedRef, error) {
	command, err := normalizeFetchRefCommand(command)
	if err != nil {
		return nil, err
	}
	if s == nil || s.admission == nil {
		return nil, authority.ErrAdmissionDenied
	}
	if err := s.admission.RequireSystem(ActionFetchRepositoryRef, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if s.repositories == nil || s.inspector == nil || s.broker == nil || s.fetches == nil {
		return nil, ErrUnavailable
	}
	repository, err := s.repositories.ResolveRepositoryCheckout(
		ctx,
		command.WorkspaceKey,
		command.OperationID,
		command.RepositoryRef,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve repository %q checkout: %w", command.RepositoryRef, err)
	}
	repository, targetPath, err := normalizeRepositoryCheckout(
		MaterializeCommand{
			WorkspaceKey: command.WorkspaceKey, MaterializationID: command.OperationID,
			RepositoryRef: command.RepositoryRef,
		},
		repository,
	)
	if err != nil {
		return nil, err
	}
	canonicalTarget, err := s.canonicalTarget(ctx, repository.WorkspacePath, targetPath)
	if err != nil {
		return nil, err
	}
	releaseOperation, err := s.fetches.acquireOperation(ctx, refFetchCoordinates{
		workspaceKey: command.WorkspaceKey, operationID: command.OperationID,
		repositoryRef: command.RepositoryRef, remoteURL: repository.RemoteURL,
		workspacePath: repository.WorkspacePath, targetPath: targetPath,
		remoteName: repository.RemoteName, sourceRef: command.SourceRef,
		destinationRef: command.DestinationRef, expectedCommit: command.ExpectedCommit,
		canonicalTarget: canonicalTarget,
	})
	if err != nil {
		return nil, err
	}
	defer releaseOperation()
	releaseTarget, err := s.fetches.acquireTarget(ctx, canonicalTarget)
	if err != nil {
		return nil, err
	}
	defer releaseTarget()
	if err := s.revalidateTarget(ctx, repository.WorkspacePath, targetPath, canonicalTarget); err != nil {
		return nil, err
	}
	match, err := s.inspector.MatchRemote(ctx, targetPath, repository.RemoteName, repository.RemoteURL)
	if err != nil {
		return nil, fmt.Errorf("inspect repository %q checkout: %w", command.RepositoryRef, err)
	}
	if match != CheckoutMatched {
		if match == CheckoutConflict {
			return nil, fmt.Errorf("%w: repository %q target does not match the requested remote", ErrCheckoutConflict, command.RepositoryRef)
		}
		return nil, fmt.Errorf("%w: repository %q must be materialized before fetch", ErrInvalidMaterialization, command.RepositoryRef)
	}
	request := GitFetchRequest{
		WorkspaceKey: command.WorkspaceKey, OperationID: command.OperationID,
		RepositoryRef: command.RepositoryRef, RemoteURL: repository.RemoteURL,
		WorkspacePath: repository.WorkspacePath, TargetPath: targetPath,
		RemoteName: repository.RemoteName, SourceRef: command.SourceRef,
		DestinationRef: command.DestinationRef,
	}
	receipt, err := s.broker.FetchRef(ctx, request)
	if err != nil {
		return nil, fmt.Errorf(
			"fetch repository %q ref operation %q: %w",
			command.RepositoryRef,
			command.OperationID,
			err,
		)
	}
	if receipt != (GitFetchReceipt{
		WorkspaceKey: request.WorkspaceKey, OperationID: request.OperationID,
		RepositoryRef: request.RepositoryRef, TargetPath: request.TargetPath,
		RemoteName: request.RemoteName, SourceRef: request.SourceRef,
		DestinationRef: request.DestinationRef,
	}) {
		return nil, fmt.Errorf("%w: broker returned different fetch coordinates", ErrInvalidBrokerReceipt)
	}
	if err := s.revalidateTarget(ctx, repository.WorkspacePath, targetPath, canonicalTarget); err != nil {
		return nil, err
	}
	match, err = s.inspector.MatchRemote(ctx, targetPath, repository.RemoteName, repository.RemoteURL)
	if err != nil {
		return nil, fmt.Errorf("verify repository %q checkout: %w", command.RepositoryRef, err)
	}
	if match != CheckoutMatched {
		return nil, fmt.Errorf("%w: repository %q checkout changed during fetch", ErrInvalidMaterialization, command.RepositoryRef)
	}
	commit, err := s.inspector.ResolveCommit(ctx, targetPath, command.DestinationRef)
	if err != nil {
		return nil, fmt.Errorf("verify fetched repository %q ref: %w", command.RepositoryRef, err)
	}
	commit, err = normalizeCommitSHA(commit)
	if err != nil {
		return nil, fmt.Errorf("%w: broker produced an invalid commit", ErrInvalidMaterialization)
	}
	if command.ExpectedCommit != "" && !strings.EqualFold(commit, command.ExpectedCommit) {
		return nil, &RefChangedError{ExpectedCommit: command.ExpectedCommit, FetchedCommit: commit}
	}
	return &FetchedRef{
		WorkspaceKey: command.WorkspaceKey, OperationID: command.OperationID,
		RepositoryRef: command.RepositoryRef, CheckoutPath: targetPath,
		RemoteName: repository.RemoteName, SourceRef: command.SourceRef,
		DestinationRef: command.DestinationRef, CommitSHA: commit,
	}, nil
}

func (s *Service) canonicalTarget(
	ctx context.Context,
	workspacePath string,
	targetPath string,
) (string, error) {
	canonicalTarget, err := s.inspector.CanonicalTarget(ctx, workspacePath, targetPath)
	if err != nil {
		return "", fmt.Errorf("validate checkout containment: %w", err)
	}
	if canonicalTarget == "" ||
		!filepath.IsAbs(canonicalTarget) ||
		filepath.Clean(canonicalTarget) != canonicalTarget {
		return "", fmt.Errorf("%w: inspector returned an invalid canonical target", ErrInvalidMaterialization)
	}
	return canonicalTarget, nil
}

func (s *Service) revalidateTarget(
	ctx context.Context,
	workspacePath string,
	targetPath string,
	canonicalTarget string,
) error {
	revalidated, err := s.canonicalTarget(ctx, workspacePath, targetPath)
	if err != nil {
		return err
	}
	if revalidated != canonicalTarget {
		return fmt.Errorf("%w: checkout target identity changed", ErrInvalidMaterialization)
	}
	return nil
}

func (s *Service) revalidateRepositoryCheckout(
	ctx context.Context,
	command MaterializeCommand,
	expected RepositoryCheckout,
	expectedTarget string,
) error {
	current, err := s.repositories.ResolveRepositoryCheckout(
		ctx,
		command.WorkspaceKey,
		command.MaterializationID,
		command.RepositoryRef,
	)
	if err != nil {
		return fmt.Errorf(
			"revalidate repository %q checkout ownership: %w",
			command.RepositoryRef,
			err,
		)
	}
	current, currentTarget, err := normalizeRepositoryCheckout(command, current)
	if err != nil {
		return err
	}
	if current != expected || currentTarget != expectedTarget {
		return fmt.Errorf(
			"%w: repository checkout ownership changed during materialization",
			ErrInvalidMaterialization,
		)
	}
	return nil
}

func materializationFor(command MaterializeCommand, targetPath string, reused bool) *Materialization {
	return &Materialization{
		WorkspaceKey:      command.WorkspaceKey,
		MaterializationID: command.MaterializationID,
		RepositoryRef:     command.RepositoryRef,
		CheckoutPath:      targetPath,
		Reused:            reused,
	}
}

func normalizeMaterializeCommand(command MaterializeCommand) (MaterializeCommand, error) {
	var err error
	if command.WorkspaceKey, err = requireCanonical("workspace", command.WorkspaceKey); err != nil {
		return MaterializeCommand{}, err
	}
	if command.MaterializationID, err = requireCanonical("materialization id", command.MaterializationID); err != nil {
		return MaterializeCommand{}, err
	}
	if command.RepositoryRef, err = requireCanonical("repository ref", command.RepositoryRef); err != nil {
		return MaterializeCommand{}, err
	}
	return command, nil
}

func normalizeRepositoryCheckout(
	command MaterializeCommand,
	repository RepositoryCheckout,
) (RepositoryCheckout, string, error) {
	var err error
	if repository.WorkspaceKey, err = requireCanonical("resolved workspace", repository.WorkspaceKey); err != nil {
		return RepositoryCheckout{}, "", err
	}
	if repository.RepositoryRef, err = requireCanonical("resolved repository ref", repository.RepositoryRef); err != nil {
		return RepositoryCheckout{}, "", err
	}
	if repository.WorkspaceKey != command.WorkspaceKey ||
		repository.RepositoryRef != command.RepositoryRef {
		return RepositoryCheckout{}, "", fmt.Errorf("%w: repository resolver returned different ownership coordinates", ErrInvalidMaterialization)
	}
	repository.RemoteURL, err = normalizeTokenFreeRemote(repository.RemoteURL)
	if err != nil {
		return RepositoryCheckout{}, "", err
	}
	repository.RemoteName, err = requireRemoteName(repository.RemoteName)
	if err != nil {
		return RepositoryCheckout{}, "", err
	}
	repository.WorkspacePath, err = requireAbsolutePath("resolved workspace path", repository.WorkspacePath)
	if err != nil {
		return RepositoryCheckout{}, "", err
	}
	repository.CheckoutName, err = requireCheckoutName(repository.CheckoutName)
	if err != nil {
		return RepositoryCheckout{}, "", err
	}
	targetPath := filepath.Join(repository.WorkspacePath, repository.CheckoutName)
	if targetPath == repository.WorkspacePath || !pathContains(repository.WorkspacePath, targetPath) {
		return RepositoryCheckout{}, "", fmt.Errorf("%w: checkout target escapes the workspace", ErrInvalid)
	}
	return repository, targetPath, nil
}

func normalizeFetchRefCommand(command FetchRefCommand) (FetchRefCommand, error) {
	var err error
	if command.WorkspaceKey, err = requireCanonical("workspace", command.WorkspaceKey); err != nil {
		return FetchRefCommand{}, err
	}
	if command.OperationID, err = requireCanonical("operation id", command.OperationID); err != nil {
		return FetchRefCommand{}, err
	}
	if _, admissionMaterialization, parseErr :=
		ParseRepositoryAdmissionMaterializationID(command.OperationID); parseErr != nil {
		return FetchRefCommand{}, parseErr
	} else if admissionMaterialization {
		return FetchRefCommand{}, fmt.Errorf(
			"%w: repository admission materialization IDs cannot authorize ref fetch",
			ErrInvalid,
		)
	}
	if command.RepositoryRef, err = requireCanonical("repository ref", command.RepositoryRef); err != nil {
		return FetchRefCommand{}, err
	}
	if command.SourceRef, err = requireFetchSourceRef(command.SourceRef); err != nil {
		return FetchRefCommand{}, err
	}
	if command.DestinationRef, err = requireFetchDestinationRef(command.DestinationRef); err != nil {
		return FetchRefCommand{}, err
	}
	if command.ExpectedCommit != "" {
		if command.ExpectedCommit, err = normalizeCommitSHA(command.ExpectedCommit); err != nil {
			return FetchRefCommand{}, err
		}
	}
	return command, nil
}

func requireRemoteName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "origin"
	}
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
		strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") ||
		strings.Contains(value, "..") || strings.Contains(value, "@{") ||
		strings.Contains(value, "//") || strings.ContainsAny(value, ` \~^:?*[\`) {
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

func normalizeCommitSHA(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) != 40 && len(value) != 64 {
		return "", fmt.Errorf("%w: commit must be a full SHA", ErrInvalid)
	}
	for _, character := range value {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') &&
			(character < 'A' || character > 'F') {
			return "", fmt.Errorf("%w: commit must be hexadecimal", ErrInvalid)
		}
	}
	return strings.ToLower(value), nil
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

func requireCheckoutName(value string) (string, error) {
	value, err := requireCanonical("checkout name", value)
	if err != nil {
		return "", err
	}
	if value == "." || value == ".." || filepath.IsAbs(value) ||
		strings.ContainsAny(value, `/\`) || filepath.Base(value) != value {
		return "", fmt.Errorf("%w: checkout name must be one safe path segment", ErrInvalid)
	}
	return value, nil
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
