package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
)

const signalHandlerHelperEnv = "LOOM_TEST_ROOT_SIGNAL_CASE"

// signalCases send SIGTERM to a command running under Execute. A command that
// takes the signal must get to finish its own shutdown, whichever shape its
// handler has; one without a handler still dies by the default action.
var signalCases = map[string]func(){
	// serve: the channel stays registered.
	"stays-registered": func() {
		c := make(chan os.Signal, 1)
		cmdstore.Notify(c, syscall.SIGTERM)
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-c
	},
	// daemon, worker, automode, epic: Stop right after the first signal.
	"stops-after-first": func() {
		c := make(chan os.Signal, 1)
		cmdstore.Notify(c, syscall.SIGTERM)
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-c
		signal.Stop(c)
	},
	// Every cmdstore.WithStore command.
	"signal-context": func() {
		ctx, stop := cmdstore.SignalContext()
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		<-ctx.Done()
		stop()
	},
	"no-handler": func() {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	},
}

func TestRootSignalHandlerLeavesTakenSignalsToTheCommand(t *testing.T) {
	if name := os.Getenv(signalHandlerHelperEnv); name != "" {
		rootCmd.AddCommand(&cobra.Command{Use: "signal-test", PersistentPreRun: func(*cobra.Command, []string) {},
			RunE: func(*cobra.Command, []string) error {
				signalCases[name]()
				time.Sleep(300 * time.Millisecond) // time for the root handler to act
				return nil
			}})
		os.Args = []string{os.Args[0], "signal-test"}
		if err := Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		return
	}

	// 20 runs per case: the re-raise race this guards against was intermittent.
	for name := range signalCases {
		for i := range 20 {
			t.Run(fmt.Sprintf("%s/%d", name, i), func(t *testing.T) {
				t.Parallel()
				cmd := exec.Command(os.Args[0], "-test.run=^TestRootSignalHandlerLeavesTakenSignalsToTheCommand$") //nolint:norawexec // subprocess receives a real SIGTERM
				cmd.Env = append(os.Environ(), signalHandlerHelperEnv+"="+name)
				out, err := cmd.CombinedOutput()
				var exitErr *exec.ExitError
				killed := errors.As(err, &exitErr) && exitErr.Sys().(syscall.WaitStatus).Signal() == syscall.SIGTERM
				if name == "no-handler" {
					if !killed {
						t.Fatalf("command without a handler survived SIGTERM: %v\n%s", err, out)
					}
				} else if err != nil {
					t.Fatalf("command that took SIGTERM did not shut down cleanly: %v\n%s", err, out)
				}
			})
		}
	}
}
