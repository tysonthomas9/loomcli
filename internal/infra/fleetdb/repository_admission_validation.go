package fleetdb

import (
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/platform/repositoryremote"
)

type repositoryAdmissionBeginRequestWire struct {
	OperationID       string                        `json:"operation_id"`
	OwnerID           string                        `json:"owner_id"`
	OwnerLeaseSeconds int                           `json:"owner_lease_seconds,omitempty"`
	Repositories      []RepositoryAdmissionRepoSpec `json:"repositories"`
}

func repositoryAdmissionBeginRequest(
	input RepositoryAdmissionBeginInput,
) (repositoryAdmissionBeginRequestWire, error) {
	if err := validateRepositoryAdmissionOperationID(input.OperationID); err != nil {
		return repositoryAdmissionBeginRequestWire{}, err
	}
	input.OwnerID = strings.TrimSpace(input.OwnerID)
	if input.OwnerID == "" || len(input.OwnerID) > 200 {
		return repositoryAdmissionBeginRequestWire{}, fmt.Errorf(
			"repository admission owner: %w",
			ErrRepositoryAdmissionInvalid,
		)
	}
	if err := validateRepositoryAdmissionRepoSpecs(input.Repositories); err != nil {
		return repositoryAdmissionBeginRequestWire{}, err
	}
	leaseSeconds, err := repositoryAdmissionOptionalLeaseSeconds(input.OwnerLease)
	if err != nil {
		return repositoryAdmissionBeginRequestWire{}, err
	}
	return repositoryAdmissionBeginRequestWire{
		OperationID: input.OperationID, OwnerID: input.OwnerID,
		OwnerLeaseSeconds: leaseSeconds,
		Repositories: append(
			[]RepositoryAdmissionRepoSpec(nil),
			input.Repositories...,
		),
	}, nil
}

type repositoryAdmissionGuardWire struct {
	OwnerID           string `json:"owner_id"`
	OwnerGenerationID string `json:"owner_generation_id"`
	SpecFingerprint   string `json:"spec_fingerprint"`
	ExpectedVersion   int64  `json:"expected_version"`
}

func repositoryAdmissionGuardRequest(
	guard RepositoryAdmissionGuard,
) repositoryAdmissionGuardWire {
	return repositoryAdmissionGuardWire{
		OwnerID: guard.OwnerID, OwnerGenerationID: guard.OwnerGenerationID,
		SpecFingerprint: guard.SpecFingerprint, ExpectedVersion: guard.ExpectedVersion,
	}
}

func repositoryAdmissionBasePath(workspace string) string {
	return "/api/v1/" + pathEscape(workspace) + "/repository-admissions"
}

func validateRepositoryAdmissionWorkspaceInput(
	input RepositoryAdmissionWorkspaceInput,
) error {
	if err := validateRepositoryAdmissionWorkspaceKey(input.Key); err != nil {
		return err
	}
	if input.Name == "" ||
		input.Name != strings.TrimSpace(input.Name) ||
		len(input.Name) > 200 ||
		input.State == "" {
		return fmt.Errorf("repository admission workspace: %w", ErrRepositoryAdmissionInvalid)
	}
	switch input.DesignFormat {
	case "", "markdown", "html":
	default:
		return fmt.Errorf("repository admission workspace design format: %w", ErrRepositoryAdmissionInvalid)
	}
	return nil
}

func validateRepositoryAdmissionWorkspaceKey(workspace string) error {
	if !repositoryAdmissionWorkspaceKeyPattern.MatchString(workspace) {
		return fmt.Errorf("repository admission workspace: %w", ErrRepositoryAdmissionInvalid)
	}
	return nil
}

func validateRepositoryAdmissionOperationID(operationID string) error {
	if operationID == "" ||
		operationID != strings.TrimSpace(operationID) ||
		len(operationID) > 200 ||
		strings.IndexFunc(operationID, unicode.IsControl) >= 0 {
		return fmt.Errorf("repository admission operation ID: %w", ErrRepositoryAdmissionInvalid)
	}
	return nil
}

