package main

import (
	"fmt"
	"os"

	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

func main() {
	if err := registry.WriteYAML(os.Stdout, registry.Scenarios); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "generate E2E coverage: %v\n", err)
		os.Exit(1)
	}
}
