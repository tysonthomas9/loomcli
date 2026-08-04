package local

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// The local runtime re-launches this same loom binary as child processes:
// `loom serve`, `loom daemon` (the agent supervisor), and `loom local service`.
// Each resolves the binary via os.Executable(). Under `go test`, os.Executable()
// is the package test binary (local.test), so spawning it as e.g.
// `local.test daemon` re-runs the ENTIRE local test suite, which re-enters the
// spawn path and forks exponentially until the host dies — a fork bomb. The
// supervisor package hit the same trap (see its loomExecutablePath seam +
// TestMain stub in internal/cli/daemon/supervisor); the local package never had
// the equivalent guard.
//
// loomReexecCommand / loomReexecCommandContext are the single choke point for
// every loom self-re-exec in this package. They refuse to spawn a *.test
// binary, converting a host-crashing fork bomb into a fast, local error. In
// production the executable is the real loom binary and the guard is a no-op.
// The guard is caller- and package-independent (it inspects the path at spawn
// time), so it also protects any future test that reaches these paths.

func loomReexecCommand(exe string, args ...string) (*exec.Cmd, error) {
	if err := guardLoomReexec(exe); err != nil {
		return nil, err
	}
	return exec.Command(exe, args...), nil //nolint:gosec // G204: intentional loom self-launch; exe guarded above
}

func loomReexecCommandContext(ctx context.Context, exe string, args ...string) (*exec.Cmd, error) {
	if err := guardLoomReexec(exe); err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, exe, args...), nil //nolint:gosec // G204: intentional loom self-launch; exe guarded above
}

func guardLoomReexec(exe string) error {
	if strings.HasSuffix(filepath.Base(exe), ".test") {
		return fmt.Errorf("refusing to re-exec test binary %q as a loom child process (fork-bomb guard)", exe)
	}
	return nil
}
