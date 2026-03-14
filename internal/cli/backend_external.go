package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

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
	env := append(FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
	if agentName != "" {
		env = append(env, "BD_ACTOR="+agentName)
	}
	cmd.Env = env

	// Pass prompt via stdin pipe (not CLI args) to avoid exposure in process listings.
	// MultiReader delivers the prompt first, then falls through to os.Stdin
	// for interactive terminal input.
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %w", err)
	}
	if _, err := io.WriteString(w, prompt); err != nil {
		w.Close()
		r.Close()
		return fmt.Errorf("failed to write prompt to stdin: %w", err)
	}
	w.Close()
	cmd.Stdin = io.MultiReader(r, os.Stdin)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	r.Close()
	return err
}

func (e *ExternalBackend) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
	cmd := exec.Command(e.binPath, "invoke", "--non-interactive")
	cmd.Dir = workDir
	env := append(FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
	if agentName != "" {
		env = append(env, "BD_ACTOR="+agentName)
	}
	cmd.Env = env

	// Pass prompt via stdin pipe
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %w", err)
	}
	if _, err := io.WriteString(w, prompt); err != nil {
		w.Close()
		r.Close()
		return fmt.Errorf("failed to write prompt to stdin: %w", err)
	}
	w.Close()
	cmd.Stdin = r

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.Close()
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		r.Close()
		return fmt.Errorf("failed to start %s: %w", e.binPath, err)
	}

	// Monitor for shutdown signal
	guard := newProcessGuard(cmd.Process)
	go func() {
		select {
		case <-shutdown:
			guard.Signal(syscall.SIGTERM)
		case <-guard.Done():
		}
	}()

	// Forward stdout line-by-line
	scanner := bufio.NewScanner(stdout)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}

	runErr := cmd.Wait()
	guard.WaitAndMark()
	r.Close()
	return runErr
}

// Meta returns descriptive metadata about the external backend by invoking
// the "meta --json" subcommand. Returns a zero-value BackendMeta if the
// subcommand fails or is not implemented.
func (e *ExternalBackend) Meta() BackendMeta {
	ctx, cancel := context.WithTimeout(context.Background(), externalCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.binPath, "meta", "--json")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil && !(errors.Is(err, exec.ErrWaitDelay) && len(out) > 0) {
		return BackendMeta{
			DisplayName: e.name,
			BinaryName:  filepath.Base(e.binPath),
		}
	}
	var meta BackendMeta
	if err := json.Unmarshal(out, &meta); err != nil {
		return BackendMeta{
			DisplayName: e.name,
			BinaryName:  filepath.Base(e.binPath),
		}
	}
	return meta
}

// HealthCheck reports the installation and readiness status of the external
// backend by invoking the "health --json" subcommand. Returns an unhealthy
// status if the subcommand fails or is not implemented.
func (e *ExternalBackend) HealthCheck() HealthStatus {
	if _, err := os.Stat(e.binPath); err != nil {
		return HealthStatus{
			Installed: false,
			Healthy:   false,
			Message:   fmt.Sprintf("binary no longer found at %s", e.binPath),
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), externalCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.binPath, "health", "--json")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil && !(errors.Is(err, exec.ErrWaitDelay) && len(out) > 0) {
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
	dirs := filepath.SplitList(pathEnv)

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // permission denied, non-existent, etc.
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			name := entry.Name()
			if !strings.HasPrefix(name, "loom-backend-") {
				continue
			}

			backendName := strings.TrimPrefix(name, "loom-backend-")
			if backendName == "" {
				continue
			}

			// First PATH entry wins for duplicate names
			if seen[backendName] {
				continue
			}

			// Built-in backends take priority
			if IsRegistered(backendName) {
				seen[backendName] = true
				continue
			}

			absPath := filepath.Join(dir, name)

			// Verify file is executable
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.Mode()&0111 == 0 {
				continue
			}

			seen[backendName] = true
			RegisterBackend(&ExternalBackend{
				name:    backendName,
				binPath: absPath,
			})

			if os.Getenv("LOOM_DEBUG") != "" {
				fmt.Fprintf(os.Stderr, "[debug] Discovered external backend %q at %s\n", backendName, absPath)
			}
		}
	}
}

func init() {
	if os.Getenv("LOOM_NO_EXTERNAL_BACKENDS") != "" {
		return
	}
	DiscoverExternalBackends()
}