func validateRepositoryAdmissionCoordinates(workspace, admissionID string) error {
	if err := validateRepositoryAdmissionWorkspaceKey(workspace); err != nil {
		return err
	}
	if !validRepositoryAdmissionOpaqueID(admissionID) {
		return fmt.Errorf("repository admission ID: %w", ErrRepositoryAdmissionInvalid)
	}
	return nil
}

func validateRepositoryAdmissionRepoSpecs(specs []RepositoryAdmissionRepoSpec) error {
	if len(specs) < 1 || len(specs) > 64 {
		return fmt.Errorf("repository admission repository count: %w", ErrRepositoryAdmissionInvalid)
	}
	seen := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		if !repositoryAdmissionRepoNamePattern.MatchString(spec.Name) ||
			(spec.SourceRepoID != "" && !repositoryAdmissionRepoNamePattern.MatchString(spec.SourceRepoID)) ||
			(spec.Remote != "" && (spec.Remote != strings.TrimSpace(spec.Remote) || len(spec.Remote) > 100)) ||
			(spec.DefaultBranch != "" && !validRepositoryAdmissionBranch(spec.DefaultBranch)) {
			return fmt.Errorf("repository admission repository spec: %w", ErrRepositoryAdmissionInvalid)
		}
		if _, duplicate := seen[spec.Name]; duplicate {
			return fmt.Errorf("repository admission duplicate repository: %w", ErrRepositoryAdmissionInvalid)
		}
		seen[spec.Name] = struct{}{}
		if _, err := repositoryremote.Normalize(spec.RemoteURL); err != nil {
			return fmt.Errorf("repository admission repository remote: %w", errors.Join(ErrRepositoryAdmissionInvalid, err))
		}
		groupSeen := make(map[string]struct{}, len(spec.Groups))
		for _, group := range spec.Groups {
			if !repositoryAdmissionGroupPattern.MatchString(group) {
				return fmt.Errorf("repository admission repository group: %w", ErrRepositoryAdmissionInvalid)
			}
			if _, duplicate := groupSeen[group]; duplicate {
				return fmt.Errorf("repository admission duplicate repository group: %w", ErrRepositoryAdmissionInvalid)
			}
			groupSeen[group] = struct{}{}
		}
	}
	return nil
}

func validateRepositoryAdmissionBeginIntent(
	record *RepositoryAdmissionRecord,
	workspace string,
	input RepositoryAdmissionBeginInput,
) error {
	if record == nil ||
		record.WorkspaceKey != workspace ||
		record.OperationID != input.OperationID ||
		record.OwnerID != strings.TrimSpace(input.OwnerID) ||
		!sameCanonicalRepositoryAdmissionRepoSpecs(
			record.Spec.Repositories,
			input.Repositories,
		) {
		return fmt.Errorf(
			"repository admission begin response changed immutable intent: %w",
			ErrRepositoryAdmissionInvalid,
		)
	}
	return nil
}

func validateWorkspaceRepositoryAdmissionBeginResult(
	result *WorkspaceRepositoryAdmissionBeginResult,
	expected RepositoryAdmissionWorkspaceInput,
) error {
	if result == nil ||
		result.Workspace == nil ||
		result.Admission == nil ||
		result.Workspace.Key != expected.Key ||
		result.Workspace.Name != expected.Name ||
		result.Workspace.Description != expected.Description ||
		string(result.Workspace.State) != expected.State ||
		result.Workspace.ErrorMessage != expected.ErrorMessage ||
		result.Workspace.DefaultBranch != expected.DefaultBranch ||
		result.Workspace.DesignFormat != expected.DesignFormat ||
		result.Workspace.CreatedAt.IsZero() ||
		result.Workspace.UpdatedAt.IsZero() ||
		result.Workspace.UpdatedAt.Before(result.Workspace.CreatedAt) ||
		result.Admission.WorkspaceKey != expected.Key ||
		result.WorkspaceEventID == "" ||
		result.WorkspaceEventID != strings.TrimSpace(result.WorkspaceEventID) ||
		strings.IndexFunc(result.WorkspaceEventID, unicode.IsControl) >= 0 {
		return fmt.Errorf(
			"create workspace repository admission response changed workspace-create identity: %w",
			ErrRepositoryAdmissionInvalid,
		)
	}
	return nil
}

