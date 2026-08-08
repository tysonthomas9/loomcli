package repositoryadmission

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	fleettransport "github.com/tysonthomas9/loomcli/internal/infra/fleetdb/transport"
)

const (
	RepositoryAdmissionCapability = "repositories.admission.v1"

	defaultRepositoryAdmissionOwnerLease = 10 * time.Minute
	minRepositoryAdmissionOwnerLease     = time.Second
	maxRepositoryAdmissionOwnerLease     = 30 * time.Minute
)

var (
	ErrRepositoryAdmissionUnavailable = errors.New("fleetdb: repository admission unavailable")
	ErrRepositoryAdmissionNotFound    = errors.New("fleetdb: repository admission not found")
	ErrRepositoryAdmissionInvalid     = errors.New("fleetdb: repository admission invalid")
	ErrRepositoryAdmissionConflict    = errors.New("fleetdb: repository admission conflict")
	ErrRepositoryAdmissionFenceLost   = errors.New("fleetdb: repository admission fence lost")
	ErrRepositoryAdmissionState       = errors.New("fleetdb: repository admission state conflict")

	repositoryAdmissionWorkspaceKeyPattern = regexp.MustCompile(`^[A-Z]([A-Z0-9-]{0,30}[A-Z0-9])?$`)
	repositoryAdmissionRepoNamePattern     = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,98}[a-z0-9])?$`)
	repositoryAdmissionGroupPattern        = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,48}[a-z0-9])?$`)
)

// RepositoryAdmissionTransport is the narrow service-authenticated FleetDB
// process-manager surface. Ordinary Workspace/Repo CRUD is intentionally
// absent: callers cannot publish one repository before the complete batch is
// locally materialized and verified.
type RepositoryAdmissionTransport interface {
	CreateWorkspaceWithRepositoryAdmission(
		context.Context,
		WorkspaceRepositoryAdmissionBeginInput,
	) (*WorkspaceRepositoryAdmissionBeginResult, error)
	BeginRepositoryAdmission(
		context.Context,
		string,
		RepositoryAdmissionBeginInput,
	) (*RepositoryAdmissionRecord, error)
	GetRepositoryAdmission(
		context.Context,
		string,
		string,
	) (*RepositoryAdmissionRecord, error)
	GetRepositoryAdmissionByOperation(
		context.Context,
		string,
		string,
	) (*RepositoryAdmissionRecord, error)
	ListRecoverableRepositoryAdmissions(
		context.Context,
		string,
		int,
	) ([]*RepositoryAdmissionRecord, error)
	RenewRepositoryAdmission(
		context.Context,
		RepositoryAdmissionRenewInput,
	) (*RepositoryAdmissionRecord, error)
	ClaimRepositoryAdmissionRecovery(
		context.Context,
		RepositoryAdmissionRecoveryClaimInput,
	) (*RepositoryAdmissionRecord, error)
	CommitRepositoryAdmission(
		context.Context,
		RepositoryAdmissionCommitInput,
	) (*RepositoryAdmissionRecord, error)
	FailRepositoryAdmission(
		context.Context,
		RepositoryAdmissionFailInput,
	) (*RepositoryAdmissionRecord, error)
	AbortRepositoryAdmission(
		context.Context,
		RepositoryAdmissionAbortInput,
	) (*RepositoryAdmissionRecord, error)
}

type RepositoryAdmissionRepoSpec struct {
	Name          string   `json:"name"`
	RemoteURL     string   `json:"remote_url"`
	Remote        string   `json:"remote,omitempty"`
	DefaultBranch string   `json:"default_branch,omitempty"`
	Groups        []string `json:"groups,omitempty"`
	SourceRepoID  string   `json:"source_repo_id,omitempty"`
}

type RepositoryAdmissionSpec struct {
	WorkspaceKey string                        `json:"workspace_key"`
	OperationID  string                        `json:"operation_id"`
	Repositories []RepositoryAdmissionRepoSpec `json:"repositories"`
}

