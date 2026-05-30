package tsfirst

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	backendcaps "github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	defspkg "github.com/tysonthomas9/loomcli/internal/defs"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/usage"
	workflowpkg "github.com/tysonthomas9/loomcli/internal/workflow"
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
	connectSession string

	applyDir      string
	applyInstance string
	applyStart    bool
	applyJSON     bool

	runDir     string
	runInput   string
	runPayload string
	runWait    bool
	runOnce    bool
	runJSON    bool

	tsfirstWithActiveWorkspace = cmdstore.WithActiveWorkspace
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

var addSkillCmd = &cobra.Command{
	Use:   "skill <NAME>",
	Short: "Scaffold a source-owned Loom skill bundle",
	Args:  cobra.ExactArgs(1),
	RunE:  runAddSkill,
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

var runCmd = &cobra.Command{
	Use:   "run <WORKFLOW>",
	Short: "Apply and run a TypeScript-defined workflow",
	Args:  cobra.ExactArgs(1),
	RunE:  runTypeScriptWorkflowCommand,
}

func init() {
	addCmd.PersistentFlags().StringVar(&addDir, "dir", ".", "Directory containing the Loom TypeScript project")
	addCmd.PersistentFlags().BoolVar(&addJSON, "json", false, "JSON output")
	addCmd.AddCommand(addAgentCmd, addWorkflowCmd, addSkillCmd)

	checkCmd.Flags().StringVar(&checkDir, "dir", ".", "Directory containing the Loom TypeScript project")
	checkCmd.Flags().BoolVar(&checkJSON, "json", false, "JSON output")

	connectCmd.Flags().StringVar(&connectDir, "dir", ".", "Directory containing the Loom TypeScript project")
	connectCmd.Flags().StringVar(&connectEnvFile, "env", "", "Env file to allowlist for the local session")
	connectCmd.Flags().StringVar(&connectMessage, "message", "", "One-shot message for non-interactive local connect")
	connectCmd.Flags().StringVar(&connectSession, "session", "default", "Named local session to continue")
	connectCmd.Flags().BoolVar(&connectJSON, "json", false, "JSON output")

	applyCmd.PersistentFlags().StringVar(&applyDir, "dir", ".", "Directory containing the Loom TypeScript project")
	applyCmd.PersistentFlags().BoolVar(&applyJSON, "json", false, "JSON output")
	applyCmd.Flags().StringVar(&applyInstance, "instance", "", "Durable agent instance name (default: agent definition name)")
	applyCmd.Flags().BoolVar(&applyStart, "start", false, "Set desired state to running and queue a start command")
	applyCmd.AddCommand(applyWorkflowCmd)

	runCmd.Flags().StringVar(&runDir, "dir", ".", "Directory containing the Loom TypeScript project")
	runCmd.Flags().StringVar(&runInput, "input", "{}", "Workflow input JSON")
	runCmd.Flags().StringVar(&runPayload, "payload", "", "Workflow input JSON (alias for --input)")
	runCmd.Flags().BoolVar(&runWait, "wait", false, "Poll until the workflow reaches a terminal state")
	runCmd.Flags().BoolVar(&runOnce, "once", true, "Run one reconcile pass for constrained built-in workflows")
	runCmd.Flags().BoolVar(&runJSON, "json", false, "JSON output")

	cli.RegisterCommand(addCmd)
	cli.RegisterCommand(checkCmd)
	cli.RegisterCommand(connectCmd)
	cli.RegisterCommand(applyCmd)
	cli.RegisterCommand(runCmd)
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

func runAddSkill(_ *cobra.Command, args []string) error {
	path, err := defspkg.ScaffoldSkill(addDir, args[0])
	if err != nil {
		return err
	}
	if addJSON {
		return cmdstore.WriteJSON(map[string]any{
			"skill": args[0],
			"path":  path,
		})
	}
	fmt.Printf("Created Loom skill scaffold: %s\n", path)
	fmt.Printf("Import it from an agent with: import %s from '../skills/%s/SKILL.md' with { type: 'skill' };\n", importName(args[0]), args[0])
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

func runConnect(_ *cobra.Command, args []string) error {
	opts := connectOptions{
		Dir:      connectDir,
		Agent:    args[0],
		Instance: args[1],
		Session:  connectSession,
		EnvFile:  connectEnvFile,
		Message:  connectMessage,
	}
	if opts.Message != "" {
		return runOneConnectMessage(opts)
	}
	messages, interactive, err := stdinMessages()
	if err != nil {
		return err
	}
	if len(messages) == 0 {
		return runConnectReady(opts, interactive)
	}
	return runConnectMessages(opts, messages)
}

func runOneConnectMessage(opts connectOptions) error {
	if !connectJSON {
		return runConnectMessages(opts, []string{opts.Message})
	}
	result, err := runLocalConnect(context.Background(), opts)
	if err != nil {
		return err
	}
	return printConnectResult(result, connectJSON)
}

func runConnectReady(opts connectOptions, interactive bool) error {
	if interactive && !connectJSON {
		return runInteractiveConnect(context.Background(), opts, os.Stdin, os.Stdout)
	}
	result, err := connectReady(opts)
	if err != nil {
		return err
	}
	if connectJSON {
		return cmdstore.WriteJSON(result)
	}
	fmt.Printf("Connected to %s instance %s session %s (backend=%s model=%s)\n", result.Agent, result.Instance, result.Session, fallback(result.Backend, "default"), fallback(result.Model, "default"))
	fmt.Println("Local session ready. Pass --message or pipe prompts on stdin to run turns.")
	return nil
}

func runConnectMessages(opts connectOptions, messages []string) error {
	results := make([]connectResult, 0, len(messages))
	var ready connectResult
	if !connectJSON {
		var err error
		ready, err = connectReady(opts)
		if err != nil {
			return err
		}
		fmt.Printf("Connected to %s instance %s session %s (backend=%s model=%s)\n", ready.Agent, ready.Instance, ready.Session, fallback(ready.Backend, "default"), fallback(ready.Model, "default"))
		if len(ready.Env) > 0 {
			fmt.Printf("Env allowlist: %s\n", strings.Join(ready.Env, ", "))
		}
	}
	for _, message := range messages {
		opts.Message = message
		var streamed *trackingWriter
		if !connectJSON {
			fmt.Printf("You: %s\n", message)
			fmt.Printf("%s: ", ready.Agent)
			streamed = &trackingWriter{w: os.Stdout}
			opts.Stream = streamed
		}
		result, err := runLocalConnect(context.Background(), opts)
		if err != nil {
			return err
		}
		results = append(results, result)
		if !connectJSON {
			if err := ensureStreamLineBreak(streamed); err != nil {
				return err
			}
		}
	}
	if connectJSON {
		return cmdstore.WriteJSON(results)
	}
	if len(results) > 0 && results[len(results)-1].TranscriptPath != "" {
		fmt.Printf("Transcript: %s\n", results[len(results)-1].TranscriptPath)
	}
	return nil
}

func runInteractiveConnect(ctx context.Context, opts connectOptions, in io.Reader, out io.Writer) error {
	result, err := connectReady(opts)
	if err != nil {
		return err
	}
	if err := printInteractiveConnectHeader(out, result); err != nil {
		return err
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for {
		if _, err := fmt.Fprint(out, "> "); err != nil {
			return err
		}
		if !scanner.Scan() {
			if _, err := fmt.Fprintln(out); err != nil {
				return err
			}
			break
		}
		message := strings.TrimSpace(scanner.Text())
		if message == "" {
			continue
		}
		if message == "/exit" || message == "/quit" {
			break
		}
		turnOpts := opts
		turnOpts.Message = message
		if _, err := fmt.Fprintf(out, "%s: ", result.Agent); err != nil {
			return err
		}
		streamed := &trackingWriter{w: out}
		turnOpts.Stream = streamed
		if _, err := runLocalConnect(ctx, turnOpts); err != nil {
			return err
		}
		if err := ensureStreamLineBreak(streamed); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func runLocalConnect(ctx context.Context, opts connectOptions) (connectResult, error) {
	plan, agent, result, err := connectReadyWithAgent(opts)
	if err != nil {
		return connectResult{}, err
	}
	history, err := readLocalTurns(result.TranscriptPath)
	if err != nil {
		return connectResult{}, err
	}
	prompt := localConnectPrompt(plan, agent, result.Instance, result.Session, opts.Message, history)
	_, envValues, err := envForConnect(opts.EnvFile)
	if err != nil {
		return connectResult{}, err
	}
	started := time.Now().UTC()
	operationID := localOperationID(agent.Name, result.Instance, result.Session, started)
	invocation, err := withTemporaryEnv(envValues, func() (localInvocationResult, error) {
		return invokeLocalAgent(ctx, plan, agent, prompt, opts.Message, opts.Stream, lastProviderSessionID(history))
	})
	if err != nil {
		completed := time.Now().UTC()
		result, turn := failLocalConnectResult(result, agent, opts.Message, operationID, lastProviderSessionID(history), prompt, started, completed, err)
		if appendErr := appendLocalTurn(result.TranscriptPath, turn); appendErr != nil {
			return result, errors.Join(err, appendErr)
		}
		return result, err
	}
	completed := time.Now().UTC()
	providerSessionID := fallback(invocation.ProviderSessionID, lastProviderSessionID(history))
	result, turn := completeLocalConnectResult(result, agent, opts.Message, operationID, providerSessionID, prompt, started, completed, completed.Sub(started), invocation)
	if err := appendLocalTurn(result.TranscriptPath, turn); err != nil {
		return connectResult{}, err
	}
	return result, nil
}

func connectReady(opts connectOptions) (connectResult, error) {
	_, _, result, err := connectReadyWithAgent(opts)
	return result, err
}

func connectReadyWithAgent(opts connectOptions) (*defspkg.Plan, defspkg.AgentModule, connectResult, error) {
	plan, err := defspkg.Load(opts.Dir)
	if err != nil {
		return nil, defspkg.AgentModule{}, connectResult{}, err
	}
	agent, ok := defspkg.FindAgent(plan, opts.Agent)
	if !ok {
		return nil, defspkg.AgentModule{}, connectResult{}, fmt.Errorf("agent definition %q not found", opts.Agent)
	}
	envNames, _, err := envForConnect(opts.EnvFile)
	if err != nil {
		return nil, defspkg.AgentModule{}, connectResult{}, err
	}
	envNames = compactStrings(append(agent.Env, envNames...))
	result := connectResult{
		Root:           plan.Root,
		Agent:          agent.Name,
		Instance:       fallback(opts.Instance, "local"),
		Session:        fallback(opts.Session, "default"),
		Backend:        agent.Backend,
		Model:          agent.Model,
		WorkDir:        localWorkDir(plan.Root, agent),
		EnvFile:        opts.EnvFile,
		Env:            envNames,
		TranscriptPath: localTranscriptPath(plan.Root, agent.Name, fallback(opts.Instance, "local"), fallback(opts.Session, "default")),
		ToolRuntime:    localConnectToolRuntime(plan, agent),
	}
	return plan, agent, result, nil
}

func printConnectResult(result connectResult, jsonOut bool) error {
	if jsonOut {
		return cmdstore.WriteJSON(result)
	}
	fmt.Printf("Connected to %s instance %s session %s (backend=%s model=%s)\n", result.Agent, result.Instance, result.Session, fallback(result.Backend, "default"), fallback(result.Model, "default"))
	if len(result.Env) > 0 {
		fmt.Printf("Env allowlist: %s\n", strings.Join(result.Env, ", "))
	}
	if result.ToolRuntime != nil && len(result.ToolRuntime.TypedTools) > 0 {
		fmt.Printf("Typed tool runtime: %s (%s)\n", result.ToolRuntime.Status, typedToolNames(result.ToolRuntime))
	}
	if result.Message != "" {
		fmt.Printf("You: %s\n", result.Message)
		fmt.Printf("%s: %s\n", result.Agent, result.Response)
	}
	if result.TranscriptPath != "" {
		fmt.Printf("Transcript: %s\n", result.TranscriptPath)
	}
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

type workflowRunResult struct {
	Workspace string                        `json:"workspace"`
	Workflow  string                        `json:"workflow"`
	Version   string                        `json:"version"`
	Summary   string                        `json:"summary"`
	Run       *domain.WorkflowRun           `json:"run"`
	Builtin   *workflowpkg.BuiltinRunResult `json:"builtin,omitempty"`
	Operation *workflowRunOperation         `json:"operation,omitempty"`
}

type workflowRunOperation struct {
	ID               string                   `json:"id"`
	Kind             string                   `json:"kind"`
	Status           domain.WorkflowRunStatus `json:"status"`
	WorkflowRunID    string                   `json:"workflow_run_id"`
	WorkflowName     string                   `json:"workflow_name"`
	WorkflowVersion  string                   `json:"workflow_version,omitempty"`
	Result           json.RawMessage          `json:"result,omitempty"`
	ErrorClass       string                   `json:"error_class,omitempty"`
	ErrorMessage     string                   `json:"error_message,omitempty"`
	StartedAt        string                   `json:"started_at,omitempty"`
	FinishedAt       string                   `json:"finished_at,omitempty"`
	EventCount       int                      `json:"event_count,omitempty"`
	TaskRunCount     int                      `json:"task_run_count,omitempty"`
	EventCorrelation map[string]string        `json:"event_correlation,omitempty"`
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
	return tsfirstWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
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
	return tsfirstWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
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

func runTypeScriptWorkflowCommand(_ *cobra.Command, args []string) error {
	plan, err := defspkg.Load(runDir)
	if err != nil {
		return err
	}
	workflow, ok := defspkg.FindWorkflow(plan, args[0])
	if !ok {
		return fmt.Errorf("workflow definition %q not found", args[0])
	}
	input, err := parseWorkflowPayload(runInput, runPayload)
	if err != nil {
		return err
	}
	return tsfirstWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		result, err := runTypeScriptWorkflow(ctx, h.Store, cli.DefaultIssueBackend(), ws, actorName(), plan, workflow, input, runOnce, runWait)
		if err != nil {
			return err
		}
		if runJSON {
			return cmdstore.WriteJSON(result)
		}
		fmt.Printf("Workflow run %s %s (%s@%s)\n", result.Run.RunID, result.Run.Status, result.Workflow, result.Version)
		fmt.Printf("Applied TypeScript workflow to workspace %s\n", result.Workspace)
		if result.Builtin != nil {
			fmt.Printf("ready=%d open=%d blocked=%d ensured=%d\n", result.Builtin.ReadyCount, result.Builtin.OpenCount, result.Builtin.BlockedCount, len(result.Builtin.TaskRuns))
		}
		return nil
	})
}

func runTypeScriptWorkflow(ctx context.Context, st store.Store, ib backend.IssueBackend, workspace, actor string, plan *defspkg.Plan, workflow defspkg.WorkflowModule, input json.RawMessage, once, wait bool) (*workflowRunResult, error) {
	if err := defspkg.Apply(ctx, st, workspace, actor, plan); err != nil {
		return nil, err
	}
	run, err := workflowpkg.CreateOrResumeRun(ctx, st, workspace, workflow.Name, input, actor)
	if err != nil {
		return nil, fmt.Errorf("create workflow run: %w", err)
	}
	var builtin *workflowpkg.BuiltinRunResult
	if once {
		if ib == nil {
			return nil, errors.New("no issue backend available")
		}
		builtin, err = workflowpkg.RunOnce(ctx, st, ib, run)
		if err != nil {
			return nil, fmt.Errorf("run workflow: %w", err)
		}
		run = builtin.Run
	}
	if wait {
		run, err = waitTypeScriptWorkflow(ctx, st, workspace, run.RunID)
		if err != nil {
			return nil, err
		}
	}
	operation, err := workflowRunOperationEnvelope(ctx, st, workspace, run, builtin)
	if err != nil {
		return nil, err
	}
	return &workflowRunResult{
		Workspace: workspace,
		Workflow:  workflow.Name,
		Version:   workflow.Version,
		Summary:   defspkg.Summary(plan),
		Run:       run,
		Builtin:   builtin,
		Operation: operation,
	}, nil
}

func workflowRunOperationEnvelope(ctx context.Context, st store.Store, workspace string, run *domain.WorkflowRun, builtin *workflowpkg.BuiltinRunResult) (*workflowRunOperation, error) {
	if run == nil {
		return nil, nil
	}
	eventCount := 0
	if st != nil && st.RunEvents() != nil {
		events, err := st.RunEvents().List(ctx, workspace, store.RunEventFilter{WorkflowRunID: run.RunID, Limit: 10000})
		if err != nil {
			return nil, fmt.Errorf("list workflow operation events: %w", err)
		}
		eventCount = len(events)
	}
	taskRunCount := 0
	if builtin != nil {
		taskRunCount = len(builtin.TaskRuns)
	}
	operation := &workflowRunOperation{
		ID:              run.RunID,
		Kind:            "workflow_run",
		Status:          run.Status,
		WorkflowRunID:   run.RunID,
		WorkflowName:    run.WorkflowName,
		WorkflowVersion: run.WorkflowVersion,
		Result:          cloneRawMessage(run.Result),
		ErrorClass:      run.ErrorClass,
		ErrorMessage:    run.ErrorMessage,
		EventCount:      eventCount,
		TaskRunCount:    taskRunCount,
		EventCorrelation: map[string]string{
			"workspace":       workspace,
			"workflow_run_id": run.RunID,
			"workflow":        run.WorkflowName,
		},
	}
	if !run.StartedAt.IsZero() {
		operation.StartedAt = run.StartedAt.Format(time.RFC3339Nano)
	}
	if run.FinishedAt != nil {
		operation.FinishedAt = run.FinishedAt.Format(time.RFC3339Nano)
	}
	return operation, nil
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func parseWorkflowInput(s string) (json.RawMessage, error) {
	return parseWorkflowInputFlag(s, "--input")
}

func parseWorkflowPayload(input, payload string) (json.RawMessage, error) {
	if strings.TrimSpace(payload) == "" {
		return parseWorkflowInput(input)
	}
	if trimmedInput := strings.TrimSpace(input); trimmedInput != "" && trimmedInput != "{}" {
		return nil, errors.New("--input and --payload cannot both be set")
	}
	return parseWorkflowInputFlag(payload, "--payload")
}

func parseWorkflowInputFlag(s, flagName string) (json.RawMessage, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "{}"
	}
	var tmp any
	if err := json.Unmarshal([]byte(s), &tmp); err != nil {
		return nil, fmt.Errorf("%s must be valid JSON: %w", flagName, err)
	}
	return json.RawMessage(s), nil
}

func waitTypeScriptWorkflow(ctx context.Context, st store.Store, workspace, runID string) (*domain.WorkflowRun, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		run, err := st.WorkflowRuns().Get(ctx, workspace, runID)
		if err != nil {
			return nil, err
		}
		if !domain.WorkflowRunStatusLive(run.Status) {
			return run, nil
		}
		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case <-ticker.C:
		}
	}
}

func envForConnect(path string) ([]string, map[string]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil, nil
	}
	f, err := os.Open(path) //nolint:gosec // explicit user-supplied env file path for local dev.
	if err != nil {
		return nil, nil, fmt.Errorf("read env file %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	var out []string
	values := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if idx := strings.IndexByte(line, '='); idx > 0 {
			name := strings.TrimSpace(line[:idx])
			out = append(out, name)
			values[name] = cleanEnvValue(line[idx+1:])
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("read env file %s: %w", path, err)
	}
	return compactStrings(out), values, nil
}

func cleanEnvValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func withTemporaryEnv[T any](values map[string]string, fn func() (T, error)) (T, error) {
	var zero T
	if len(values) == 0 {
		return fn()
	}
	type prior struct {
		value string
		ok    bool
	}
	restore := make(map[string]prior, len(values))
	for key, value := range values {
		old, ok := os.LookupEnv(key)
		restore[key] = prior{value: old, ok: ok}
		if err := os.Setenv(key, value); err != nil {
			return zero, err
		}
	}
	defer func() {
		for key, old := range restore {
			if old.ok {
				_ = os.Setenv(key, old.value)
			} else {
				_ = os.Unsetenv(key)
			}
		}
	}()
	return fn()
}

func invokeLocalAgent(ctx context.Context, plan *defspkg.Plan, agent defspkg.AgentModule, prompt, message string, stream io.Writer, resumeProviderSessionID string) (localInvocationResult, error) {
	toolRuntime := localConnectToolRuntime(plan, agent)
	resume := newConnectResume(resumeProviderSessionID)
	if strings.EqualFold(agent.Backend, "echo") {
		response := "echo: " + message
		markResumeUnsupported(resume, "echo")
		if stream != nil {
			if _, err := io.WriteString(stream, response); err != nil {
				return localInvocationResult{}, err
			}
		}
		return localInvocationResult{Response: response, ToolRuntime: echoToolRuntime(toolRuntime), Resume: resume}, nil
	}
	backendName := strings.TrimSpace(agent.Backend)
	if backendName == "" {
		backendName = cli.GetBackendName()
	}
	backend, ok := cli.GetBackendByName(backendName)
	if !ok {
		return localInvocationResult{}, fmt.Errorf("backend %q is not registered; use backend \"echo\" for offline local connect or install/configure the backend", backendName)
	}
	appliedToolRuntime, err := enforceBackendTypedTools(backendName, backend, toolRuntime)
	if err != nil {
		return localInvocationResult{}, err
	}
	workDir := localWorkDir(plan.Root, agent)
	if streamer, ok := backend.(backendcaps.StreamingBackend); ok {
		var rc io.ReadCloser
		var err error
		if resume != nil {
			if resumable, ok := backend.(backendcaps.ResumableStreamingBackend); ok {
				rc, err = resumable.InvokeStreamingResumed(ctx, workDir, prompt, agent.Name, resume.PriorProviderSessionID)
				if err != nil {
					return localInvocationResult{}, err
				}
				markResumeApplied(resume, connectResumeMethodStreamingResumed)
			} else if setBackendResumeSessionID(backend, resume.PriorProviderSessionID) {
				markResumeApplied(resume, connectResumeMethodSetter)
			} else {
				markResumeUnsupported(resume, backendName)
			}
		}
		if rc == nil {
			rc, err = streamer.InvokeStreaming(ctx, workDir, prompt, agent.Name)
		}
		if err != nil {
			return localInvocationResult{}, err
		}
		defer func() { _ = rc.Close() }()
		result, err := captureStreamingResponse(rc, stream)
		result.ToolRuntime = appliedToolRuntime
		result.ToolCalls = collectBackendTypedToolCalls(backend, workDir)
		backendMetadata := backendReportedProviderMetadata(backend, workDir)
		result.ProviderMetadata = mergeBackendProviderMetadata(result.ProviderMetadata, backendMetadata)
		if result.ProviderModel == "" {
			result.ProviderModel = backendProviderModel(backendMetadata)
		}
		result.Resume = resume
		result = fillBackendSessionID(result, backend, workDir)
		return result, err
	}
	var result localInvocationResult
	if resume != nil {
		if resumable, ok := backend.(backendcaps.ResumableNonInteractiveBackend); ok {
			result, err = invokeNonStreamingLocalAgentWithRunner(backendName, workDir, prompt, agent.Name, stream, func(shutdown <-chan struct{}, collector *usage.Collector) error {
				return resumable.InvokeNonInteractiveResumed(workDir, prompt, agent.Name, resume.PriorProviderSessionID, shutdown, collector)
			})
			markResumeApplied(resume, connectResumeMethodNonInteractiveResumed)
		} else if setBackendResumeSessionID(backend, resume.PriorProviderSessionID) {
			markResumeApplied(resume, connectResumeMethodSetter)
			result, err = invokeNonStreamingLocalAgent(backend, backendName, workDir, prompt, agent.Name, stream)
		} else {
			markResumeUnsupported(resume, backendName)
			result, err = invokeNonStreamingLocalAgent(backend, backendName, workDir, prompt, agent.Name, stream)
		}
	} else {
		result, err = invokeNonStreamingLocalAgent(backend, backendName, workDir, prompt, agent.Name, stream)
	}
	result.ToolRuntime = appliedToolRuntime
	result.ToolCalls = collectBackendTypedToolCalls(backend, workDir)
	backendMetadata := backendReportedProviderMetadata(backend, workDir)
	result.ProviderMetadata = mergeBackendProviderMetadata(result.ProviderMetadata, backendMetadata)
	if result.ProviderModel == "" {
		result.ProviderModel = backendProviderModel(backendMetadata)
	}
	result.Resume = resume
	result = fillBackendSessionID(result, backend, workDir)
	return result, err
}

type localTurn struct {
	Timestamp         string              `json:"timestamp"`
	OperationID       string              `json:"operation_id,omitempty"`
	Operation         *connectOperation   `json:"operation,omitempty"`
	ErrorClass        string              `json:"error_class,omitempty"`
	ErrorMessage      string              `json:"error_message,omitempty"`
	Agent             string              `json:"agent"`
	Instance          string              `json:"instance"`
	Session           string              `json:"session"`
	Backend           string              `json:"backend,omitempty"`
	Model             string              `json:"model,omitempty"`
	ProviderModel     string              `json:"provider_model,omitempty"`
	ProviderSessionID string              `json:"provider_session_id,omitempty"`
	ProviderMetadata  map[string]any      `json:"provider_metadata,omitempty"`
	DefinitionVersion string              `json:"definition_version,omitempty"`
	Message           string              `json:"message"`
	Response          string              `json:"response,omitempty"`
	DurationMS        int64               `json:"duration_ms,omitempty"`
	Usage             *connectUsage       `json:"usage,omitempty"`
	PromptHash        string              `json:"prompt_hash,omitempty"`
	ToolRuntime       *connectToolRuntime `json:"tool_runtime,omitempty"`
	ToolCalls         []connectToolCall   `json:"tool_calls,omitempty"`
	Resume            *connectResume      `json:"resume,omitempty"`
}

func readLocalTurns(path string) ([]localTurn, error) {
	f, err := os.Open(path) //nolint:gosec // local transcript path under the selected project root.
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read local transcript %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	var out []localTurn
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		var turn localTurn
		if err := json.Unmarshal(scanner.Bytes(), &turn); err != nil {
			return nil, fmt.Errorf("parse local transcript %s: %w", path, err)
		}
		out = append(out, turn)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read local transcript %s: %w", path, err)
	}
	return out, nil
}

func appendLocalTurn(path string, turn localTurn) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create local session dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644) //nolint:gosec // transcript file under selected project root.
	if err != nil {
		return fmt.Errorf("open local transcript %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	data, err := json.Marshal(turn)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write local transcript %s: %w", path, err)
	}
	return nil
}

func localConnectPrompt(plan *defspkg.Plan, agent defspkg.AgentModule, instance, session, message string, history []localTurn) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are the TypeScript-defined Loom agent %q.\n", agent.Name)
	if agent.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n", agent.Description)
	}
	if agent.Model != "" {
		fmt.Fprintf(&b, "Model: %s\n", agent.Model)
	}
	fmt.Fprintf(&b, "Instance: %s\nSession: %s\nProject root: %s\n", instance, session, plan.Root)
	if len(agent.Repos) > 0 {
		fmt.Fprintf(&b, "Runtime repos: %s\n", strings.Join(agent.Repos, ", "))
	}
	if len(agent.AllowedCommands) > 0 || len(agent.DeniedCommands) > 0 {
		fmt.Fprintf(&b, "Allowed commands: %s\nDenied commands: %s\n", strings.Join(agent.AllowedCommands, ", "), strings.Join(agent.DeniedCommands, ", "))
	}
	if len(agent.Skills) > 0 {
		fmt.Fprintf(&b, "Registered skills: %s\n", strings.Join(agent.Skills, ", "))
	}
	if strings.TrimSpace(agent.Instructions) != "" {
		fmt.Fprintf(&b, "\nAgent instructions:\n%s\n", strings.TrimSpace(agent.Instructions))
	}
	if len(history) > 0 {
		fmt.Fprintf(&b, "\nRecent local session history:\n")
		start := 0
		if len(history) > 6 {
			start = len(history) - 6
		}
		for _, turn := range history[start:] {
			fmt.Fprintf(&b, "User: %s\nAgent: %s\n", turn.Message, turn.Response)
		}
	}
	fmt.Fprintf(&b, "\nUser message:\n%s\n", message)
	return b.String()
}