func sameCanonicalRepositoryAdmissionRepoSpecs(
	left,
	right []RepositoryAdmissionRepoSpec,
) bool {
	left = canonicalRepositoryAdmissionRepoSpecs(left)
	right = canonicalRepositoryAdmissionRepoSpecs(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Name != right[index].Name ||
			left[index].RemoteURL != right[index].RemoteURL ||
			left[index].Remote != right[index].Remote ||
			left[index].DefaultBranch != right[index].DefaultBranch ||
			!slices.Equal(left[index].Groups, right[index].Groups) ||
			left[index].SourceRepoID != right[index].SourceRepoID {
			return false
		}
	}
	return true
}

func validateRepositoryAdmissionGuard(guard RepositoryAdmissionGuard) error {
	if err := validateRepositoryAdmissionCoordinates(guard.WorkspaceKey, guard.AdmissionID); err != nil {
		return err
	}
	if strings.TrimSpace(guard.OwnerID) == "" ||
		len(guard.OwnerID) > 200 ||
		!validRepositoryAdmissionOpaqueID(guard.OwnerGenerationID) ||
		guard.ExpectedVersion < 1 {
		return fmt.Errorf("repository admission guard: %w", ErrRepositoryAdmissionInvalid)
	}
	return validateRepositoryAdmissionFingerprint(guard.SpecFingerprint)
}

func validateRepositoryAdmissionResolvedBranches(
	branches []RepositoryAdmissionResolvedBranch,
) error {
	if len(branches) < 1 || len(branches) > 64 {
		return fmt.Errorf("repository admission resolved branches: %w", ErrRepositoryAdmissionInvalid)
	}
	seen := make(map[string]struct{}, len(branches))
	for _, branch := range branches {
		if !repositoryAdmissionRepoNamePattern.MatchString(branch.Name) ||
			!validRepositoryAdmissionBranch(branch.DefaultBranch) {
			return fmt.Errorf("repository admission resolved branch: %w", ErrRepositoryAdmissionInvalid)
		}
		if _, duplicate := seen[branch.Name]; duplicate {
			return fmt.Errorf("repository admission duplicate resolved branch: %w", ErrRepositoryAdmissionInvalid)
		}
		seen[branch.Name] = struct{}{}
	}
	return nil
}

func validRepositoryAdmissionBranch(branch string) bool {
	if branch == "" ||
		branch != strings.TrimSpace(branch) ||
		len(branch) > 200 ||
		strings.IndexFunc(branch, unicode.IsControl) >= 0 ||
		strings.ContainsAny(branch, " ~^:?*[\\") ||
		strings.Contains(branch, "..") ||
		strings.Contains(branch, "@{") ||
		strings.Contains(branch, "//") ||
		branch == "@" ||
		strings.HasPrefix(branch, "-") ||
		strings.HasPrefix(branch, "/") ||
		strings.HasSuffix(branch, "/") ||
		strings.HasSuffix(branch, ".") {
		return false
	}
	for _, component := range strings.Split(branch, "/") {
		if strings.HasPrefix(component, ".") ||
			strings.HasSuffix(component, ".lock") {
			return false
		}
	}
	return true
}

func validateRepositoryAdmissionFingerprint(fingerprint string) error {
	const prefix = "sha256:"
	if !strings.HasPrefix(fingerprint, prefix) ||
		len(fingerprint) != len(prefix)+64 {
		return fmt.Errorf("repository admission fingerprint: %w", ErrRepositoryAdmissionInvalid)
	}
	encoded := strings.TrimPrefix(fingerprint, prefix)
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) != 32 || strings.ToLower(encoded) != encoded {
		return fmt.Errorf("repository admission fingerprint: %w", ErrRepositoryAdmissionInvalid)
	}
	return nil
}

func validRepositoryAdmissionOpaqueID(value string) bool {
	if len(value) != 32 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16
}

func validRepositoryAdmissionErrorClass(value string) bool {
	if len(value) < 1 || len(value) > 100 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value[1:] {
		if (char < 'a' || char > 'z') &&
			(char < '0' || char > '9') &&
			char != '_' {
			return false
		}
	}
	return true
}

