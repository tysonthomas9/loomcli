// Package connectorgrants coordinates an exact Automation binding snapshot
// with the complete Connectors authority set for that binding. Neither
// capability imports the other; this named application workflow owns the
// cross-capability replacement ceremony.
package connectorgrants

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

// ErrGrantSetUnavailable means the workflow was composed without every
// capability port required for authoritative grant-set replacement.
var ErrGrantSetUnavailable = errors.New("connector grants: replacement workflow unavailable")

// DesiredGrant is one action/resource tuple in the complete authority set a
// connector should expose to a binding.
type DesiredGrant struct {
	Action          string
	ResourcePattern string
}

// ReplaceGrantSetRequest binds a complete connector authority set to an exact
// disabled trigger-binding generation and revision.
type ReplaceGrantSetRequest struct {
	WorkspaceKey             string
	ConnectorID              string
	BindingID                string
	ExpectedBindingCreatedAt time.Time
	ExpectedBindingUpdatedAt time.Time
	Grants                   []DesiredGrant
}

// ReplaceGrantSetResult reports the converged active set. Revoked counts only
// rows changed by this call; exact retries therefore report zero.
type ReplaceGrantSetResult struct {
	BindingID        string                       `json:"binding_id"`
	BindingCreatedAt time.Time                    `json:"binding_created_at"`
	BindingUpdatedAt time.Time                    `json:"binding_updated_at"`
	Grants           []*connectors.ConnectorGrant `json:"grants"`
	GrantsRevoked    int                          `json:"grants_revoked"`
}

// Workflow owns the restartable least-privilege replacement ceremony.
// The mutex serializes calls through one serve process; exact binding snapshot
// checks remain the authoritative generation/revision fence across modules.
type Workflow struct {
	bindings BindingReader
	grants   ConnectorManagement
	mu       sync.Mutex
}

// BindingReader is the one Automation query required for the generation and
// revision fence. The workflow cannot list or mutate bindings.
type BindingReader interface {
	GetBinding(context.Context, string, string) (*automation.Binding, error)
}

// ConnectorManagement is the exact Connectors public surface used by this
// workflow. It excludes connector creation, credential lifecycle, calls, and
// provider dispatch.
type ConnectorManagement interface {
	GetConnector(context.Context, connectors.GetConnectorQuery) (*connectors.Connector, error)
	CreateGrant(context.Context, connectors.CreateGrantCommand) (*connectors.ConnectorGrant, error)
	RevokeGrant(context.Context, connectors.RevokeGrantCommand) error
	ListGrants(context.Context, connectors.ListGrantsQuery) ([]*connectors.ConnectorGrant, error)
}

func New(bindings BindingReader, grants ConnectorManagement) (*Workflow, error) {
	if bindings == nil || grants == nil {
		return nil, ErrGrantSetUnavailable
	}
	return &Workflow{bindings: bindings, grants: grants}, nil
}

