// Package workspaceerrors gives workspace creation a typed failure vocabulary.
//
// New builds a CreateError pairing a machine-readable Code — AlreadyExists,
// PathNotFound, NotGitRepo, GitFailed, ConfigFailed, or SecurityViolation —
// with a message and the wrapped cause. Callers switch on the Code to decide
// whether a failure is the user's to fix, retryable, or a hard stop, instead
// of matching on error text.
//
// It is a leaf package so both the CLI and the web layer can classify the same
// failure identically without either depending on the other.
package workspaceerrors
