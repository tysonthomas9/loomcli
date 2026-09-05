package skillse2e_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

func TestMain(m *testing.M) {
	result := m.Run()
	if output := os.Getenv("E2E_COVERAGE_OUTPUT"); output != "" {
		if err := registry.WriteCoverageFile(output); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "write E2E coverage: %v\n", err)
			result = 1
		}
	}
	os.Exit(result)
}
