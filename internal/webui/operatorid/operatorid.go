// Package operatorid resolves the operator identity that webui-originated
// issue writes are attributed to.
//
// It is the single home for the LOOM_OPERATOR_ACTOR lookup so the webui
// handlers and `loom doctor` cannot drift apart about who the operator is.
package operatorid

import (
	"os"
	"strings"
)

const (
	// EnvOperatorActor overrides the operator identity used for webui issue
	// writes in open mode (no verified session).
	EnvOperatorActor = "LOOM_OPERATOR_ACTOR"

	// DefaultOperatorActor is the identity used when the environment does
	// not name one.
	DefaultOperatorActor = "operator@local"
)

// Resolve returns the configured operator actor, falling back to
// DefaultOperatorActor when the environment variable is unset or blank.
func Resolve() string {
	if actor := strings.TrimSpace(os.Getenv(EnvOperatorActor)); actor != "" {
		return actor
	}
	return DefaultOperatorActor
}
