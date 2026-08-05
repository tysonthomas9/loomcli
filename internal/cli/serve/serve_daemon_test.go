package serve

import (
	"testing"
)

func TestServeFlagsHaveNoSupervisorBypass(t *testing.T) {
	t.Parallel()
	f := serveCmd.Flags().Lookup("no-daemon")
	if f != nil {
		t.Fatal("retired --no-daemon supervisor bypass is still registered")
	}
}
