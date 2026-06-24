package supervisor

import (
	"os"
	"testing"
)

// TestMain redirects loomExecutablePath away from os.Executable() for the
// whole package. Under `go test`, os.Executable() is the supervisor.test
// binary itself; any test that reaches a real spawn (spawnAgent /
// superviseAgent via Start) would exec the test binary as the "agent",
// which re-runs the entire suite and recursively spawns more agents — a
// fork bomb that exhausts the host (observed: >2000 pids within seconds).
//
// /bin/false exists on both Linux and macOS, execs successfully, and exits
// non-zero immediately, so spawn paths still exercise the full exec +
// supervise machinery without recursion.
//
// TestHelperProcess self-execs are unaffected: they invoke os.Args[0]
// directly with -test.run=TestHelperProcess and an env-var mode guard.
func TestMain(m *testing.M) {
	loomExecutablePath = func() (string, error) { return "/bin/false", nil }
	os.Exit(m.Run())
}
