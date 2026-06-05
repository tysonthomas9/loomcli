package backends

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/flue"
	"github.com/tysonthomas9/loomcli/internal/harness"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// FlueBackend implements cli.Backend for the flue agent-harness framework.
//
// One-shot agents (`loom plan` / `loom task`) run through `flue run`, which
// loom drives as a subprocess via the shared harness wrapper. On first use
// the flue project (workflow + dependencies) is bootstrapped under
// ~/.loom/flue; see internal/flue. The prompt, worktree path, and model are
// passed as a JSON --payload to the workflow.
type FlueBackend struct{}

func (f *FlueBackend) Name() string { return NameFlue }

func (f *FlueBackend) InvokeInteractive(workDir, prompt, agentName string) error {
	return flueInteractiveInvoker(workDir, prompt, agentName)
}

func (f *FlueBackend) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	return flueNonInteractiveInvoker(workDir, prompt, agentName, shutdown, collector)
}

// InvokeLead implements LeadServerBackend: the lead agent runs against a
// long-lived flue server with multi-turn, resumable conversation, rather than
// the one-shot `flue run` used by InvokeInteractive (plan/task). See flue_lead.go.
func (f *FlueBackend) InvokeLead(workDir, prompt string) error {
	return flueInvokeLead(workDir, prompt)
}

// Mockable seams for tests.
var flueInteractiveInvoker = defaultFlueInteractiveInvoker
var flueNonInteractiveInvoker func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error = defaultFlueNonInteractiveInvoker

const (
	// defaultFlueWorkflow must stay short: when the agent uses the ChatGPT
	// Codex provider, the per-session affinity key derived from the workflow
	// name must fit that backend's 64-char prompt_cache_key cap.
	defaultFlueWorkflow = "agent"
	defaultFlueModel    = "anthropic/claude-sonnet-4-6"
	defaultCodexModel   = "gpt-5.5"
	envFlueWorkflow     = "LOOM_FLUE_WORKFLOW"
	envFlueModel        = "LOOM_FLUE_MODEL"
)

// resolveFlueWorkflow returns the workflow name to run. The daemon/resolver
// sets LOOM_FLUE_WORKFLOW per agent/role (Phase 3); otherwise the embedded
// default workflow is used.
func resolveFlueWorkflow() string {
	if wf := strings.TrimSpace(os.Getenv(envFlueWorkflow)); wf != "" {
		return wf
	}
	return defaultFlueWorkflow
}

// resolveFlueModel mirrors the LOOM_OPENCODE_MODEL precedent, with a codex
// fallback: LOOM_FLUE_MODEL > Anthropic platform key > local codex auth >
// default. Reusing local `codex login` lets codex-authenticated users run the
// flue backend with no separate API key (the embedded app.ts registers the
// matching openai-codex provider).
func resolveFlueModel() string {
	if m := strings.TrimSpace(os.Getenv(envFlueModel)); m != "" {
		return m
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return defaultFlueModel
	}
	if model, ok := codexFlueModel(); ok {
		return "openai-codex/" + model
	}
	return defaultFlueModel
}

// codexFlueModel returns the model to use with local codex (ChatGPT) auth, or
// ("", false) when codex auth is absent. The model name comes from the Codex
// CLI config (~/.codex/config.toml), defaulting to gpt-5.5.
func codexFlueModel() (string, bool) {
	if !hasCodexAuthFile() {
		return "", false
	}
	model := defaultCodexModel
	if path := codexConfigPath(); path != "" {
		if data, err := os.ReadFile(path); err == nil { //nolint:gosec // path under user codex home
			if v := parseCodexConfigModel(string(data)); v != "" {
				model = v
			}
		}
	}
	return model, true
}

// codexConfigPath returns ~/.codex/config.toml (alongside auth.json, honoring
// CODEX_HOME), or "" if the codex home can't be resolved.
func codexConfigPath() string {
	authPath := codexAuthFilePath()
	if authPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(authPath), "config.toml")
}

// parseCodexConfigModel extracts the top-level `model = "..."` value from a
// Codex config.toml, ignoring comments, sections, and other keys such as
// model_reasoning_effort.
func parseCodexConfigModel(data string) string {
	for _, raw := range strings.Split(data, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			break // a [section] begins; the top-level model key precedes it
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 || strings.TrimSpace(line[:eq]) != "model" {
			continue
		}
		return strings.Trim(strings.TrimSpace(line[eq+1:]), `"'`)
	}
	return ""
}

// fluePayloadJSON serializes the workflow payload. Compact JSON keeps the
// --payload argument well within ARG_MAX even for large prompts; args are
// passed via execve directly (no shell), so no escaping is required.
func fluePayloadJSON(workDir, prompt, model string) (string, error) {
	data, err := json.Marshal(struct {
		Prompt string `json:"prompt"`
		CWD    string `json:"cwd"`
		Model  string `json:"model"`
	}{Prompt: prompt, CWD: workDir, Model: model})
	if err != nil {
		return "", fmt.Errorf("flue backend: marshal payload: %w", err)
	}
	return string(data), nil
}