type RepositoryAdmissionBeginInput struct {
	OperationID  string
	OwnerID      string
	OwnerLease   time.Duration
	Repositories []RepositoryAdmissionRepoSpec
}

type RepositoryAdmissionWorkspaceInput struct {
	Key           string `json:"key"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	State         string `json:"state,omitempty"`
	ErrorMessage  string `json:"error_message,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	DesignFormat  string `json:"design_format,omitempty"`
}

type WorkspaceRepositoryAdmissionBeginInput struct {
	Workspace    RepositoryAdmissionWorkspaceInput
	OperationID  string
	OwnerID      string
	OwnerLease   time.Duration
	Repositories []RepositoryAdmissionRepoSpec
}

type WorkspaceRepositoryAdmissionBeginResult struct {
	Workspace        *workspacemodule.Workspace `json:"workspace"`
	Admission        *RepositoryAdmissionRecord `json:"admission"`
	WorkspaceEventID string                     `json:"workspace_event_id"`
}

type RepositoryAdmissionRepoReceipt struct {
	Repository workspacemodule.Repository `json:"repository"`
	EventID    string                     `json:"event_id"`
}

type RepositoryAdmissionReceipt struct {
	AdmissionID           string                                    `json:"admission_id"`
	SpecFingerprint       string                                    `json:"spec_fingerprint"`
	Repositories          []RepositoryAdmissionRepoReceipt          `json:"repositories"`
	WorkspaceFinalization *RepositoryAdmissionWorkspaceFinalization `json:"workspace_finalization,omitempty"`
	CommittedAt           time.Time                                 `json:"committed_at"`
}

type RepositoryAdmissionWorkspaceFinalization struct {
	State         string `json:"state"`
	DefaultBranch string `json:"default_branch"`
}

type RepositoryAdmissionRecord struct {
	AdmissionID         string                      `json:"admission_id"`
	WorkspaceKey        string                      `json:"workspace_key"`
	OperationID         string                      `json:"operation_id"`
	OwnerID             string                      `json:"owner_id"`
	OwnerGenerationID   string                      `json:"owner_generation_id,omitempty"`
	OwnerLeaseExpiresAt time.Time                   `json:"owner_lease_expires_at"`
	SpecFingerprint     string                      `json:"spec_fingerprint"`
	Spec                RepositoryAdmissionSpec     `json:"spec"`
	State               string                      `json:"state"`
	LastErrorClass      string                      `json:"last_error_class,omitempty"`
	Version             int64                       `json:"version"`
	CreatedAt           time.Time                   `json:"created_at"`
	UpdatedAt           time.Time                   `json:"updated_at"`
	TerminalAt          *time.Time                  `json:"terminal_at,omitempty"`
	Receipt             *RepositoryAdmissionReceipt `json:"receipt,omitempty"`
}

type RepositoryAdmissionGuard struct {
	WorkspaceKey      string
	AdmissionID       string
	OwnerID           string
	OwnerGenerationID string
	SpecFingerprint   string
	ExpectedVersion   int64
}

type RepositoryAdmissionResolvedBranch struct {
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch"`
}

type RepositoryAdmissionRenewInput struct {
	RepositoryAdmissionGuard
	Lease time.Duration
}

type RepositoryAdmissionRecoveryClaimInput struct {
	WorkspaceKey            string
	AdmissionID             string
	ExpectedSpecFingerprint string
	ExpectedVersion         int64
	NewOwnerID              string
	Lease                   time.Duration
}

type RepositoryAdmissionCommitInput struct {
	RepositoryAdmissionGuard
	ResolvedDefaultBranches []RepositoryAdmissionResolvedBranch
	WorkspaceFinalization   *RepositoryAdmissionWorkspaceFinalization
}

type RepositoryAdmissionFailInput struct {
	RepositoryAdmissionGuard
	ErrorClass string
	Retryable  bool
}

