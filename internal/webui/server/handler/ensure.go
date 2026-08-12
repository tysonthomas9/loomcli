package handler

import (
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// WriteExistingIfFound is the fast idempotent path for POST-create handlers: if
// fetch reports an already-provisioned resource it writes 200 with that resource
// and returns true (the caller should then return), otherwise it writes nothing
// and returns false so the caller proceeds to create. The bool that fetch
// returns is the "found" signal, so a nil-but-no-error lookup is never emitted as
// "200 null".
func WriteExistingIfFound[T any](w http.ResponseWriter, fetch func() (T, bool)) bool {
	if existing, ok := fetch(); ok {
		WriteJSON(w, http.StatusOK, existing)
		return true
	}
	return false
}

// WriteCreatedOrExisting writes the terminal response for an idempotent
// POST-create: 201 with created on success; on a create race
// (ErrAlreadyExists/ErrConflict, whichever a backend reports for a duplicate) it
// re-fetches via fetch and returns the winner (200); otherwise
// WriteDomainError(failMsg). Pairs with WriteExistingIfFound (same fetch closure)
// so every create handler shares one race-recovery path and one sentinel set,
// while keeping its own pre-create steps and their error handling.
func WriteCreatedOrExisting[T any](w http.ResponseWriter, created T, err error, fetch func() (T, bool), failMsg string) {
	if err == nil {
		WriteJSON(w, http.StatusCreated, created)
		return
	}
	if errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrConflict) {
		if existing, ok := fetch(); ok {
			WriteJSON(w, http.StatusOK, existing)
			return
		}
	}
	WriteDomainError(w, err, failMsg)
}
