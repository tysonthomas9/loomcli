package stackpublish

import (
	"errors"
	"net/http"
)

// ErrNotFound marks a provider answer of 404, such as a PR that was deleted
// or transferred. The provider error text is unchanged.
var ErrNotFound = errors.New("not found on provider")

type notFoundError struct{ error }

func (e notFoundError) Unwrap() []error { return []error{e.error, ErrNotFound} }

func markNotFound(status int, err error) error {
	if status == http.StatusNotFound {
		return notFoundError{err}
	}
	return err
}
