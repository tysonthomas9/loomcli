package driverapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
)

// Label write ops close the loop opened by the issue-journal bridge: the
// bridge turns a label mutation into a trigger event, and these ops let the
// workflow that event started write labels back. Together they are the
// label-as-state-machine mechanic the daemon path already relies on, made
// available to Flue workflows.
//
// Attribution caveat (verified against fleet-db, not a design intent):
// the actor recorded on the resulting journal event is the AUTHENTICATED
// identity of the fleet-db credential loomcli holds, NOT the
// driver-run:<id> actor computed here. fleet-db strips the client-supplied
// X-Actor header as anti-spoofing (internal/auth/middleware.go), resolves
// the actor from the authenticated context (internal/api/labels.go) and
// journals that (internal/service/issue_service.go). The actor passed to
// issueBackends below therefore reaches fleet-db's HTTP client but does not
// survive into the journal.
//
// Consequence: a trigger binding CANNOT use an actor filter to tell a
// workflow's own label write apart from any other write made with the same
// credential, so exclude_actor_kinds does not provide self-trigger safety on
// journal-sourced label bindings. Discriminate on event content (e.g. a
// sentinel label) instead. TestLabelOpsAttribution pins the current behavior
// and fails loudly if fleet-db ever starts honoring the supplied actor.

// labelParams is the camelCase driver-op request wire shared by add-label and
// remove-label.
type labelParams struct {
	IssueID string `json:"issueId"`
	Label   string `json:"label"`
}

// labelResponse echoes the applied mutation. Actor is the actor loomcli
// supplied to the issue backend — see the attribution caveat above for why it
// is not necessarily what lands in the journal.
type labelResponse struct {
	IssueID string `json:"issueId"`
	Label   string `json:"label"`
	Actor   string `json:"actor"`
}

func (m *Module) addLabel(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	return m.mutateLabel(ctx, ws, id, body, true)
}

func (m *Module) removeLabel(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	return m.mutateLabel(ctx, ws, id, body, false)
}

// mutateLabel is the shared add/remove body: decode, validate, prove the
// caller owns a RUNNING parent DriverRun, then apply through the workspace
// issue backend. Both ops are idempotent at the backend (AddLabel is a no-op
// when present, RemoveLabel when absent), so an SDK retry is safe.
func (m *Module) mutateLabel(ctx context.Context, ws string, id driverIdentity, body []byte, add bool) (any, error) {
	params, err := decodeParams[labelParams](body)
	if err != nil {
		return nil, err
	}
	issueID := strings.TrimSpace(params.IssueID)
	if issueID == "" {
		return nil, fmt.Errorf("issue id required: %w", domain.ErrInvalid)
	}
	label := strings.TrimSpace(params.Label)
	if label == "" {
		return nil, fmt.Errorf("label required: %w", domain.ErrInvalid)
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	actor := driverpkg.DriverRunActor(parent.RunID)
	issueBackend, err := m.issueBackends(ws, actor)
	if err != nil {
		return nil, err
	}
	if add {
		err = issueBackend.AddLabel(ctx, issueID, label)
	} else {
		err = issueBackend.RemoveLabel(ctx, issueID, label)
	}
	if err != nil {
		verb := "add label"
		if !add {
			verb = "remove label"
		}
		return nil, fmt.Errorf("%s: %w", verb, err)
	}
	return labelResponse{IssueID: issueID, Label: label, Actor: actor}, nil
}
