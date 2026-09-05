package backends

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/harness"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// externalCmdTimeout is the maximum time allowed for external backend
// meta and health check commands to complete.
const externalCmdTimeout = 5 * time.Second

// ExternalBackend implements the Backend interface by delegating to an external
// binary discovered on PATH matching the loom-backend-* naming convention.
// This follows the plugin pattern used by Git (git-credential-*), kubectl
// (kubectl-*), and Docker (docker-*).
type ExternalBackend struct {
	name    string // extracted backend name (e.g., "aider" from "loom-backend-aider")
	binPath string // absolute path to the discovered executable
}

func (e *ExternalBackend) Name() string { return e.name }

func (e *ExternalBackend) InvokeInteractive(workDir, prompt, agentName string) error {
	cmd := exec.Command(e.binPath, "invoke", "--interactive")
	cmd.Dir = workDir
	cmd.Env = buildBackendEnv(workDir, agentName)

	// Pass prompt via stdin pipe (not CLI args) to avoid exposure in process listings.
	// MultiReader delivers the prompt first, then falls through to os.Stdin
	// for interactive terminal input.
	r := pipePromptToCmd(cmd, prompt)
	cmd.Stdin = io.MultiReader(r, os.Stdin)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	r.Close()
	return err
}

func (e *ExternalBackend) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
	return runHarness(context.Background(), shutdown, harnessInvocation{
		BinaryName:  e.binPath,
		Args:        []string{"invoke", "--non-interactive"},
		WorkDir:     workDir,
		Env:         buildBackendEnv(workDir, agentName),
		Prompt:      prompt,
		HarnessName: "", // unknown upstream; generic classifier
		LineHandler: func(line string) { fmt.Println(line) },
		RetryPolicy: harness.DefaultRetryPolicy(),
	})
}

// probeSubcommandJSON runs `<binPath> <sub> --json` with a 1s WaitDelay,
// tolerating a WaitDelay-killed process that still produced output (so a
// well-behaved plugin that prints then lingers is not treated as a failure). It
// returns the captured stdout; a non-nil error means the subcommand failed
// without usable output. Callers unmarshal and build their own fallback — this
// owns only the fiddly exec + ErrWaitDelay tolerance the probes shared.
func probeSubcommandJSON(ctx context.Context, binPath, sub string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, externalCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath, sub, "--json")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil && !(errors.Is(err, exec.ErrWaitDelay) && len(out) > 0) {
		return nil, err
	}
	return out, nil
}

// Meta returns descriptive metadata about the external backend by invoking
// the "meta --json" subcommand. Returns a zero-value BackendMeta if the
// subcommand fails or is not implemented.
func (e *ExternalBackend) Meta() BackendMeta {
	fallback := BackendMeta{DisplayName: e.name, BinaryName: filepath.Base(e.binPath)}
	out, err := probeSubcommandJSON(context.Background(), e.binPath, "meta")
	if err != nil {
		return fallback
	}
	var meta BackendMeta
	if err := json.Unmarshal(out, &meta); err != nil {
		return fallback
	}
	return meta
}

// HealthCheck reports the installation and readiness status of the external
// backend by invoking the "health --json" subcommand. Returns an unhealthy
// status if the subcommand fails or is not implemented.
func (e *ExternalBackend) HealthCheck() HealthStatus {
	return e.healthCheck(context.Background(), true)
}

// HealthCheckForAdmission reports readiness using the caller's launch context.
func (e *ExternalBackend) HealthCheckForAdmission(ctx context.Context) HealthStatus {
	return e.healthCheck(ctx, false)
}

func (e *ExternalBackend) healthCheck(ctx context.Context, includeVersion bool) HealthStatus {
	if _, err := os.Stat(e.binPath); err != nil {
		return HealthStatus{
			Installed: false,
			Healthy:   false,
			Message:   fmt.Sprintf("binary no longer found at %s", e.binPath),
		}
	}
	out, err := probeSubcommandJSON(ctx, e.binPath, "health")
	if err != nil {
		return HealthStatus{
			Installed: true, // the binary was found on PATH during discovery
			Healthy:   false,
			Message:   fmt.Sprintf("health check failed: %v", err),
		}
	}
	var hs HealthStatus
	if err := json.Unmarshal(out, &hs); err != nil {
		return HealthStatus{
			Installed: true,
			Healthy:   false,
			Message:   fmt.Sprintf("health check returned invalid JSON: %v", err),
		}
	}
	if !includeVersion {
		hs.Version = ""
	}
	return hs
}

// DiscoverExternalBackends scans $PATH for executables matching the
// loom-backend-* naming convention and registers each as an ExternalBackend.
// Discovery errors are non-fatal — broken plugins on PATH do not prevent loom
// from starting. Built-in backends take priority: if a name is already
// registered, the external backend is skipped.
func DiscoverExternalBackends() {
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		return
	}

	seen := make(map[string]bool)
	for _, dir := range filepath.SplitList(pathEnv) {
		discoverBackendsInDir(dir, seen)
	}
}

// discoverBackendsInDir scans a single directory for loom-backend-* executables.
func discoverBackendsInDir(dir string, seen map[string]bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // permission denied, non-existent, etc.
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "loom-backend-") {
			continue
		}
		backendName := strings.TrimPrefix(entry.Name(), "loom-backend-")
		if backendName == "" || seen[backendName] {
			continue
		}
		// Built-in backends take priority
		if cli.IsRegistered(backendName) {
			seen[backendName] = true
			continue
		}
		absPath := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil || info.Mode()&0111 == 0 {
			continue
		}
		seen[backendName] = true
		cli.RegisterBackend(&ExternalBackend{name: backendName, binPath: absPath})
		if os.Getenv("LOOM_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "[debug] Discovered external backend %q at %s\n", backendName, absPath)
		}
	}
}

func init() {
	if os.Getenv("LOOM_NO_EXTERNAL_BACKENDS") != "" {
		return
	}
	DiscoverExternalBackends()
}