func flueRunArgs(workflow, projectDir, payload string) []string {
	return []string{"run", workflow, "--target", "node", "--root", projectDir, "--payload", payload}
}

func defaultFlueInteractiveInvoker(workDir, prompt, agentName string) error {
	flueBin, projectDir, err := flue.DefaultManager().EnsureProject(context.Background())
	if err != nil {
		return err
	}
	payload, err := fluePayloadJSON(workDir, prompt, resolveFlueModel())
	if err != nil {
		return err
	}

	fmt.Println("Launching flue agent...")
	fmt.Println("")

	cmd := exec.Command(flueBin, flueRunArgs(resolveFlueWorkflow(), projectDir, payload)...) //nolint:gosec // flueBin resolved from managed project
	cmd.Dir = workDir
	cmd.Env = buildBackendEnv(workDir, agentName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func defaultFlueNonInteractiveInvoker(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	// LOOM_FLUE_SANDBOX=daytona runs the task in a fresh remote Daytona sandbox
	// and patch-syncs the result back into the local worktree (proposal Phase 2).
	if resolveFlueSandbox() == "daytona" {
		return runFlueDaytonaTask(workDir, prompt, agentName, shutdown, collector)
	}

	flueBin, projectDir, err := flue.DefaultManager().EnsureProject(context.Background())
	if err != nil {
		return err
	}
	payload, err := fluePayloadJSON(workDir, prompt, resolveFlueModel())
	if err != nil {
		return err
	}

	fmt.Println("Launching flue agent (non-interactive)...")
	fmt.Println("")

	return runHarness(context.Background(), shutdown, harnessInvocation{
		BinaryName: flueBin,
		Args:       flueRunArgs(resolveFlueWorkflow(), projectDir, payload),
		WorkDir:    workDir,
		Env:        buildBackendEnv(workDir, agentName),
		// flue run reads its input from --payload, not stdin.
		Prompt: "",
		// No built-in classifier for flue; the generic cost/quota classifier
		// handles it.
		HarnessName: "",
		LineHandler: func(line string) {
			fmt.Println(line)
		},
		RetryPolicy: harness.DefaultRetryPolicy(),
	})
}

// ManagedRuntimeReady implements cli.ManagedRuntimeBackend. flue has no CLI on
// PATH (it's a managed Node project that builds on first use), so the daemon's
// pre-spawn gate must check Node availability here rather than a PATH lookup,
// which would always report flue missing and park the agent. The project's
// lazy first-use build is not required for readiness — only Node.
func (f *FlueBackend) ManagedRuntimeReady() (bool, string) {
	s := flue.DefaultManager().Probe()
	if s.NodeInstalled {
		return true, ""
	}
	if s.NodeError != "" {
		return false, s.NodeError
	}
	return false, "Node.js >= 22.18 not found (required by the flue runtime)"
}

// Meta returns descriptive metadata about the flue backend. BinaryName is
// intentionally empty: flue is not a global CLI on PATH but a managed Node
// project, so health/setup are handled differently from the binary backends.
func (f *FlueBackend) Meta() BackendMeta {
	return BackendMeta{
		DisplayName: "Flue",
		Description: "Flue agent harness (TypeScript runtime)",
		URL:         "https://flueframework.com",
		BinaryName:  "",
	}
}

// HealthCheck reports readiness without bootstrapping: Node present at a
// supported version, the managed project built, and a provider key set.
func (f *FlueBackend) HealthCheck() HealthStatus {
	s := flue.DefaultManager().Probe()

	var hs HealthStatus
	hs.Version = s.NodeVersion
	hs.APIKeySet = flueProviderKeySet()
	// "Installed" means the flue runtime is available to run — i.e. Node is
	// present. The project itself builds automatically on first use, so a
	// not-yet-built project must NOT report uninstalled (that would make the
	// health-gated commands like `loom lead` refuse to run and never trigger
	// the first-use build). The unbuilt state is surfaced in Message instead.
	hs.Installed = s.NodeInstalled

	var issues []string
	if !s.NodeInstalled {
		if s.NodeError != "" {
			issues = append(issues, s.NodeError)
		} else {
			issues = append(issues, "Node.js >= 22.18 not found")
		}
	}
	if s.NodeInstalled && !s.Built {
		issues = append(issues, "flue project not built yet (runs on first use)")
	}
	if !hs.APIKeySet {
		issues = append(issues, "no provider API key set (e.g. ANTHROPIC_API_KEY)")
	}

	hs.Healthy = s.NodeInstalled && hs.APIKeySet
	if len(issues) > 0 {
		hs.Message = strings.Join(issues, "; ")
	} else {
		hs.Message = "ready"
	}
	return hs
}

// flueProviderKeySet reports whether any usable model credential is present —
// a common provider API key, or local codex (ChatGPT) auth.
func flueProviderKeySet() bool {
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY", "GEMINI_API_KEY"} {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return hasCodexAuthFile()
}

func init() {
	cli.RegisterBackend(&FlueBackend{})
}