func repositoryAdmissionOptionalLeaseSeconds(lease time.Duration) (int, error) {
	if lease == 0 {
		return 0, nil
	}
	return repositoryAdmissionLeaseSeconds(lease)
}

func repositoryAdmissionRequiredLeaseSeconds(lease time.Duration) (int, error) {
	if lease == 0 {
		lease = defaultRepositoryAdmissionOwnerLease
	}
	return repositoryAdmissionLeaseSeconds(lease)
}

func repositoryAdmissionLeaseSeconds(lease time.Duration) (int, error) {
	if lease < minRepositoryAdmissionOwnerLease ||
		lease > maxRepositoryAdmissionOwnerLease ||
		lease%time.Second != 0 {
		return 0, fmt.Errorf("repository admission owner lease: %w", ErrRepositoryAdmissionInvalid)
	}
	return int(lease / time.Second), nil
}

//nolint:cyclop,funlen // Keep the exhaustive persisted-record invariant check centralized so malformed admission state fails closed.
func validateRepositoryAdmissionRecord(
	record *RepositoryAdmissionRecord,
	allowRedactedOwner bool,
) error {
	if record == nil {
		return fmt.Errorf("repository admission response: %w", ErrRepositoryAdmissionInvalid)
	}
	if err := validateRepositoryAdmissionCoordinates(record.WorkspaceKey, record.AdmissionID); err != nil {
		return err
	}
	if err := validateRepositoryAdmissionOperationID(record.OperationID); err != nil {
		return err
	}
	if (!allowRedactedOwner &&
		(record.OwnerID == "" ||
			record.OwnerID != strings.TrimSpace(record.OwnerID) ||
			len(record.OwnerID) > 200 ||
			!validRepositoryAdmissionOpaqueID(record.OwnerGenerationID))) ||
		(allowRedactedOwner &&
			(record.OwnerID != "" || record.OwnerGenerationID != "")) ||
		record.OwnerLeaseExpiresAt.IsZero() ||
		record.Version < 1 ||
		record.CreatedAt.IsZero() ||
		record.UpdatedAt.IsZero() ||
		record.UpdatedAt.Before(record.CreatedAt) ||
		record.OwnerLeaseExpiresAt.Before(record.CreatedAt) ||
		record.OwnerLeaseExpiresAt.After(
			record.UpdatedAt.Add(maxRepositoryAdmissionOwnerLease),
		) {
		return fmt.Errorf("repository admission response owner/version: %w", ErrRepositoryAdmissionInvalid)
	}
	if err := validateRepositoryAdmissionFingerprint(record.SpecFingerprint); err != nil {
		return err
	}
	if record.Spec.WorkspaceKey != record.WorkspaceKey ||
		record.Spec.OperationID != record.OperationID {
		return fmt.Errorf("repository admission response identity: %w", ErrRepositoryAdmissionInvalid)
	}
	if err := validateRepositoryAdmissionRepoSpecs(record.Spec.Repositories); err != nil {
		return err
	}
	switch record.State {
	case "pending", "retryable_failed":
		if record.TerminalAt != nil || record.Receipt != nil {
			return fmt.Errorf("repository admission nonterminal response: %w", ErrRepositoryAdmissionInvalid)
		}
	case "permanent_failed", "aborted":
		if record.TerminalAt == nil || record.Receipt != nil {
			return fmt.Errorf("repository admission terminal response: %w", ErrRepositoryAdmissionInvalid)
		}
	case "committed":
		if record.TerminalAt == nil || record.Receipt == nil {
			return fmt.Errorf("repository admission committed response: %w", ErrRepositoryAdmissionInvalid)
		}
	default:
		return fmt.Errorf("repository admission response state: %w", ErrRepositoryAdmissionInvalid)
	}
	if record.TerminalAt != nil &&
		(record.TerminalAt.Before(record.CreatedAt) ||
			record.TerminalAt.After(record.UpdatedAt)) {
		return fmt.Errorf(
			"repository admission response terminal timestamp: %w",
			ErrRepositoryAdmissionInvalid,
		)
	}
	return validateRepositoryAdmissionReceipt(record)
}