type RepositoryAdmissionAbortInput struct {
	RepositoryAdmissionGuard
	ReasonClass string
}

type repositoryAdmissionStore struct {
	client fleettransport.Requester
}

var _ RepositoryAdmissionTransport = (*repositoryAdmissionStore)(nil)

func New(client fleettransport.Requester) RepositoryAdmissionTransport {
	return &repositoryAdmissionStore{client: client}
}

//nolint:funlen // Keep request validation, owner-issued authority, FleetDB write, and receipt verification in one admission transaction.
func (store *repositoryAdmissionStore) CreateWorkspaceWithRepositoryAdmission(
	ctx context.Context,
	input WorkspaceRepositoryAdmissionBeginInput,
) (*WorkspaceRepositoryAdmissionBeginResult, error) {
	if store == nil || store.client == nil {
		return nil, ErrRepositoryAdmissionUnavailable
	}
	begin := RepositoryAdmissionBeginInput{
		OperationID: input.OperationID, OwnerID: input.OwnerID,
		OwnerLease: input.OwnerLease, Repositories: input.Repositories,
	}
	if err := validateRepositoryAdmissionWorkspaceInput(input.Workspace); err != nil {
		return nil, err
	}
	request, err := repositoryAdmissionBeginRequest(begin)
	if err != nil {
		return nil, err
	}
	body := struct {
		Workspace RepositoryAdmissionWorkspaceInput `json:"workspace"`
		repositoryAdmissionBeginRequestWire
	}{
		Workspace:                           input.Workspace,
		repositoryAdmissionBeginRequestWire: request,
	}
	var result WorkspaceRepositoryAdmissionBeginResult
	if err := store.client.Do(
		ctx,
		http.MethodPost,
		"/api/v1/admin/workspace-repository-admissions",
		body,
		&result,
	); err != nil {
		return nil, mapRepositoryAdmissionError(err)
	}
	if err := validateRepositoryAdmissionRecord(result.Admission, false); err != nil {
		return nil, err
	}
	if err := validateRepositoryAdmissionBeginIntent(
		result.Admission,
		input.Workspace.Key,
		begin,
	); err != nil {
		return nil, err
	}
	if err := validateWorkspaceRepositoryAdmissionBeginResult(
		&result,
		input.Workspace,
	); err != nil {
		return nil, err
	}
	return &result, nil
}

func (store *repositoryAdmissionStore) BeginRepositoryAdmission(
	ctx context.Context,
	workspace string,
	input RepositoryAdmissionBeginInput,
) (*RepositoryAdmissionRecord, error) {
	if store == nil || store.client == nil {
		return nil, ErrRepositoryAdmissionUnavailable
	}
	if err := validateRepositoryAdmissionWorkspaceKey(workspace); err != nil {
		return nil, err
	}
	request, err := repositoryAdmissionBeginRequest(input)
	if err != nil {
		return nil, err
	}
	var result RepositoryAdmissionRecord
	if err := store.client.Do(
		ctx,
		http.MethodPost,
		repositoryAdmissionBasePath(workspace),
		request,
		&result,
	); err != nil {
		return nil, mapRepositoryAdmissionError(err)
	}
	if err := validateRepositoryAdmissionRecord(&result, false); err != nil {
		return nil, err
	}
	if err := validateRepositoryAdmissionBeginIntent(
		&result,
		workspace,
		input,
	); err != nil {
		return nil, err
	}
	return &result, nil
}

