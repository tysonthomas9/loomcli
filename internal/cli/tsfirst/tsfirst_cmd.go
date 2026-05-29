package tsfirst

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	defspkg "github.com/tysonthomas9/loomcli/internal/defs"
)

var (
	addDir  string
	addJSON bool

	checkDir  string
	checkJSON bool

	connectDir     string
	connectEnvFile string
	connectJSON    bool
	connectMessage string

	applyDir      string
	applyInstance string
	applyStart    bool
	applyJSON     bool
)

var addCmd = &cobra.Command{
	Use:   "add",
	Short: "Scaffold TypeScript-first Loom modules",
}

var addAgentCmd = &cobra.Command{
	Use:   "agent <NAME>",
	Short: "Scaffold a TypeScript-defined Loom agent",
	Args:  cobra.ExactArgs(1),
	RunE:  runAddAgent,
}

var addWorkflowCmd = &cobra.Command{
	Use:   "workflow <NAME>",
	Short: "Scaffold a TypeScript-defined Loom workflow",
	Args:  cobra.ExactArgs(1),
	RunE:  runAddWorkflow,
}

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Compile and validate TypeScript-first Loom definitions",
	Args:  cobra.NoArgs,
	RunE:  runCheck,
}

var connectCmd = &cobra.Command{
	Use:   "connect <AGENT> <INSTANCE>",
	Short: "Open a local development session for a TypeScript-defined agent",
	Args:  cobra.ExactArgs(2),
	RunE:  runConnect,
}

var applyCmd = &cobra.Command{
	Use:   "apply <AGENT>",
	Short: "Apply a TypeScript-defined agent into the active Loom workspace",
	Args:  cobra.ExactArgs(1),
	RunE:  runApply,
}

var applyWorkflowCmd = &cobra.Command{
	Use:   "workflow <NAME>",
	Short: "Apply a TypeScript-defined workflow into the active Loom workspace",
	Args:  cobra.ExactArgs(1),
	RunE:  runApplyWorkflow,
}

func init() {
	addCmd.PersistentFlags().StringVar(&addDir, "dir", ".", "Directory containing the Loom TypeScript project")
	addCmd.PersistentFlags().BoolVar(&addJSON, "json", false, "JSON output")
	addCmd.AddCommand(addAgentCmd, addWorkflowCmd)

	checkCmd.Flags().StringVar(&checkDir, "dir", ".", "Directory containing the Loom TypeScript project")
	checkCmd.Flags().BoolVar(&checkJSON, "json", false, "JSON output")

	connectCmd.Flags().StringVar(&connectDir, "dir", ".", "Directory containing the Loom TypeScript project")
	connectCmd.Flags().StringVar(&connectEnvFile, "env", "", "Env file to allowlist for the local session")
	connectCmd.Flags().StringVar(&connectMessage, "message", "", "One-shot message for non-interactive local connect")
	connectCmd.Flags().BoolVar(&connectJSON, "json", false, "JSON output")

	applyCmd.PersistentFlags().StringVar(&applyDir, "dir", ".", "Directory containing the Loom TypeScript project")
	applyCmd.PersistentFlags().BoolVar(&applyJSON, "json", false, "JSON output")
	applyCmd.Flags().StringVar(&applyInstance, "instance", "", "Durable agent instance name (default: agent definition name)")
	applyCmd.Flags().BoolVar(&applyStart, "start", false, "Set desired state to running and queue a start command")
	applyCmd.AddCommand(applyWorkflowCmd)

	cli.RegisterCommand(addCmd)
	cli.RegisterCommand(checkCmd)
	cli.RegisterCommand(connectCmd)
	cli.RegisterCommand(applyCmd)
}

func runAddAgent(_ *cobra.Command, args []string) error {
	path, err := defspkg.ScaffoldAgent(addDir, args[0])
	if err != nil {
		return err
	}
	if addJSON {
		return cmdstore.WriteJSON(map[string]any{
			"agent": args[0],
			"path":  path,
		})
	}
	fmt.Printf("Created TypeScript agent scaffold: %s\n", path)
	fmt.Println("Next: loom check")
	return nil
}

func runAddWorkflow(_ *cobra.Command, args []string) error {
	path, err := defspkg.ScaffoldWorkflow(addDir, args[0])
	if err != nil {
		return err
	}
	if addJSON {
		return cmdstore.WriteJSON(map[string]any{
			"workflow": args[0],
			"path":     path,
		})
	}
	fmt.Printf("Created TypeScript workflow scaffold: %s\n", path)
	fmt.Println("Next: loom check")
	return nil
}

func runCheck(_ *cobra.Command, _ []string) error {
	plan, err := defspkg.Load(checkDir)
	if err != nil {
		return err
	}
	if checkJSON {
		return cmdstore.WriteJSON(plan)
	}
	fmt.Printf("TypeScript definitions OK: %s\n", defspkg.Summary(plan))
	return nil
}

type connectResult struct {
	Root     string   `json:"root"`
	Agent    string   `json:"agent"`
	Instance string   `json:"instance"`
	Backend  string   `json:"backend,omitempty"`
	Model    string   `json:"model,omitempty"`
	EnvFile  string   `json:"env_file,omitempty"`
	Env      []string `json:"env,omitempty"`
	Message  string   `json:"message,omitempty"`
	Response string   `json:"response,omitempty"`
}