func lastProviderSessionID(history []localTurn) string {
	for i := len(history) - 1; i >= 0; i-- {
		if id := strings.TrimSpace(history[i].ProviderSessionID); id != "" {
			return id
		}
	}
	return ""
}

func localOperationID(agent, instance, session string, at time.Time) string {
	seed := strings.Join([]string{agent, instance, session, at.Format(time.RFC3339Nano)}, "\x00")
	sum := hashText(seed)
	if len(sum) > 16 {
		sum = sum[:16]
	}
	return "lc_" + sum
}

func stdinMessages() ([]string, bool, error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return nil, true, nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, false, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out, false, nil
}

func localWorkDir(root string, agent defspkg.AgentModule) string {
	for _, repo := range agent.Repos {
		repo = strings.TrimSpace(repo)
		if repo == "" {
			continue
		}
		if repo == "." {
			return root
		}
		if filepath.IsAbs(repo) {
			return repo
		}
		return filepath.Join(root, repo)
	}
	return root
}

func localTranscriptPath(root, agent, instance, session string) string {
	return filepath.Join(root, ".loom", "local-sessions", safePathSegment(agent), safePathSegment(instance), safePathSegment(session)+".jsonl")
}

func safePathSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
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

func importName(name string) string {
	parts := strings.Split(strings.TrimSpace(name), "-")
	if len(parts) == 0 {
		return "skill"
	}
	out := parts[0]
	for _, part := range parts[1:] {
		if part == "" {
			continue
		}
		out += strings.ToUpper(part[:1]) + part[1:]
	}
	return out + "Skill"
}