func (store *repositoryAdmissionStore) GetRepositoryAdmission(
	ctx context.Context,
	workspace,
	admissionID string,
) (*RepositoryAdmissionRecord, error) {
	if store == nil || store.client == nil {
		return nil, ErrRepositoryAdmissionUnavailable
	}
	if err := validateRepositoryAdmissionCoordinates(workspace, admissionID); err != nil {
		return nil, err
	}
	var result RepositoryAdmissionRecord
	if err := store.client.Do(
		ctx,
		http.MethodGet,
		repositoryAdmissionBasePath(workspace)+"/"+pathEscape(admissionID),
		nil,
		&result,
	); err != nil {
		return nil, mapRepositoryAdmissionError(err)
	}
	if err := validateRepositoryAdmissionRecord(&result, false); err != nil {
		return nil, err
	}
	if result.WorkspaceKey != workspace || result.AdmissionID != admissionID {
		return nil, fmt.Errorf(
			"repository admission get response changed requested coordinates: %w",
			ErrRepositoryAdmissionInvalid,
		)
	}
	return &result, nil
}

func (store *repositoryAdmissionStore) GetRepositoryAdmissionByOperation(
	ctx context.Context,
	workspace,
	operationID string,
) (*RepositoryAdmissionRecord, error) {
	if store == nil || store.client == nil {
		return nil, ErrRepositoryAdmissionUnavailable
	}
	if err := validateRepositoryAdmissionWorkspaceKey(workspace); err != nil {
		return nil, err
	}
	if err := validateRepositoryAdmissionOperationID(operationID); err != nil {
		return nil, err
	}
	var result RepositoryAdmissionRecord
	if err := store.client.Do(
		ctx,
		http.MethodGet,
		repositoryAdmissionBasePath(workspace)+"/operations/"+pathEscape(operationID),
		nil,
		&result,
	); err != nil {
		return nil, mapRepositoryAdmissionError(err)
	}
	if err := validateRepositoryAdmissionRecord(&result, true); err != nil {
		return nil, err
	}
	if result.WorkspaceKey != workspace || result.OperationID != operationID {
		return nil, fmt.Errorf(
			"repository admission operation response changed requested coordinates: %w",
			ErrRepositoryAdmissionInvalid,
		)
	}
	return &result, nil
}

//nolint:funlen // Keep paginated recovery decoding, workspace fencing, and record validation in one fail-closed query boundary.
func (store *repositoryAdmissionStore) ListRecoverableRepositoryAdmissions(
	ctx context.Context,
	workspace string,
	limit int,
) ([]*RepositoryAdmissionRecord, error) {
	if store == nil || store.client == nil {
		return nil, ErrRepositoryAdmissionUnavailable
	}
	if err := validateRepositoryAdmissionWorkspaceKey(workspace); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("repository admission recovery limit: %w", ErrRepositoryAdmissionInvalid)
	}
	var response struct {
		RepositoryAdmissions []*RepositoryAdmissionRecord `json:"repository_admissions"`
		Count                int                          `json:"count"`
	}
	path := repositoryAdmissionBasePath(workspace) +
		"/recoverable?limit=" + url.QueryEscape(strconv.Itoa(limit))
	if err := store.client.Do(
		ctx,
		http.MethodGet,
		path,
		nil,
		&response,
	); err != nil {
		return nil, mapRepositoryAdmissionError(err)
	}
	if response.Count != len(response.RepositoryAdmissions) {
		return nil, fmt.Errorf(
			"recoverable repository admission response count: %w",
			ErrRepositoryAdmissionInvalid,
		)
	}
	if len(response.RepositoryAdmissions) > limit {
		return nil, fmt.Errorf(
			"recoverable repository admission response exceeded requested limit: %w",
			ErrRepositoryAdmissionInvalid,
		)
	}
	seen := make(map[string]struct{}, len(response.RepositoryAdmissions))
	for _, record := range response.RepositoryAdmissions {
		if err := validateRepositoryAdmissionRecord(record, false); err != nil {
			return nil, err
		}
		if record.WorkspaceKey != workspace ||
			(record.State != "pending" &&
				record.State != "retryable_failed") {
			return nil, fmt.Errorf(
				"recoverable repository admission returned divergent coordinates or state: %w",
				ErrRepositoryAdmissionInvalid,
			)
		}
		if _, duplicate := seen[record.AdmissionID]; duplicate {
			return nil, fmt.Errorf(
				"recoverable repository admission response duplicated an admission: %w",
				ErrRepositoryAdmissionInvalid,
			)
		}
		seen[record.AdmissionID] = struct{}{}
	}
	return response.RepositoryAdmissions, nil
}