// Replace installs the complete active authority set for one connector and one
// exact trigger-binding revision. Stale scopes are revoked before replacements
// are created, and the binding must remain disabled for every side effect.
//
// Replace deliberately does not enable the binding. Its caller may do so only
// after success, which makes every partial/retry state inert.
func (r *Workflow) Replace(ctx context.Context, request ReplaceGrantSetRequest) (ReplaceGrantSetResult, error) {
	if ctx == nil {
		return ReplaceGrantSetResult{}, fmt.Errorf("context is required: %w", connectors.ErrInvalid)
	}
	request, desired, err := prepareReplaceGrantSetRequest(request)
	if err != nil {
		return ReplaceGrantSetResult{}, err
	}
	if r == nil || r.bindings == nil || r.grants == nil {
		return ReplaceGrantSetResult{}, ErrGrantSetUnavailable
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	binding, err := r.requireExactDisabledBinding(ctx, request)
	if err != nil {
		return ReplaceGrantSetResult{}, err
	}
	if _, err := r.grants.GetConnector(ctx, connectors.GetConnectorQuery{
		WorkspaceKey: request.WorkspaceKey, ConnectorID: request.ConnectorID,
	}); err != nil {
		return ReplaceGrantSetResult{}, fmt.Errorf("get connector %q: %w", request.ConnectorID, err)
	}

	current, err := r.grants.ListGrants(ctx, connectors.ListGrantsQuery{
		WorkspaceKey: request.WorkspaceKey, BindingID: request.BindingID,
	})
	if err != nil {
		return ReplaceGrantSetResult{}, fmt.Errorf("list connector grants: %w", err)
	}
	setToken := desiredGrantSetToken(desired)
	preserved, obsolete, err := classifyCurrentGrants(current, desired, binding, request, setToken)
	if err != nil {
		return ReplaceGrantSetResult{}, err
	}

	revoked, err := r.revokeObsoleteGrants(ctx, request, obsolete)
	if err != nil {
		return ReplaceGrantSetResult{}, err
	}

	resolved, err := r.resolveDesiredGrants(ctx, request, binding, desired, preserved, setToken)
	if err != nil {
		return ReplaceGrantSetResult{}, err
	}

	if _, err := r.requireExactDisabledBinding(ctx, request); err != nil {
		return ReplaceGrantSetResult{}, err
	}
	return ReplaceGrantSetResult{
		BindingID:        binding.BindingID,
		BindingCreatedAt: binding.CreatedAt,
		BindingUpdatedAt: binding.UpdatedAt,
		Grants:           resolved,
		GrantsRevoked:    revoked,
	}, nil
}

func prepareReplaceGrantSetRequest(
	request ReplaceGrantSetRequest,
) (ReplaceGrantSetRequest, []DesiredGrant, error) {
	request.WorkspaceKey = strings.TrimSpace(request.WorkspaceKey)
	request.ConnectorID = strings.TrimSpace(request.ConnectorID)
	request.BindingID = strings.TrimSpace(request.BindingID)
	if request.WorkspaceKey == "" || request.ConnectorID == "" || request.BindingID == "" {
		return ReplaceGrantSetRequest{}, nil, fmt.Errorf(
			"workspace, connector id and binding id are required: %w",
			connectors.ErrInvalid,
		)
	}
	if request.ExpectedBindingCreatedAt.IsZero() || request.ExpectedBindingUpdatedAt.IsZero() {
		return ReplaceGrantSetRequest{}, nil, fmt.Errorf(
			"expected binding generation and revision are required: %w",
			connectors.ErrInvalid,
		)
	}
	desired, err := normalizeDesiredGrants(request.Grants)
	if err != nil {
		return ReplaceGrantSetRequest{}, nil, err
	}
	return request, desired, nil
}

func classifyCurrentGrants(
	current []*connectors.ConnectorGrant,
	desired []DesiredGrant,
	binding *automation.Binding,
	request ReplaceGrantSetRequest,
	setToken string,
) (map[string]*connectors.ConnectorGrant, []*connectors.ConnectorGrant, error) {
	desiredByKey := make(map[string]DesiredGrant, len(desired))
	for _, grant := range desired {
		desiredByKey[grantKey(grant.Action, grant.ResourcePattern)] = grant
	}
	preserved := make(map[string]*connectors.ConnectorGrant, len(desired))
	obsolete := make([]*connectors.ConnectorGrant, 0)
	for _, grant := range current {
		if grant == nil || grant.ConnectorID != request.ConnectorID {
			continue
		}
		key := grantKey(grant.Action, grant.ResourcePattern)
		wantedGrant, wanted := desiredByKey[key]
		matchesDesiredRevision := wanted &&
			grantMatchesDesiredRevision(grant, binding, request.ConnectorID, wantedGrant, setToken)
		if grantMatchesRevision(grant, binding, request.ConnectorID) && !matchesDesiredRevision {
			return nil, nil, fmt.Errorf(
				"trigger binding %q already has a different connector grant set for this revision: %w",
				request.BindingID,
				connectors.ErrConflict,
			)
		}
		if matchesDesiredRevision && preserved[key] == nil {
			preserved[key] = grant
			continue
		}
		obsolete = append(obsolete, grant)
	}
	return preserved, obsolete, nil
}

func (r *Workflow) revokeObsoleteGrants(
	ctx context.Context,
	request ReplaceGrantSetRequest,
	obsolete []*connectors.ConnectorGrant,
) (int, error) {
	revoked := 0
	for _, grant := range obsolete {
		if _, err := r.requireExactDisabledBinding(ctx, request); err != nil {
			return 0, err
		}
		if err := r.grants.RevokeGrant(ctx, connectors.RevokeGrantCommand{
			WorkspaceKey: request.WorkspaceKey, GrantID: grant.GrantID,
		}); err != nil {
			if errors.Is(err, connectors.ErrGrantRevoked) {
				continue
			}
			return 0, fmt.Errorf("revoke stale connector grant %q: %w", grant.GrantID, err)
		}
		revoked++
	}
	return revoked, nil
}

func (r *Workflow) resolveDesiredGrants(
	ctx context.Context,
	request ReplaceGrantSetRequest,
	binding *automation.Binding,
	desired []DesiredGrant,
	preserved map[string]*connectors.ConnectorGrant,
	setToken string,
) ([]*connectors.ConnectorGrant, error) {
	resolved := make([]*connectors.ConnectorGrant, 0, len(desired))
	for _, grant := range desired {
		key := grantKey(grant.Action, grant.ResourcePattern)
		if existing := preserved[key]; existing != nil {
			resolved = append(resolved, existing)
			continue
		}
		if _, err := r.requireExactDisabledBinding(ctx, request); err != nil {
			return nil, err
		}
		created, err := r.createGrant(ctx, binding, request.ConnectorID, grant, setToken)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, created)
	}
	return resolved, nil
}

