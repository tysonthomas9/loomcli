package fleet

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

const unsupportedCreateExternalRefMessage = `unknown field "external_ref"`

func isCreateExternalRefUnsupported(err error) bool {
	var backendErr *backend.BackendError
	return errors.As(err, &backendErr) &&
		backendErr.Kind == backend.KindValidation &&
		backendErr.Message == unsupportedCreateExternalRefMessage
}

func (b *FleetBackend) createWithoutExternalRef(
	ctx context.Context,
	params backend.CreateParams,
) (*backend.IssueData, error) {
	retryParams := params
	retryParams.ExternalRef = ""
	retryKey, err := retryParams.FleetCreateIdempotencyKey(time.Now())
	if err != nil {
		return nil, backend.ErrInternal("Create", "derive compatibility idempotency key", err)
	}
	retryParams.IdempotencyKey = retryKey
	result, err := b.createIssueOnce(ctx, retryParams)
	if err != nil {
		return nil, err
	}

	externalRef := params.ExternalRef
	if err := b.Update(ctx, result.ID, backend.UpdateParams{ExternalRef: &externalRef}); err != nil {
		// The issue itself was created; return it alongside the classified
		// error so callers that inspect the partial result can still see the ID.
		return result, createExternalRefPatchError(result.ID, err)
	}
	result.ExternalRef = externalRef
	return result, nil
}

func createExternalRefPatchError(id string, err error) error {
	kind := backend.KindInternal
	var backendErr *backend.BackendError
	if errors.As(err, &backendErr) {
		kind = backendErr.Kind
	}
	return backend.NewBackendError(
		kind,
		"Create",
		fmt.Sprintf("issue %s was created, but setting external_ref failed", id),
		err,
	)
}

const unsupportedCreateAcceptanceCriteriaMessage = `unknown field "acceptance_criteria"`

// isCreateAcceptanceCriteriaUnsupported reports whether err is the strict-decode
// rejection a fleet-db whose CreateIssueRequest predates acceptance_criteria
// returns. Such a server 400s the whole body, so the issue is not created at all
// — the retry below re-creates it without the field.
func isCreateAcceptanceCriteriaUnsupported(err error) bool {
	var backendErr *backend.BackendError
	return errors.As(err, &backendErr) &&
		backendErr.Kind == backend.KindValidation &&
		backendErr.Message == unsupportedCreateAcceptanceCriteriaMessage
}

// createWithoutAcceptanceCriteria retries the create with the field stripped,
// then attempts to apply it by PATCH. Unlike the external_ref fallback, the
// PATCH is expected to fail on exactly the servers that need this retry — a
// fleet-db that rejects acceptance_criteria on create rejects it on PATCH too —
// so its failure is reported as a descriptive error carrying the created issue
// rather than being swallowed.
func (b *FleetBackend) createWithoutAcceptanceCriteria(
	ctx context.Context,
	params backend.CreateParams,
) (*backend.IssueData, error) {
	retryParams := params
	retryParams.AcceptanceCriteria = ""
	retryKey, err := retryParams.FleetCreateIdempotencyKey(time.Now())
	if err != nil {
		return nil, backend.ErrInternal("Create", "derive compatibility idempotency key", err)
	}
	retryParams.IdempotencyKey = retryKey
	result, err := b.createIssueOnce(ctx, retryParams)
	if err != nil {
		return nil, err
	}

	acceptance := params.AcceptanceCriteria
	if err := b.Update(ctx, result.ID, backend.UpdateParams{AcceptanceCriteria: &acceptance}); err != nil {
		// The issue itself was created; return it alongside the classified
		// error so callers that inspect the partial result can still see the ID.
		return result, createAcceptanceCriteriaPatchError(result.ID, err)
	}
	// backend.IssueData is the slim projection and carries no acceptance
	// criteria field, so there is nothing to echo back here — the value is
	// persisted and comes back on the next Get.
	return result, nil
}

func createAcceptanceCriteriaPatchError(id string, err error) error {
	kind := backend.KindInternal
	var backendErr *backend.BackendError
	if errors.As(err, &backendErr) {
		kind = backendErr.Kind
	}
	return backend.NewBackendError(
		kind,
		"Create",
		fmt.Sprintf(
			"issue %s was created, but this fleet-db does not accept acceptance_criteria (needs fleet-db PR #244)",
			id,
		),
		err,
	)
}