func (store *repositoryAdmissionStore) RenewRepositoryAdmission(
	ctx context.Context,
	input RepositoryAdmissionRenewInput,
) (*RepositoryAdmissionRecord, error) {
	if err := validateRepositoryAdmissionGuard(input.RepositoryAdmissionGuard); err != nil {
		return nil, err
	}
	leaseSeconds, err := repositoryAdmissionRequiredLeaseSeconds(input.Lease)
	if err != nil {
		return nil, err
	}
	body := struct {
		repositoryAdmissionGuardWire
		LeaseSeconds int `json:"lease_seconds"`
	}{
		repositoryAdmissionGuardWire: repositoryAdmissionGuardRequest(input.RepositoryAdmissionGuard),
		LeaseSeconds:                 leaseSeconds,
	}
	result, err := store.mutate(
		ctx,
		input.RepositoryAdmissionGuard,
		"renew",
		body,
	)
	if err != nil {
		return nil, err
	}
	if result.State != "pending" ||
		result.Version != input.ExpectedVersion+1 {
		return nil, fmt.Errorf(
			"repository admission renewal response changed state or version: %w",
			ErrRepositoryAdmissionInvalid,
		)
	}
	return result, nil
}

//nolint:funlen // Keep recovery claim authority, generation fencing, response decoding, and validation in one owner-fenced transaction.
func (store *repositoryAdmissionStore) ClaimRepositoryAdmissionRecovery(
	ctx context.Context,
	input RepositoryAdmissionRecoveryClaimInput,
) (*RepositoryAdmissionRecord, error) {
	if store == nil || store.client == nil {
		return nil, ErrRepositoryAdmissionUnavailable
	}
	if err := validateRepositoryAdmissionCoordinates(input.WorkspaceKey, input.AdmissionID); err != nil {
		return nil, err
	}
	if err := validateRepositoryAdmissionFingerprint(input.ExpectedSpecFingerprint); err != nil ||
		input.ExpectedVersion < 1 ||
		strings.TrimSpace(input.NewOwnerID) == "" ||
		input.NewOwnerID != strings.TrimSpace(input.NewOwnerID) ||
		len(input.NewOwnerID) > 200 {
		return nil, fmt.Errorf("repository admission recovery claim: %w", ErrRepositoryAdmissionInvalid)
	}
	leaseSeconds, err := repositoryAdmissionRequiredLeaseSeconds(input.Lease)
	if err != nil {
		return nil, err
	}
	body := struct {
		ExpectedSpecFingerprint string `json:"expected_spec_fingerprint"`
		ExpectedVersion         int64  `json:"expected_version"`
		NewOwnerID              string `json:"new_owner_id"`
		LeaseSeconds            int    `json:"lease_seconds"`
	}{
		ExpectedSpecFingerprint: input.ExpectedSpecFingerprint,
		ExpectedVersion:         input.ExpectedVersion,
		NewOwnerID:              input.NewOwnerID,
		LeaseSeconds:            leaseSeconds,
	}
	var result RepositoryAdmissionRecord
	if err := store.client.Do(
		ctx,
		http.MethodPost,
		repositoryAdmissionBasePath(input.WorkspaceKey)+"/"+
			pathEscape(input.AdmissionID)+"/claim-recovery",
		body,
		&result,
	); err != nil {
		return nil, mapRepositoryAdmissionError(err)
	}
	if err := validateRepositoryAdmissionRecord(&result, false); err != nil {
		return nil, err
	}
	if result.WorkspaceKey != input.WorkspaceKey ||
		result.AdmissionID != input.AdmissionID ||
		result.SpecFingerprint != input.ExpectedSpecFingerprint ||
		result.OwnerID != input.NewOwnerID ||
		result.State != "pending" ||
		result.Version != input.ExpectedVersion+1 {
		return nil, fmt.Errorf(
			"repository admission recovery response changed owner coordinates, state, or version: %w",
			ErrRepositoryAdmissionInvalid,
		)
	}
	return &result, nil
}

