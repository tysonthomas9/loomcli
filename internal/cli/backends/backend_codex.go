package backends

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/harness"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// CodexBackend implements the Backend interface for the OpenAI Codex CLI.
type CodexBackend struct{}

func (c *CodexBackend) Name() string { return NameCodex }

var codexProviderMetadata backendProviderMetadataCapture

func (c *CodexBackend) InvokeInteractive(workDir, prompt, agentName string) error {
	return codexInvoker(workDir, prompt, agentName)
}

func (c *CodexBackend) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	return codexNonInteractiveInvoker(workDir, prompt, agentName, shutdown, collector)
}

func (c *CodexBackend) LastSessionID(_ string) string {
	return codexProviderMetadata.LastSessionID()
}

func (c *CodexBackend) LastProviderMetadata(_ string) map[string]any {
	return codexProviderMetadata.Metadata()
}

// codexInvoker is the function used to invoke Codex interactively (mockable for tests)
var codexInvoker = defaultCodexInvoker

// codexNonInteractiveInvoker is the function used for non-interactive Codex invocation (mockable for tests)
var codexNonInteractiveInvoker func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error = defaultCodexNonInteractiveInvoker

// buildCodexInteractiveCmd constructs the exec.Cmd for interactive Codex invocation.
// Extracted for testability — callers can inspect the returned cmd without execution.
func buildCodexInteractiveCmd(workDir, prompt, agentName string) *exec.Cmd {
	cmd := exec.Command("codex", "--no-alt-screen", "--dangerously-bypass-approvals-and-sandbox", prompt)
	cmd.Dir = workDir
	cmd.Env = buildBackendEnv(workDir, agentName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

// isTerminal reports whether f is connected to a terminal (TTY)
// using the TIOCGWINSZ ioctl, which only succeeds on real terminals.
func isTerminal(f *os.File) bool {
	// struct winsize: rows, cols, xpixel, ypixel
	var ws [4]uint16
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws[0]))) //nolint:gosec // G103: deliberate unsafe for TIOCGWINSZ ioctl
	return err == 0
}

func defaultCodexInvoker(workDir, prompt, agentName string) error {
	// When stdin is not a TTY (e.g. daemon subprocess), Codex interactive
	// mode fails with "stdin is not a terminal". Fall back to non-interactive
	// exec mode which works headlessly.
	if !isTerminal(os.Stdin) {
		shutdown := make(chan struct{})
		return defaultCodexNonInteractiveInvoker(workDir, prompt, agentName, shutdown, nil)
	}

	cmd := buildCodexInteractiveCmd(workDir, prompt, agentName)

	fmt.Println("Launching Codex agent...")
	fmt.Println("")

	return cmd.Run()
}

func defaultCodexNonInteractiveInvoker(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	fmt.Println("Launching Codex agent (non-interactive)...")
	fmt.Println("")
	codexProviderMetadata.Clear(NameCodex)

	return runHarness(context.Background(), shutdown, harnessInvocation{
		BinaryName:  "codex",
		Args:        buildCodexNonInteractiveArgs(prompt),
		WorkDir:     workDir,
		Env:         buildBackendEnv(workDir, agentName),
		HarnessName: "codex",
		LineHandler: func(line string) {
			codexProviderMetadata.IngestLine(line)
			fmt.Println(line)
			if collector != nil {
				collectCodexStreamUsage(line, collector)
			}
		},
		RetryPolicy: harness.DefaultRetryPolicy(),
	})
}

func buildCodexNonInteractiveArgs(prompt string) []string {
	args := []string{"exec", "--json", "--dangerously-bypass-approvals-and-sandbox"}
	if prompt != "" {
		// The wrapper runs subprocesses under a PTY. Codex treats PTY stdin as
		// interactive input and requires the initial exec prompt as argv.
		args = append(args, prompt)
	}
	return args
}

