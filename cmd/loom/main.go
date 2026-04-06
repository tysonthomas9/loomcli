package main

import (
	"fmt"
	"os"

	"github.com/tysonthomas9/loomcli/internal/cli"

	// Sub-package registrations — each package's init() calls cli.RegisterCommand().
	_ "github.com/tysonthomas9/loomcli/internal/cli/agent"
	_ "github.com/tysonthomas9/loomcli/internal/cli/automode"
	_ "github.com/tysonthomas9/loomcli/internal/cli/backends"
	_ "github.com/tysonthomas9/loomcli/internal/cli/cleanup"
	_ "github.com/tysonthomas9/loomcli/internal/cli/daemon"
	_ "github.com/tysonthomas9/loomcli/internal/cli/doctor"
	_ "github.com/tysonthomas9/loomcli/internal/cli/git"
	_ "github.com/tysonthomas9/loomcli/internal/cli/hooks"
	_ "github.com/tysonthomas9/loomcli/internal/cli/migrate"
	_ "github.com/tysonthomas9/loomcli/internal/cli/monitor"
	_ "github.com/tysonthomas9/loomcli/internal/cli/serve"
	_ "github.com/tysonthomas9/loomcli/internal/cli/workspace"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