func (store *repositoryAdmissionStore) CommitRepositoryAdmission(
	ctx context.Context,
	input RepositoryAdmissionCommitInput,
) (*RepositoryAdmissionRecord, error) {
	if err := validateRepositoryAdmissionGuard(input.RepositoryAdmissionGuard); err != nil {
		return nil, err
	}
	if err := validateRepositoryAdmissionResolvedBranches(input.ResolvedDefaultBranches); err != nil {
		return nil, err
	}
	if err := validateRepositoryAdmissionWorkspaceFinalization(input.WorkspaceFinalization); err != nil {
		return nil, err
	}
	body := struct {
		repositoryAdmissionGuardWire
		ResolvedDefaultBranches []RepositoryAdmissionResolvedBranch       `json:"resolved_default_branches"`
		WorkspaceFinalization   *RepositoryAdmissionWorkspaceFinalization `json:"workspace_finalization,omitempty"`
	}{
		repositoryAdmissionGuardWire: repositoryAdmissionGuardRequest(input.RepositoryAdmissionGuard),
		ResolvedDefaultBranches: append(
			[]RepositoryAdmissionResolvedBranch(nil),
			input.ResolvedDefaultBranches...,
		),
		WorkspaceFinalization: input.WorkspaceFinalization,
	}
	result, err := store.mutate(
		ctx,
		input.RepositoryAdmissionGuard,
		"commit",
		body,
	)
	if err != nil {
		return nil, err
	}
	if !repositoryAdmissionCommitResponseMatches(result, input) {
		return nil, fmt.Errorf(
			"repository admission commit response changed resolution: %w",
			ErrRepositoryAdmissionInvalid,
		)
	}
	return result, nil
}

func (store *repositoryAdmissionStore) FailRepositoryAdmission(
	ctx context.Context,
	input RepositoryAdmissionFailInput,
) (*RepositoryAdmissionRecord, error) {
	if err := validateRepositoryAdmissionGuard(input.RepositoryAdmissionGuard); err != nil {
		return nil, err
	}
	if !validRepositoryAdmissionErrorClass(input.ErrorClass) {
		return nil, fmt.Errorf("repository admission error class: %w", ErrRepositoryAdmissionInvalid)
	}
	body := struct {
		repositoryAdmissionGuardWire
		ErrorClass string `json:"error_class"`
		Retryable  bool   `json:"retryable"`
	}{
		repositoryAdmissionGuardWire: repositoryAdmissionGuardRequest(input.RepositoryAdmissionGuard),
		ErrorClass:                   input.ErrorClass,
		Retryable:                    input.Retryable,
	}
	result, err := store.mutate(
		ctx,
		input.RepositoryAdmissionGuard,
		"fail",
		body,
	)
	if err != nil {
		return nil, err
	}
	expectedState := "permanent_failed"
	if input.Retryable {
		expectedState = "retryable_failed"
	}
	if result.State != expectedState ||
		result.LastErrorClass != input.ErrorClass ||
		result.Version < input.ExpectedVersion {
		return nil, fmt.Errorf(
			"repository admission failure response changed outcome: %w",
			ErrRepositoryAdmissionInvalid,
		)
	}
	return result, nil
}