// buildBackendEnv constructs the standard environment for backend subprocess invocations.
func buildBackendEnv(workDir, agentName string) []string {
	env := appendLoomExecutableDirToPath(cli.FilteredEnv())
	env = append(env, "LOOM_WORKTREE_PATH="+workDir)
	if agentName != "" {
		env = append(env, "LOOM_AGENT_NAME="+agentName)
	}
	env = append(env, activeSessionEnvVars()...)
	return env
}

func appendLoomExecutableDirToPath(env []string) []string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return env
	}
	dir := filepath.Dir(exe)
	if dir == "." || dir == "" {
		return env
	}

	pathPrefix := "PATH="
	for i, entry := range env {
		if !strings.HasPrefix(entry, pathPrefix) {
			continue
		}
		current := strings.TrimPrefix(entry, pathPrefix)
		if pathContainsDir(current, dir) {
			return env
		}
		if current == "" {
			env[i] = pathPrefix + dir
		} else {
			env[i] = pathPrefix + dir + string(os.PathListSeparator) + current
		}
		return env
	}

	return append([]string{pathPrefix + dir}, env...)
}

func pathContainsDir(pathValue, dir string) bool {
	for _, entry := range filepath.SplitList(pathValue) {
		if entry == dir {
			return true
		}
	}
	return false
}

// pipePromptToCmd attaches the prompt to cmd.Stdin without exposing it in CLI args.
// Do not pre-write to an OS pipe here: large prompts can exceed the pipe buffer
// and deadlock before the child process starts reading.
func pipePromptToCmd(cmd *exec.Cmd, prompt string) io.ReadCloser {
	r := io.NopCloser(strings.NewReader(prompt))
	cmd.Stdin = r
	return r
}

// Meta returns descriptive metadata about the Codex backend.
func (c *CodexBackend) Meta() BackendMeta {
	version := detectBinaryVersion("codex")
	return BackendMeta{
		DisplayName: "Codex",
		Version:     version,
		Description: "OpenAI Codex CLI",
		URL:         "https://github.com/openai/codex",
		BinaryName:  "codex",
	}
}

// HealthCheck reports the installation and readiness status of the Codex backend.
func (c *CodexBackend) HealthCheck() HealthStatus {
	var hs HealthStatus
	var issues []string

	if _, err := exec.LookPath("codex"); err == nil {
		hs.Installed = true
		hs.Version = detectBinaryVersion("codex")
	} else {
		issues = append(issues, "codex binary not found on PATH")
	}

	if os.Getenv("OPENAI_API_KEY") != "" || hasCodexAuthFile() {
		hs.APIKeySet = true
	} else {
		issues = append(issues, "OPENAI_API_KEY not set and codex auth.json not found")
	}

	hs.Healthy = hs.Installed && hs.APIKeySet
	if len(issues) > 0 {
		hs.Message = strings.Join(issues, "; ")
	} else {
		hs.Message = "ready"
	}
	return hs
}

func hasCodexAuthFile() bool {
	path := codexAuthFilePath()
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func codexAuthFilePath() string {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return filepath.Join(home, "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "auth.json")
}

func init() {
	cli.RegisterBackend(&CodexBackend{})
}

// codexUsageEvent is the minimal structure for Codex --json output
// that contains a usage object (emitted on turn.completed events).
type codexUsageEvent struct {
	Type  string `json:"type"`
	Usage *struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage,omitempty"`
}

// collectCodexStreamUsage is best-effort: Codex emits turn.completed events
// with a usage object when running with --json. If the line doesn't contain
// usage data, it's silently ignored.
func collectCodexStreamUsage(line string, collector *usage.Collector) {
	var event codexUsageEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return
	}
	if event.Usage == nil {
		return
	}
	// No message-level dedup needed for Codex (one usage per turn)
	collector.Accumulate("", event.Usage.InputTokens, event.Usage.OutputTokens, 0, 0)
}
