package main

import (
	"context"
	"fmt"
	"os"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/data"

	// Sub-package registrations — each package's init() calls cli.RegisterCommand().
	_ "github.com/tysonthomas9/loomcli/internal/cli/agent"
	_ "github.com/tysonthomas9/loomcli/internal/cli/agent/lead"
	_ "github.com/tysonthomas9/loomcli/internal/cli/agentdef"
	_ "github.com/tysonthomas9/loomcli/internal/cli/automode"
	_ "github.com/tysonthomas9/loomcli/internal/cli/backends"
	_ "github.com/tysonthomas9/loomcli/internal/cli/cleanup"
	_ "github.com/tysonthomas9/loomcli/internal/cli/connector"
	_ "github.com/tysonthomas9/loomcli/internal/cli/daemon"
	_ "github.com/tysonthomas9/loomcli/internal/cli/doctor"
	_ "github.com/tysonthomas9/loomcli/internal/cli/driver"
	_ "github.com/tysonthomas9/loomcli/internal/cli/epic"
	_ "github.com/tysonthomas9/loomcli/internal/cli/git"
	_ "github.com/tysonthomas9/loomcli/internal/cli/hooks"
	_ "github.com/tysonthomas9/loomcli/internal/cli/local"
	_ "github.com/tysonthomas9/loomcli/internal/cli/monitor"
	_ "github.com/tysonthomas9/loomcli/internal/cli/repo"
	_ "github.com/tysonthomas9/loomcli/internal/cli/role"
	_ "github.com/tysonthomas9/loomcli/internal/cli/serve"
	_ "github.com/tysonthomas9/loomcli/internal/cli/serve/install"
	_ "github.com/tysonthomas9/loomcli/internal/cli/serve/logroutercmd"
	_ "github.com/tysonthomas9/loomcli/internal/cli/serve/worker"
	_ "github.com/tysonthomas9/loomcli/internal/cli/stack"
	_ "github.com/tysonthomas9/loomcli/internal/cli/teamtemplatecmd"
	_ "github.com/tysonthomas9/loomcli/internal/cli/trigger"
	_ "github.com/tysonthomas9/loomcli/internal/cli/workflow"
	_ "github.com/tysonthomas9/loomcli/internal/cli/workspace"
)

// cli/data is sdk-only and cannot import internal/cli, so it cannot register
// itself via init() like other sub-packages. Instead it exports Commands()
// which we register explicitly from main.
func init() {
	data.SetLocalIssueBackendProvider(func(_ context.Context) backend.IssueBackend {
		return cli.DefaultIssueBackend()
	})
	for _, c := range data.Commands() {
		cli.RegisterCommand(c)
	}
}

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
