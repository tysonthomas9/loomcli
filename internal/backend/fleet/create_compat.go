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
