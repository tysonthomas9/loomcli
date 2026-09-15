package fleet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// fleet-db decodes write bodies with DisallowUnknownFields, so a server whose
// CreateIssueRequest predates one of loom's newer optional fields 400s the
// WHOLE body — the issue is not created at all. The table below names every
// create field that is younger than some deployed fleet-db, together with the
// strict-decode message that server returns for it, so a rejection can be
// recognized, the field stripped, and the create retried.
const (
	unsupportedCreateExternalRefMessage        = `unknown field "external_ref"`
	unsupportedCreateAcceptanceCriteriaMessage = `unknown field "acceptance_criteria"`
)

// createCompatField describes one such field: how to read it off CreateParams,
// how to strip it from a retry, how to apply it afterwards by PATCH, and what
// to say when that PATCH fails too.
type createCompatField struct {
	// name is the wire field name, used in the combined patch-failure message.
	name string
	// unsupportedMessage is the exact strict-decode rejection for this field.
	unsupportedMessage string
	get                func(backend.CreateParams) string
	clear              func(*backend.CreateParams)
	setUpdate          func(*backend.UpdateParams, string)
	// echo writes the value onto the created issue when the PATCH succeeded, for
	// fields backend.IssueData actually carries. nil when it carries none.
	echo func(*backend.IssueData, string)
	// patchFailure is this field's own wording for a failed follow-up PATCH.
	// Used only when it is the single stripped field; see createPatchError.
	patchFailure func(id string) string
}

var createCompatFields = []createCompatField{
	{
		name:               "external_ref",
		unsupportedMessage: unsupportedCreateExternalRefMessage,
		get:                func(p backend.CreateParams) string { return p.ExternalRef },
		clear:              func(p *backend.CreateParams) { p.ExternalRef = "" },
		setUpdate:          func(u *backend.UpdateParams, v string) { u.ExternalRef = &v },
		echo:               func(d *backend.IssueData, v string) { d.ExternalRef = v },
		patchFailure: func(id string) string {
			return fmt.Sprintf("issue %s was created, but setting external_ref failed", id)
		},
	},
	{
		name:               "acceptance_criteria",
		unsupportedMessage: unsupportedCreateAcceptanceCriteriaMessage,
		get:                func(p backend.CreateParams) string { return p.AcceptanceCriteria },
		clear:              func(p *backend.CreateParams) { p.AcceptanceCriteria = "" },
		setUpdate:          func(u *backend.UpdateParams, v string) { u.AcceptanceCriteria = &v },
		// backend.IssueData is the slim projection and carries no acceptance
		// criteria field, so there is nothing to echo back — the value is
		// persisted and comes back on the next Get.
		echo: nil,
		// Unlike external_ref, this PATCH is expected to fail on exactly the
		// servers that need the retry: a fleet-db that rejects the field on
		// create rejects it on PATCH too.
		patchFailure: func(id string) string {
			return fmt.Sprintf(
				"issue %s was created, but this fleet-db does not accept acceptance_criteria (needs fleet-db PR #244)",
				id,
			)
		},
	},
}

// rejectedCreateCompatField matches err against the compat table, returning the
// field whose absence from the server's schema explains the rejection. Only a
// field the caller actually set is a candidate — a create that never sent the
// field cannot be fixed by stripping it, so such an error is passed through
// unchanged rather than retried.
func rejectedCreateCompatField(
	err error,
	params backend.CreateParams,
) (createCompatField, bool) {
	var backendErr *backend.BackendError
	if !errors.As(err, &backendErr) || backendErr.Kind != backend.KindValidation {
		return createCompatField{}, false
	}
	for _, field := range createCompatFields {
		if backendErr.Message == field.unsupportedMessage && field.get(params) != "" {
			return field, true
		}
	}
	return createCompatField{}, false
}

// createWithCompatRetries posts the create body and, whenever the server
// rejects it solely because it does not know one of the fields above, strips
// that field and re-posts. Each rejection strips one MORE field and leaves the
// previously stripped ones out, so a server missing several of them converges;
// the per-field fallbacks this replaced each retried from the caller's original
// params, which re-sent a field the previous attempt had just removed and so
// could never satisfy a server missing two.
//
// The retry body's idempotency key is re-derived from the retry body itself:
// fleet-db 409s on the same key with a different body, so the key must match
// the bytes actually sent.
func (b *FleetBackend) createWithCompatRetries(
	ctx context.Context,
	params backend.CreateParams,
) (*backend.IssueData, error) {
	retryParams := params
	stripped := make([]createCompatField, 0, len(createCompatFields))
	// Each pass removes one field that is currently set, so the loop can run at
	// most once per table entry plus the initial attempt.
	for range len(createCompatFields) + 1 {
		result, err := b.createIssueOnce(ctx, retryParams)
		if err == nil {
			return b.applyStrippedCreateFields(ctx, result, params, stripped)
		}
		field, ok := rejectedCreateCompatField(err, retryParams)
		if !ok {
			return nil, err
		}
		field.clear(&retryParams)
		stripped = append(stripped, field)
		retryKey, keyErr := retryParams.FleetCreateIdempotencyKey(time.Now())
		if keyErr != nil {
			return nil, backend.ErrInternal("Create", "derive compatibility idempotency key", keyErr)
		}
		retryParams.IdempotencyKey = retryKey
	}
	return nil, backend.ErrInternal("Create", "compatibility retries did not converge", nil)
}

// applyStrippedCreateFields PATCHes back, in one request, every field the
// compatibility retries removed from the create body.
func (b *FleetBackend) applyStrippedCreateFields(
	ctx context.Context,
	result *backend.IssueData,
	params backend.CreateParams,
	stripped []createCompatField,
) (*backend.IssueData, error) {
	if len(stripped) == 0 {
		return result, nil
	}
	var update backend.UpdateParams
	for _, field := range stripped {
		field.setUpdate(&update, field.get(params))
	}
	if err := b.Update(ctx, result.ID, update); err != nil {
		// The issue itself was created; return it alongside the classified
		// error so callers that inspect the partial result can still see the ID.
		return result, createPatchError(result.ID, stripped, err)
	}
	for _, field := range stripped {
		if field.echo != nil {
			field.echo(result, field.get(params))
		}
	}
	return result, nil
}

// createPatchError wraps a failed follow-up PATCH. A single stripped field
// keeps its own wording; several share a generic message naming them all.
func createPatchError(id string, stripped []createCompatField, err error) error {
	kind := backend.KindInternal
	var backendErr *backend.BackendError
	if errors.As(err, &backendErr) {
		kind = backendErr.Kind
	}
	message := ""
	if len(stripped) == 1 {
		message = stripped[0].patchFailure(id)
	} else {
		names := make([]string, 0, len(stripped))
		for _, field := range stripped {
			names = append(names, field.name)
		}
		message = fmt.Sprintf("issue %s was created, but setting %s failed", id, strings.Join(names, ", "))
	}
	return backend.NewBackendError(kind, "Create", message, err)
}