func normalizeDesiredGrants(requests []DesiredGrant) ([]DesiredGrant, error) {
	if requests == nil {
		return nil, fmt.Errorf("grants must be an array: %w", connectors.ErrInvalid)
	}
	desired := make([]DesiredGrant, 0, len(requests))
	seen := make(map[string]struct{}, len(requests))
	for _, request := range requests {
		action, err := connectors.NormalizeAction(request.Action)
		if err != nil {
			return nil, err
		}
		resource := strings.TrimSpace(request.ResourcePattern)
		if resource == "" {
			return nil, fmt.Errorf("resource_pattern is required: %w", connectors.ErrInvalid)
		}
		key := grantKey(action, resource)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		desired = append(desired, DesiredGrant{Action: action, ResourcePattern: resource})
	}
	return desired, nil
}

func (r *Workflow) requireExactDisabledBinding(
	ctx context.Context,
	request ReplaceGrantSetRequest,
) (*automation.Binding, error) {
	binding, err := r.bindings.GetBinding(ctx, request.WorkspaceKey, request.BindingID)
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, fmt.Errorf("trigger binding %q returned no record: %w", request.BindingID, automation.ErrInvalidPersistedState)
	}
	if !binding.CreatedAt.Equal(request.ExpectedBindingCreatedAt) ||
		!binding.UpdatedAt.Equal(request.ExpectedBindingUpdatedAt) {
		return nil, fmt.Errorf(
			"trigger binding %q changed generation or revision while replacing grants: %w",
			request.BindingID,
			automation.ErrConflict,
		)
	}
	if binding.Enabled {
		return nil, fmt.Errorf(
			"trigger binding %q must be disabled while replacing grants: %w",
			request.BindingID,
			automation.ErrConflict,
		)
	}
	return binding, nil
}