func (store *repositoryAdmissionStore) AbortRepositoryAdmission(
	ctx context.Context,
	input RepositoryAdmissionAbortInput,
) (*RepositoryAdmissionRecord, error) {
	if err := validateRepositoryAdmissionGuard(input.RepositoryAdmissionGuard); err != nil {
		return nil, err
	}
	if !validRepositoryAdmissionErrorClass(input.ReasonClass) {
		return nil, fmt.Errorf("repository admission abort class: %w", ErrRepositoryAdmissionInvalid)
	}
	body := struct {
		repositoryAdmissionGuardWire
		ReasonClass string `json:"reason_class"`
	}{
		repositoryAdmissionGuardWire: repositoryAdmissionGuardRequest(input.RepositoryAdmissionGuard),
		ReasonClass:                  input.ReasonClass,
	}
	result, err := store.mutate(
		ctx,
		input.RepositoryAdmissionGuard,
		"abort",
		body,
	)
	if err != nil {
		return nil, err
	}
	if result.State != "aborted" ||
		result.LastErrorClass != input.ReasonClass ||
		result.Version < input.ExpectedVersion {
		return nil, fmt.Errorf(
			"repository admission abort response changed outcome: %w",
			ErrRepositoryAdmissionInvalid,
		)
	}
	return result, nil
}

func (store *repositoryAdmissionStore) mutate(
	ctx context.Context,
	guard RepositoryAdmissionGuard,
	action string,
	body any,
) (*RepositoryAdmissionRecord, error) {
	if store == nil || store.client == nil {
		return nil, ErrRepositoryAdmissionUnavailable
	}
	var result RepositoryAdmissionRecord
	if err := store.client.Do(
		ctx,
		http.MethodPost,
		repositoryAdmissionBasePath(guard.WorkspaceKey)+"/"+
			pathEscape(guard.AdmissionID)+"/"+action,
		body,
		&result,
	); err != nil {
		return nil, mapRepositoryAdmissionError(err)
	}
	if err := validateRepositoryAdmissionRecord(&result, false); err != nil {
		return nil, err
	}
	if result.WorkspaceKey != guard.WorkspaceKey ||
		result.AdmissionID != guard.AdmissionID ||
		result.OwnerID != guard.OwnerID ||
		result.OwnerGenerationID != guard.OwnerGenerationID ||
		result.SpecFingerprint != guard.SpecFingerprint {
		return nil, fmt.Errorf(
			"repository admission mutation response changed guard coordinates: %w",
			ErrRepositoryAdmissionInvalid,
		)
	}
	return &result, nil
}

func repositoryAdmissionCommitResponseMatches(
	record *RepositoryAdmissionRecord,
	input RepositoryAdmissionCommitInput,
) bool {
	if record == nil ||
		record.State != "committed" ||
		record.Receipt == nil ||
		record.Version < input.ExpectedVersion ||
		!sameRepositoryAdmissionWorkspaceFinalization(
			record.Receipt.WorkspaceFinalization,
			input.WorkspaceFinalization,
		) ||
		len(record.Receipt.Repositories) !=
			len(input.ResolvedDefaultBranches) {
		return false
	}
	resolved := make(
		map[string]string,
		len(input.ResolvedDefaultBranches),
	)
	for _, branch := range input.ResolvedDefaultBranches {
		if _, duplicate := resolved[branch.Name]; duplicate {
			return false
		}
		resolved[branch.Name] = branch.DefaultBranch
	}
	for _, receipt := range record.Receipt.Repositories {
		branch, exists := resolved[receipt.Repository.Name]
		if !exists || branch != receipt.Repository.DefaultBranch {
			return false
		}
		delete(resolved, receipt.Repository.Name)
	}
	return len(resolved) == 0
}

func sameRepositoryAdmissionWorkspaceFinalization(
	left *RepositoryAdmissionWorkspaceFinalization,
	right *RepositoryAdmissionWorkspaceFinalization,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.State == right.State &&
		left.DefaultBranch == right.DefaultBranch
}

func pathEscape(value string) string {
	return fleettransport.PathEscape(value)
}

func withQuery(path string, query url.Values) string {
	return fleettransport.WithQuery(path, query)
}