//nolint:cyclop // Receipt fields form one signed admission postcondition and must be validated together before accepting the record.
func validateRepositoryAdmissionReceipt(record *RepositoryAdmissionRecord) error {
	if record.Receipt == nil {
		return nil
	}
	receipt := record.Receipt
	if receipt.AdmissionID != record.AdmissionID ||
		receipt.SpecFingerprint != record.SpecFingerprint ||
		receipt.CommittedAt.IsZero() ||
		record.TerminalAt == nil ||
		!receipt.CommittedAt.Equal(*record.TerminalAt) ||
		len(receipt.Repositories) != len(record.Spec.Repositories) {
		return fmt.Errorf("repository admission receipt identity: %w", ErrRepositoryAdmissionInvalid)
	}
	if receipt.WorkspaceFinalization != nil {
		if err := validateRepositoryAdmissionWorkspaceFinalization(
			receipt.WorkspaceFinalization,
		); err != nil {
			return err
		}
	}
	for index, spec := range record.Spec.Repositories {
		item := receipt.Repositories[index]
		repository := item.Repository
		if item.EventID == "" ||
			repository.WorkspaceKey != record.WorkspaceKey ||
			repository.Name != spec.Name ||
			repository.RemoteURL != spec.RemoteURL ||
			repository.Remote != spec.Remote ||
			!slices.Equal(repository.Groups, spec.Groups) ||
			repository.SourceRepoID != spec.SourceRepoID ||
			repository.CreatedAt.IsZero() ||
			repository.UpdatedAt.IsZero() ||
			!validRepositoryAdmissionBranch(repository.DefaultBranch) ||
			(spec.DefaultBranch != "" &&
				repository.DefaultBranch != spec.DefaultBranch) {
			return fmt.Errorf("repository admission repository receipt: %w", ErrRepositoryAdmissionInvalid)
		}
	}
	return nil
}

func validateRepositoryAdmissionWorkspaceFinalization(
	finalization *RepositoryAdmissionWorkspaceFinalization,
) error {
	if finalization == nil {
		return nil
	}
	if finalization.State != "ready" ||
		!validRepositoryAdmissionBranch(finalization.DefaultBranch) {
		return fmt.Errorf(
			"repository admission workspace finalization: %w",
			ErrRepositoryAdmissionInvalid,
		)
	}
	return nil
}

func canonicalRepositoryAdmissionRepoSpecs(
	specs []RepositoryAdmissionRepoSpec,
) []RepositoryAdmissionRepoSpec {
	result := append([]RepositoryAdmissionRepoSpec(nil), specs...)
	for index := range result {
		if result[index].Remote == "" {
			result[index].Remote = "origin"
		}
		result[index].Groups = append([]string(nil), result[index].Groups...)
		sort.Strings(result[index].Groups)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func mapRepositoryAdmissionError(err error) error {
	if err == nil {
		return nil
	}
	var sentinel error
	switch {
	case errors.Is(err, ErrRepositoryAdmissionNotFound):
		sentinel = ErrRepositoryAdmissionNotFound
	case errors.Is(err, ErrRepositoryAdmissionFenceLost):
		sentinel = ErrRepositoryAdmissionFenceLost
	case errors.Is(err, ErrRepositoryAdmissionState):
		sentinel = ErrRepositoryAdmissionState
	case errors.Is(err, ErrRepositoryAdmissionConflict):
		sentinel = ErrRepositoryAdmissionConflict
	case errors.Is(err, ErrRepositoryAdmissionInvalid):
		sentinel = ErrRepositoryAdmissionInvalid
	case errors.Is(err, domain.ErrNotFound):
		sentinel = ErrRepositoryAdmissionNotFound
	case errors.Is(err, domain.ErrNotOwner):
		sentinel = ErrRepositoryAdmissionFenceLost
	case errors.Is(err, domain.ErrInvalidTransition):
		sentinel = ErrRepositoryAdmissionState
	case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrConflict):
		sentinel = ErrRepositoryAdmissionConflict
	case errors.Is(err, domain.ErrInvalid):
		sentinel = ErrRepositoryAdmissionInvalid
	default:
		return fmt.Errorf(
			"repository admission transport: %w",
			errors.Join(ErrRepositoryAdmissionUnavailable, err),
		)
	}
	return fmt.Errorf("repository admission transport: %w", errors.Join(sentinel, err))
}
