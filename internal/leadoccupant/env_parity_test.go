package leadoccupant_test

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/leadoccupant"
	"github.com/tysonthomas9/loomcli/internal/placement"
)

func TestOccupantTokenEnvMatchesPlacementInjection(t *testing.T) {
	if leadoccupant.EnvOccupantToken != placement.OccupantTokenEnv {
		t.Fatalf("leadoccupant token env = %q, placement injection env = %q", leadoccupant.EnvOccupantToken, placement.OccupantTokenEnv)
	}
}
