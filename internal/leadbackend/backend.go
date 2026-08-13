// Package leadbackend composes the sandbox occupant environment with the
// allowlisted lead data API backend.
package leadbackend

import (
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/api"
	"github.com/tysonthomas9/loomcli/internal/leadoccupant"
)

const unauthorizedMessage = "occupant token expired; the lead runtime is not refreshing it — restart the lead"

// New returns the occupant backend when the sandbox credential is present.
// An incomplete occupant environment fails closed rather than falling through
// to ordinary backend discovery or interactive authentication.
func New() (backend.IssueBackend, error) {
	env, state := leadoccupant.FromEnv()
	switch state {
	case leadoccupant.StateAbsent:
		return nil, nil
	case leadoccupant.StatePartial:
		return nil, errors.New("occupant environment incomplete: LOOM_LEAD_OCCUPANT_TOKEN is set but LOOM_LEAD_API_URL/LOOM_WORKSPACE is missing")
	case leadoccupant.StateComplete:
		return api.New(api.Config{
			BaseURL:             env.BaseURL,
			WorkspaceID:         env.Workspace,
			PathPrefix:          leadoccupant.DataPathPrefix,
			UnauthorizedMessage: unauthorizedMessage,
			HTTPClient:          api.NewAuthHTTPClient(env.Transport()),
		})
	default:
		return nil, fmt.Errorf("unknown occupant environment state %d", state)
	}
}