func (r *Workflow) createGrant(
	ctx context.Context,
	binding *automation.Binding,
	connectorID string,
	grant DesiredGrant,
	setToken string,
) (*connectors.ConnectorGrant, error) {
	in := connectors.CreateGrantCommand{
		WorkspaceKey:    binding.WorkspaceKey,
		GrantID:         reconciledGrantID(binding, connectorID, grant, setToken),
		ConnectorID:     connectorID,
		BindingID:       binding.BindingID,
		Action:          grant.Action,
		ResourcePattern: grant.ResourcePattern,
	}
	created, err := r.grants.CreateGrant(ctx, in)
	if err == nil {
		return created, nil
	}
	if !errors.Is(err, connectors.ErrAlreadyExists) && !errors.Is(err, connectors.ErrConflict) {
		return nil, fmt.Errorf("create replacement connector grant: %w", err)
	}
	// A lost successful response is reconciled by the active exact grant. A
	// revoked tombstone with the deterministic id gets a revision-local fallback
	// identity so the complete-set operation remains retryable.
	existing, listErr := r.findMatchingRevisionGrant(ctx, binding, connectorID, grant, setToken)
	if listErr != nil {
		return nil, fmt.Errorf("verify replacement connector grant after create collision: %w", listErr)
	}
	if existing != nil {
		return existing, nil
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", in.GrantID, time.Now().UTC().UnixNano())))
	in.GrantID = fmt.Sprintf("%s-n%x", in.GrantID, sum[:4])
	created, err = r.grants.CreateGrant(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("create replacement connector grant: %w", err)
	}
	return created, nil
}

func (r *Workflow) findMatchingRevisionGrant(
	ctx context.Context,
	binding *automation.Binding,
	connectorID string,
	desired DesiredGrant,
	setToken string,
) (*connectors.ConnectorGrant, error) {
	grants, err := r.grants.ListGrants(ctx, connectors.ListGrantsQuery{
		WorkspaceKey: binding.WorkspaceKey, BindingID: binding.BindingID,
	})
	if err != nil {
		return nil, err
	}
	for _, grant := range grants {
		if grant != nil &&
			grant.ConnectorID == connectorID &&
			grant.Action == desired.Action &&
			grant.ResourcePattern == desired.ResourcePattern &&
			grantMatchesDesiredRevision(grant, binding, connectorID, desired, setToken) {
			return grant, nil
		}
	}
	return nil, nil
}

func grantKey(action, resource string) string {
	return action + "\x00" + resource
}

func bindingGenerationToken(binding *automation.Binding) string {
	if binding == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(
		binding.WorkspaceKey + "\x00" + binding.BindingID + "\x00" +
			binding.CreatedAt.UTC().Format(time.RFC3339Nano),
	))
	return fmt.Sprintf("%x", sum[:6])
}

func bindingRevisionToken(binding *automation.Binding, connectorID string) string {
	if binding == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(
		binding.WorkspaceKey + "\x00" + binding.BindingID + "\x00" +
			binding.CreatedAt.UTC().Format(time.RFC3339Nano) + "\x00" +
			binding.UpdatedAt.UTC().Format(time.RFC3339Nano) + "\x00" +
			connectorID,
	))
	return fmt.Sprintf("%x", sum[:6])
}

func desiredGrantSetToken(grants []DesiredGrant) string {
	keys := make([]string, 0, len(grants))
	for _, grant := range grants {
		keys = append(keys, grantKey(grant.Action, grant.ResourcePattern))
	}
	sort.Strings(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, "\x01")))
	return fmt.Sprintf("%x", sum[:6])
}

func grantMatchesRevision(
	grant *connectors.ConnectorGrant,
	binding *automation.Binding,
	connectorID string,
) bool {
	if grant == nil || binding == nil {
		return false
	}
	marker := "-g" + bindingGenerationToken(binding) + "-v" + bindingRevisionToken(binding, connectorID) + "-"
	return strings.Contains(grant.GrantID, marker)
}

func grantMatchesDesiredRevision(
	grant *connectors.ConnectorGrant,
	binding *automation.Binding,
	connectorID string,
	desired DesiredGrant,
	setToken string,
) bool {
	if grant == nil {
		return false
	}
	expected := reconciledGrantID(binding, connectorID, desired, setToken)
	return grant.GrantID == expected || strings.HasPrefix(grant.GrantID, expected+"-n")
}

func reconciledGrantID(
	binding *automation.Binding,
	connectorID string,
	grant DesiredGrant,
	setToken string,
) string {
	base := "grant-" + binding.BindingID + "-" + strings.ReplaceAll(grant.Action, ".", "-")
	sum := sha256.Sum256([]byte(
		connectorID + "\x00" + grant.Action + "\x00" + grant.ResourcePattern,
	))
	return fmt.Sprintf(
		"%s-g%s-v%s-s%s-a%x",
		base,
		bindingGenerationToken(binding),
		bindingRevisionToken(binding, connectorID),
		setToken,
		sum[:4],
	)
}
