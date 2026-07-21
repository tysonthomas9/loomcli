package daemon

import (
	"fmt"
	"io"
	"os"
)

// requireTestSupport gates the seed-* command family (docs/adr/0001): the
// seeding seam ships inside the production binary, so this env check — plus
// Hidden on every command — is the boundary that keeps test-only surface out
// of production use. Callers must invoke it first in RunE.
func requireTestSupport() error {
	if os.Getenv("LOOM_TESTSUPPORT") != "1" {
		return fmt.Errorf("seed commands are test support: set LOOM_TESTSUPPORT=1 to enable")
	}
	return nil
}

// readSeedContent reads seed payload from path, or stdin when path is empty
// or "-". Shared by the seed-* commands.
func readSeedContent(path string) ([]byte, error) {
	if path == "" || path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path) //nolint:gosec // G304: test-only CLI flag
}
