// Package sandbox holds the OpenShell sandbox mechanics shared by the one-shot
// `loom task --sandbox` path and the daemon-mode `execution: sandbox` strategy.
//
// These helpers encode the OpenShell v0.0.53 flow proven by the one-shot work
// (F1/F2/F3): create with a trivial command (`-- true`) so create returns
// instead of attaching an interactive shell; upload the loom binary and the
// bootstrap script separately (v0.0.53 has no `--upload` on create); and run the
// bootstrap by path (`exec -- sh <path>`) because exec rejects newline args.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// HostGateway returns the address a sandbox container uses to reach a service on
// the host, driver-aware. Override with LOOM_SANDBOX_HOST_GATEWAY; otherwise
// Podman exposes the host as host.containers.internal and Docker as
// host.docker.internal (driver detected via OPENSHELL_DRIVERS).
func HostGateway() string {
	if v := strings.TrimSpace(os.Getenv("LOOM_SANDBOX_HOST_GATEWAY")); v != "" {
		return v
	}
	if strings.Contains(strings.ToLower(os.Getenv("OPENSHELL_DRIVERS")), "podman") {
		return "host.containers.internal"
	}
	return "host.docker.internal"
}

// LoomPath is where the loom binary is uploaded inside the sandbox. The sandbox
// root is read-write, so no directory needs pre-creating.
const LoomPath = "/sandbox/loom"

// BootstrapPath is where the bootstrap script is uploaded. `openshell sandbox
// exec` rejects newline-bearing arguments, so the multi-line bootstrap is
// uploaded as a file and run as `sh <path>` rather than passed inline.
const BootstrapPath = "/sandbox/bootstrap.sh"

// Config configures OpenShell sandbox execution.
type Config struct {
	Providers []string // credential providers injected into the sandbox (e.g. "claude", "github")
	Network   string   // "open" (default) or a path to a custom OPA/Rego policy YAML
	From      string   // container base image (--from); empty uses the openshell default
	Backend   string   // backend override inside the sandbox; empty inherits the host default
	// LoomBinPath is where loom lives INSIDE the container. Empty means loom is
	// uploaded to LoomPath; non-empty means loom is baked into the --from image
	// at this path (no upload — which is flaky for large binaries — and no chmod,
	// since a baked binary is already executable). Set via LOOM_SANDBOX_LOOM_PATH.
	LoomBinPath string
}

// LoomCmd returns the in-container path to the loom binary: the baked path when
// set, else the uploaded LoomPath.
func (c Config) LoomCmd() string {
	if c.LoomBinPath != "" {
		return c.LoomBinPath
	}
	return LoomPath
}

// UploadsLoom reports whether loom must be uploaded into the sandbox (vs. baked
// into the --from image at LoomBinPath).
func (c Config) UploadsLoom() bool { return c.LoomBinPath == "" }

// DefaultConfig returns the sandbox defaults with env overrides:
//   - LOOM_SANDBOX_POLICY    — path to a custom OPA/Rego policy YAML (else "open").
//   - LOOM_SANDBOX_PROVIDERS — comma-separated providers (empty string disables them).
//   - LOOM_SANDBOX_BACKEND   — backend override inside the sandbox.
func DefaultConfig() Config {
	cfg := Config{Network: "open", Providers: []string{"claude", "github"}}
	if p := strings.TrimSpace(os.Getenv("LOOM_SANDBOX_POLICY")); p != "" {
		cfg.Network = p
	}
	if v, ok := os.LookupEnv("LOOM_SANDBOX_PROVIDERS"); ok {
		cfg.Providers = nil
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cfg.Providers = append(cfg.Providers, p)
			}
		}
	}
	if b := strings.TrimSpace(os.Getenv("LOOM_SANDBOX_BACKEND")); b != "" {
		cfg.Backend = b
	}
	// LOOM_SANDBOX_LOOM_PATH set => loom is baked into the --from image at this
	// path; skip the upload. Empty => upload to LoomPath.
	cfg.LoomBinPath = strings.TrimSpace(os.Getenv("LOOM_SANDBOX_LOOM_PATH"))
	return cfg
}

