package fleet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// createCompatField describes a create-body field that current fleet-db
// servers accept but older deployed ones reject outright: fleet-db decodes
// with DisallowUnknownFields, so one unknown field 400s the whole body.
// Create strips such a field, retries the create, and then applies the value
// through PATCH — turning a hard failure into a two-request success.
type createCompatField struct {
	name  string
	value func(backend.CreateParams) string
	strip func(*backend.CreateParams)
	patch func(*backend.UpdateParams, string)
	// apply mirrors the PATCHed value onto the returned projection. Nil when
	// the slim IssueData projection does not carry the field at all
	// (acceptance_criteria lives on IssueDetailData, which Create never
	// returns), in which case there is nothing to mirror.
	apply func(*backend.IssueData, string)
}

var createCompatFields = []createCompatField{
	{
		name:  "external_ref",
		value: func(p backend.CreateParams) string { return p.ExternalRef },
		strip: func(p *backend.CreateParams) { p.ExternalRef = "" },
		patch: func(u *backend.UpdateParams, v string) { u.ExternalRef = &v },
		apply: func(d *backend.IssueData, v string) { d.ExternalRef = v },
	},
	{
		name:  "acceptance_criteria",
		value: func(p backend.CreateParams) string { return p.AcceptanceCriteria },
		strip: func(p *backend.CreateParams) { p.AcceptanceCriteria = "" },
		patch: func(u *backend.UpdateParams, v string) { u.AcceptanceCriteria = &v },
	},
}

// unsupportedCreateFieldMessage is the strict-decode error fleet-db returns
// for a create body field its schema does not know.
func unsupportedCreateFieldMessage(name string) string {
	return fmt.Sprintf("unknown field %q", name)
}

// unsupportedCreateField reports which compat field the server rejected as
// unknown, or nil for any other error — including a rejection naming a field
// this request did not set, where stripping would change nothing.
func unsupportedCreateField(err error, params backend.CreateParams) *createCompatField {
	var backendErr *backend.BackendError
	if !errors.As(err, &backendErr) || backendErr.Kind != backend.KindValidation {
		return nil
	}
	for i := range createCompatFields {
		f := &createCompatFields[i]
		if backendErr.Message == unsupportedCreateFieldMessage(f.name) && f.value(params) != "" {
			return f
		}
	}
	return nil
}

// createWithoutUnsupportedFields retries the create with the rejected field
// removed, then PATCHes the stripped values back. A server may reject one
// unknown field at a time, so the retry loop keeps stripping for as long as
// the failure names another compat field; it terminates because a stripped
// field is empty and can never be selected again.
func (b *FleetBackend) createWithoutUnsupportedFields(
	ctx context.Context,
	params backend.CreateParams,
	rejected *createCompatField,
) (*backend.IssueData, error) {
	retryParams := params
	stripped := make([]*createCompatField, 0, len(createCompatFields))
	var result *backend.IssueData
	for field := rejected; field != nil; {
		field.strip(&retryParams)
		stripped = append(stripped, field)

		// The idempotency key must be derived from the bytes actually sent:
		// fleet-db 409s a reused key whose body differs.
		retryKey, err := retryParams.FleetCreateIdempotencyKey(time.Now())
		if err != nil {
			return nil, backend.ErrInternal("Create", "derive compatibility idempotency key", err)
		}
		retryParams.IdempotencyKey = retryKey

		created, err := b.createIssueOnce(ctx, retryParams)
		if err == nil {
			result = created
			break
		}
		field = unsupportedCreateField(err, retryParams)
		if field == nil {
			return nil, err
		}
	}

	update := backend.UpdateParams{}
	names := make([]string, 0, len(stripped))
	for _, field := range stripped {
		field.patch(&update, field.value(params))
		names = append(names, field.name)
	}
	if err := b.Update(ctx, result.ID, update); err != nil {
		// The issue itself was created; return it alongside the classified
		// error so callers that inspect the partial result can still see the ID.
		return result, createCompatPatchError(result.ID, names, err)
	}
	for _, field := range stripped {
		if field.apply != nil {
			field.apply(result, field.value(params))
		}
	}
	return result, nil
}

func createCompatPatchError(id string, fields []string, err error) error {
	kind := backend.KindInternal
	var backendErr *backend.BackendError
	if errors.As(err, &backendErr) {
		kind = backendErr.Kind
	}
	return backend.NewBackendError(
		kind,
		"Create",
		fmt.Sprintf("issue %s was created, but setting %s failed", id, strings.Join(fields, ", ")),
		err,
	)
}