func runConnect(_ *cobra.Command, args []string) error {
	plan, err := defspkg.Load(connectDir)
	if err != nil {
		return err
	}
	agent, ok := defspkg.FindAgent(plan, args[0])
	if !ok {
		return fmt.Errorf("agent definition %q not found", args[0])
	}
	envNames, err := envNamesForConnect(connectEnvFile)
	if err != nil {
		return err
	}
	envNames = compactStrings(append(agent.Env, envNames...))
	result := connectResult{
		Root:     plan.Root,
		Agent:    agent.Name,
		Instance: args[1],
		Backend:  agent.Backend,
		Model:    agent.Model,
		EnvFile:  connectEnvFile,
		Env:      envNames,
	}
	if connectMessage != "" {
		result.Message = connectMessage
		result.Response = localConnectResponse(agent, connectMessage)
	}
	if connectJSON {
		return cmdstore.WriteJSON(result)
	}
	fmt.Printf("Connected to %s instance %s (backend=%s model=%s)\n", result.Agent, result.Instance, fallback(result.Backend, "default"), fallback(result.Model, "default"))
	if len(result.Env) > 0 {
		fmt.Printf("Env allowlist: %s\n", strings.Join(result.Env, ", "))
	}
	if result.Message != "" {
		fmt.Printf("You: %s\n", result.Message)
		fmt.Printf("%s: %s\n", result.Agent, result.Response)
		return nil
	}
	fmt.Println("Local session ready. Use --message for a one-shot prompt in non-interactive shells.")
	return nil
}

type applyResult struct {
	Workspace string `json:"workspace"`
	Agent     string `json:"agent"`
	Instance  string `json:"instance"`
	Version   string `json:"version"`
	Started   bool   `json:"started"`
	State     string `json:"state,omitempty"`
	Desired   string `json:"desired_state,omitempty"`
	Summary   string `json:"summary"`
}

type applyWorkflowResult struct {
	Workspace string `json:"workspace"`
	Workflow  string `json:"workflow"`
	Version   string `json:"version"`
	Route     string `json:"route,omitempty"`
	Trigger   string `json:"trigger,omitempty"`
	Summary   string `json:"summary"`
}

func runApply(_ *cobra.Command, args []string) error {
	plan, err := defspkg.Load(applyDir)
	if err != nil {
		return err
	}
	agent, ok := defspkg.FindAgent(plan, args[0])
	if !ok {
		return fmt.Errorf("agent definition %q not found", args[0])
	}
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if err := defspkg.Apply(ctx, h.Store, ws, actorName(), plan); err != nil {
			return err
		}
		instance, err := defspkg.ApplyAgentInstance(ctx, h.Store, ws, agent, applyInstance, applyStart)
		if err != nil {
			return err
		}
		result := applyResult{
			Workspace: ws,
			Agent:     agent.Name,
			Instance:  instance.Name,
			Version:   agent.Version,
			Started:   applyStart,
			State:     string(instance.State),
			Desired:   string(instance.DesiredState),
			Summary:   defspkg.Summary(plan),
		}
		if applyJSON {
			return cmdstore.WriteJSON(result)
		}
		fmt.Printf("Applied %s@%s to workspace %s as agent instance %s\n", result.Agent, result.Version, result.Workspace, result.Instance)
		if applyStart {
			fmt.Println("Queued start request; a running workspace daemon will pick it up.")
		}
		return nil
	})
}

func runApplyWorkflow(_ *cobra.Command, args []string) error {
	plan, err := defspkg.Load(applyDir)
	if err != nil {
		return err
	}
	workflow, ok := defspkg.FindWorkflow(plan, args[0])
	if !ok {
		return fmt.Errorf("workflow definition %q not found", args[0])
	}
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if err := defspkg.Apply(ctx, h.Store, ws, actorName(), plan); err != nil {
			return err
		}
		result := applyWorkflowResult{
			Workspace: ws,
			Workflow:  workflow.Name,
			Version:   workflow.Version,
			Route:     workflow.RoutePath,
			Trigger:   workflow.TriggerEvent,
			Summary:   defspkg.Summary(plan),
		}
		if applyJSON {
			return cmdstore.WriteJSON(result)
		}
		fmt.Printf("Applied workflow %s@%s to workspace %s\n", result.Workflow, result.Version, result.Workspace)
		if result.Route != "" {
			fmt.Printf("Route: POST %s\n", result.Route)
		}
		if result.Trigger != "" {
			fmt.Printf("Trigger: %s\n", result.Trigger)
		}
		return nil
	})
}

func envNamesForConnect(path string) ([]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path) //nolint:gosec // explicit user-supplied env file path for local dev.
	if err != nil {
		return nil, fmt.Errorf("read env file %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	var out []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if idx := strings.IndexByte(line, '='); idx > 0 {
			out = append(out, strings.TrimSpace(line[:idx]))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read env file %s: %w", path, err)
	}
	return compactStrings(out), nil
}

func localConnectResponse(agent defspkg.AgentModule, message string) string {
	if strings.EqualFold(agent.Backend, "echo") {
		return "echo: " + message
	}
	return "local connect compiled the agent manifest; live backend chat is handled by the runtime provider"
}

func actorName() string {
	if actor := strings.TrimSpace(os.Getenv("LOOM_ACTOR")); actor != "" {
		return actor
	}
	if actor := strings.TrimSpace(os.Getenv("USER")); actor != "" {
		return actor
	}
	return "loom"
}

func compactStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func fallback(v, fallbackValue string) string {
	if strings.TrimSpace(v) == "" {
		return fallbackValue
	}
	return v
}