// BuildCreateArgs builds the `openshell sandbox create` arguments. The loom
// binary and bootstrap are uploaded/exec'd separately afterwards (v0.0.53 has no
// --upload create flag), so create only provisions the sandbox.
func BuildCreateArgs(name string, cfg Config) []string {
	args := []string{"sandbox", "create", "--name", name}
	if cfg.From != "" {
		args = append(args, "--from", cfg.From)
	}
	for _, p := range cfg.Providers {
		args = append(args, "--provider", p)
	}
	if len(cfg.Providers) > 0 {
		// Non-interactive: auto-create missing providers from local credentials
		// rather than prompting (which errors without a TTY).
		args = append(args, "--auto-providers")
	}
	// Only pass --policy for custom networks; the default "open" relies on the
	// sandbox's built-in policy.
	if cfg.Network != "" && cfg.Network != "open" {
		args = append(args, "--policy", cfg.Network)
	}
	// Trailing trivial command: create RUNS it and returns. A command-less create
	// attaches an interactive SSH shell and blocks forever in a non-TTY context.
	// Without --no-keep the sandbox persists for the upload + exec steps.
	args = append(args, "--", "true")
	return args
}

// ResolveLoomBinary returns a loom binary that can run inside the (Linux)
// sandbox. The container is Linux, so a darwin/host binary won't execute (exec
// format error). Prefer an explicit LOOM_SANDBOX_LOOM_BIN; otherwise the running
// binary only when this host is itself Linux.
func ResolveLoomBinary() (string, error) {
	if v := strings.TrimSpace(os.Getenv("LOOM_SANDBOX_LOOM_BIN")); v != "" {
		return v, nil
	}
	if runtime.GOOS == "linux" {
		return os.Executable()
	}
	return "", fmt.Errorf("--sandbox runs a Linux container but this host is %s/%s: set "+
		"LOOM_SANDBOX_LOOM_BIN to a `GOOS=linux GOARCH=%s` loom build (or use a --from image with loom baked in)",
		runtime.GOOS, runtime.GOARCH, runtime.GOARCH)
}

// WriteBootstrapScript writes the bootstrap to a temp file and returns its path
// and a cleanup func. Used because `openshell sandbox exec` rejects newline args,
// so the multi-line bootstrap is uploaded and run by path.
func WriteBootstrapScript(script string) (string, func(), error) {
	f, err := os.CreateTemp("", "loom-sandbox-bootstrap-*.sh")
	if err != nil {
		return "", func() {}, fmt.Errorf("write bootstrap script: %w", err)
	}
	name := f.Name()
	cleanup := func() { _ = os.Remove(name) }
	if _, err := f.WriteString(script); err != nil {
		_ = f.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("write bootstrap script: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("write bootstrap script: %w", err)
	}
	return name, cleanup, nil
}

// OpenshellBinary returns the openshell CLI binary name.
func OpenshellBinary() string { return "openshell" }

// RunOpenshell runs an openshell subcommand with inherited stdio and waits.
func RunOpenshell(args []string) error {
	cmd := exec.Command(OpenshellBinary(), args...) //nolint:gosec // args built internally
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// RunOpenshellExit runs an openshell subcommand and returns its exit code (0/nil
// on success; the remote exit code with a nil error on a clean non-zero exit).
func RunOpenshellExit(args []string) (int, error) {
	cmd := exec.Command(OpenshellBinary(), args...) //nolint:gosec // args built internally
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return 0, fmt.Errorf("openshell exec: %w", err)
	}
	return 0, nil
}

// DeleteSandbox runs `openshell sandbox delete <name>` with a 30s timeout.
// Best-effort: errors are logged, not returned.
func DeleteSandbox(name string) {
	if name == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, OpenshellBinary(), "sandbox", "delete", name) //nolint:gosec // name is internally generated
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Warn("sandbox delete failed", "name", name, "output", string(out), "err", err)
	}
}

// ShellQuote wraps s in single quotes for safe use inside a /bin/sh -c string,
// escaping any embedded single quotes.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
