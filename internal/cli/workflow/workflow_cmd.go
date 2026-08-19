package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/runtimepreflight"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/workflows"
)

var (
	workflowCloneOut     string
	workflowCloneJSON    bool
	workflowBuildSource  string
	workflowBuildJSON    bool
	workflowVersionID    string
	workflowApproveJSON  bool
	workflowActivateJSON bool
	workflowRunVersion   string
	workflowRunEpic      string
	workflowRunInput     []string
	workflowRunJSON      bool
	workflowListJSON     bool
	workflowVersionsJSON bool
	workflowReadyzJSON   bool
	workflowDigestJSON   bool
	workflowDigestFiles  []string
)

var (
	workflowWithActiveWorkspace = cmdstore.WithActiveWorkspace
	workflowBuildAndRegister    = workflows.BuildAndRegister
)

var workflowCmd = &cobra.Command{
	Use:     "workflow",
	Short:   "Author, approve, activate, and run Flue workflows",
	GroupID: "workspace",
}

var workflowCloneCmd = &cobra.Command{
	Use:   "clone <workflow>",
	Short: "Copy built-in workflow TypeScript source into a local source tree",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowClone,
}

var workflowBuildCmd = &cobra.Command{
	Use:   "build <workflow>",
	Short: "Build local workflow TypeScript source and register a non-active DriverVersion",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowBuild,
}

var workflowApproveCmd = &cobra.Command{
	Use:   "approve <workflow>",
	Short: "Approve one workflow DriverVersion for local process execution",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowApprove,
}

var workflowUnapproveCmd = &cobra.Command{
	Use:   "unapprove <workflow>",
	Short: "Remove approval from one workflow DriverVersion",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowUnapprove,
}

var workflowActivateCmd = &cobra.Command{
	Use:   "activate <workflow>",
	Short: "Make a workflow DriverVersion the active version",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowActivate,
}

var workflowRunCmd = &cobra.Command{
	Use:   "run <workflow>",
	Short: "Create a DriverRun for a workflow",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowRun,
}

var workflowListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered workflows",
	Args:  cobra.NoArgs,
	RunE:  runWorkflowList,
}

var workflowVersionsCmd = &cobra.Command{
	Use:   "versions <workflow>",
	Short: "List registered workflow versions",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowVersions,
}

var workflowReadyzCmd = &cobra.Command{
	Use:   "readyz",
	Short: "Check local workflow authoring prerequisites",
	Args:  cobra.NoArgs,
	RunE:  runWorkflowReadyz,
}

var workflowDigestCmd = &cobra.Command{
	Use:   "digest <workflow>",
	Short: "Print the canonical source digest of a built-in workflow",
	Long: `Print the canonical source digest (workflows.SourceDigest) of a built-in
workflow's source tree.

This is the SAME digest serve's builtin self-heal (EnsureBuiltinWorkflow)
computes, so out-of-band registrations that stamp it — e.g.
loom driver register --source-digest "$(loom workflow digest epic-runner)" —
hit the self-heal's exact-match fast path instead of logging digest drift.

Without --file the digest covers the sources embedded in THIS binary. A
registration should attest the bytes it actually STAGED, so register flows
pass --file <spec-key>=<path> for every source file in the bundle (the key
set must exactly match the workflow's source set); the digest then hashes
the staged contents under the canonical keys. Staged bytes identical to the
embedded sources produce the embedded digest (fast path); modified bytes
produce an honestly different digest (serve logs drift instead of silently
mislabeling the version). Purely local: no store or workspace is opened.`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkflowDigest,
}

func init() {
	workflowCloneCmd.Flags().StringVar(&workflowCloneOut, "out", "", "Output source directory")
	workflowCloneCmd.Flags().BoolVar(&workflowCloneJSON, "json", false, "JSON output")
	_ = workflowCloneCmd.MarkFlagRequired("out")

	workflowBuildCmd.Flags().StringVar(&workflowBuildSource, "source", "", "Workflow source directory")
	workflowBuildCmd.Flags().BoolVar(&workflowBuildJSON, "json", false, "JSON output")
	_ = workflowBuildCmd.MarkFlagRequired("source")

	for _, cmd := range []*cobra.Command{workflowApproveCmd, workflowUnapproveCmd, workflowActivateCmd} {
		cmd.Flags().StringVar(&workflowVersionID, "version", "", "DriverVersion id")
		_ = cmd.MarkFlagRequired("version")
	}
	workflowApproveCmd.Flags().BoolVar(&workflowApproveJSON, "json", false, "JSON output")
	workflowUnapproveCmd.Flags().BoolVar(&workflowApproveJSON, "json", false, "JSON output")
	workflowActivateCmd.Flags().BoolVar(&workflowActivateJSON, "json", false, "JSON output")

	workflowRunCmd.Flags().StringVar(&workflowRunVersion, "driver-version", "", "Preview run against a specific DriverVersion id")
	workflowRunCmd.Flags().StringVar(&workflowRunEpic, "epic", "", "Epic ID to pass as input.epicId")
	workflowRunCmd.Flags().StringArrayVar(&workflowRunInput, "input", nil, "Input key=value (repeatable)")
	workflowRunCmd.Flags().BoolVar(&workflowRunJSON, "json", false, "JSON output")

	workflowListCmd.Flags().BoolVar(&workflowListJSON, "json", false, "JSON output")
	workflowVersionsCmd.Flags().BoolVar(&workflowVersionsJSON, "json", false, "JSON output")
	workflowReadyzCmd.Flags().BoolVar(&workflowReadyzJSON, "json", false, "JSON output")
	workflowDigestCmd.Flags().BoolVar(&workflowDigestJSON, "json", false, "JSON output")
	workflowDigestCmd.Flags().StringArrayVar(&workflowDigestFiles, "file", nil, "Staged source to hash as <spec-key>=<path> (repeatable; must cover the workflow's full source set)")

	workflowCmd.AddCommand(workflowCloneCmd, workflowBuildCmd, workflowApproveCmd, workflowUnapproveCmd, workflowActivateCmd, workflowRunCmd, workflowListCmd, workflowVersionsCmd, workflowReadyzCmd, workflowDigestCmd)
	cli.RegisterCommand(workflowCmd)
}

type workflowBuildOutput struct {
	OK                   bool                         `json:"ok"`
	Status               string                       `json:"status"`
	Driver               *domain.Driver               `json:"driver,omitempty"`
	Version              *domain.DriverVersion        `json:"version,omitempty"`
	Diagnostics          string                       `json:"diagnostics,omitempty"`
	Error                string                       `json:"error,omitempty"`
	ErrorClass           string                       `json:"error_class,omitempty"`
	SourceDigest         string                       `json:"source_digest,omitempty"`
	MissingPrerequisites []string                     `json:"missing_prerequisites,omitempty"`
	Source               *workflows.LocalSource       `json:"source,omitempty"`
	Runners              []driverpkg.DriverRunnerSpec `json:"runners,omitempty"`
}

type workflowVersionOutput struct {
	Version        *domain.DriverVersion   `json:"version"`
	Active         bool                    `json:"active"`
	Approved       bool                    `json:"approved"`
	EffectiveTrust domain.DriverTrustLevel `json:"effective_trust"`
}

func runWorkflowClone(_ *cobra.Command, args []string) error {
	manifest, err := workflows.CloneBuiltinSource(args[0], workflowCloneOut)
	if err != nil {
		return fmt.Errorf("clone workflow source: %w", err)
	}
	if workflowCloneJSON {
		return cmdstore.WriteJSON(map[string]any{"workflow": args[0], "out": workflowCloneOut, "manifest": manifest})
	}
	fmt.Printf("Cloned workflow %s source to %s\n", args[0], workflowCloneOut)
	fmt.Printf("Edit %s and run: loom workflow build %s --source %s\n", filepath.Join(workflowCloneOut, "workflows"), args[0], workflowCloneOut)
	return nil
}

//nolint:funlen // The cobra handler keeps build, diagnostics, and JSON response assembly in one readable path.
func runWorkflowBuild(_ *cobra.Command, args []string) error {
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		source, err := workflows.ReadLocalSource(args[0], workflowBuildSource)
		if err != nil {
			return err
		}
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve work dir: %w", err)
		}
		digest := workflows.SourceDigest(source.Files)
		missingPrereqs := workflowMissingPrerequisites(source)
		absSource, err := filepath.Abs(workflowBuildSource)
		if err != nil {
			return fmt.Errorf("resolve source dir: %w", err)
		}
		result, diagnostics, err := workflowBuildAndRegister(ctx, h.Store, workflows.BuildAndRegisterOptions{
			WorkspaceKey: ws,
			Name:         source.Manifest.DriverID,
			Entrypoint:   source.Manifest.Entrypoint,
			Files:        source.Files,
			Activate:     false,
			SourceRef:    "file://" + filepath.ToSlash(absSource) + "#" + digest,
			SourceDigest: digest,
			CreatedBy:    workflowActor(),
			WorkDir:      cwd,
			Runners:      source.Runners,
			Manifest:     workflows.SourceManifestProvenance(source.Manifest),
			Trust:        domain.DriverTrustUntrusted,
		})
		if err != nil {
			if workflowBuildJSON {
				_ = cmdstore.WriteJSON(workflowBuildOutput{
					OK:                   false,
					Status:               "failed",
					Diagnostics:          diagnostics,
					Error:                err.Error(),
					ErrorClass:           "flue_build_failed",
					SourceDigest:         digest,
					MissingPrerequisites: missingPrereqs,
					Source:               source,
					Runners:              source.Runners,
				})
			}
			return fmt.Errorf("build workflow: %w", err)
		}
		if workflowBuildJSON {
			return cmdstore.WriteJSON(workflowBuildOutput{
				OK:                   true,
				Status:               "passed",
				Driver:               result.Driver,
				Version:              result.Version,
				Diagnostics:          diagnostics,
				SourceDigest:         digest,
				MissingPrerequisites: missingPrereqs,
				Source:               source,
				Runners:              source.Runners,
			})
		}
		fmt.Printf("Built workflow %s version %s\n", result.Driver.DriverID, result.Version.VersionID)
		fmt.Printf("Source digest: %s\n", result.Version.SourceDigest)
		fmt.Printf("Activate after review: loom workflow approve %s --version %s && loom workflow activate %s --version %s\n", result.Driver.DriverID, result.Version.VersionID, result.Driver.DriverID, result.Version.VersionID)
		return nil
	})
}

func runWorkflowApprove(_ *cobra.Command, args []string) error {
	return workflowVersionAction(args[0], workflowVersionID, workflowApproveJSON, "approved", driverpkg.ApproveDriverVersion)
}

func runWorkflowUnapprove(_ *cobra.Command, args []string) error {
	return workflowVersionAction(args[0], workflowVersionID, workflowApproveJSON, "unapproved", driverpkg.UnapproveDriverVersion)
}

func runWorkflowActivate(_ *cobra.Command, args []string) error {
	return workflowVersionAction(args[0], workflowVersionID, workflowActivateJSON, "activated", driverpkg.ActivateDriverVersion)
}

func workflowVersionAction(workflow, versionID string, jsonOut bool, action string, fn func(context.Context, store.Store, string, string, string) (*domain.Driver, *domain.DriverVersion, error)) error {
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		driverID, err := workflows.ResolveDriverID(ctx, h.Store, ws, workflow)
		if err != nil {
			return fmt.Errorf("resolve workflow driver: %w", err)
		}
		driver, version, err := fn(ctx, h.Store, ws, driverID, versionID)
		if err != nil {
			return err
		}
		out := workflowVersionOutput{
			Version:        version,
			Active:         driver.ActiveVersionID == version.VersionID,
			Approved:       driverpkg.DriverVersionApproved(driver, version),
			EffectiveTrust: driverpkg.DriverVersionEffectiveTrust(driver, version),
		}
		if jsonOut {
			return cmdstore.WriteJSON(out)
		}
		fmt.Printf("%s workflow %s version %s\n", workflowActionTitle(action), driver.DriverID, version.VersionID)
		return nil
	})
}

func workflowActionTitle(action string) string {
	action = strings.TrimSpace(action)
	if action == "" {
		return ""
	}
	return strings.ToUpper(action[:1]) + action[1:]
}

func runWorkflowRun(_ *cobra.Command, args []string) error {
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		driverID, err := workflows.ResolveDriverID(ctx, h.Store, ws, args[0])
		if err != nil {
			return fmt.Errorf("resolve workflow driver: %w", err)
		}
		payload, err := workflowPayload(workflowRunInput, workflowRunEpic)
		if err != nil {
			return err
		}
		if workflowRunRequiresLocalPreflight(driverID, payload) {
			if err := runtimepreflight.PreflightLocalTaskRunner(ctx, h.Store, ws); err != nil {
				return err
			}
		}
		run, err := driverpkg.CreateDriverRun(ctx, h.Store, driverpkg.RunOptions{
			WorkspaceKey:    ws,
			DriverID:        driverID,
			DriverVersionID: workflowRunVersion,
			EpicID:          workflowRunEpic,
			Entrypoint:      driverpkg.EntrypointRun,
			SourceKind:      "cli",
			SourceRef:       "loom workflow run",
			Payload:         payload,
		})
		if err != nil {
			return fmt.Errorf("create workflow run: %w", err)
		}
		if workflowRunJSON {
			return cmdstore.WriteJSON(run)
		}
		fmt.Printf("Recorded workflow run %s (%s)\n", run.RunID, run.Status)
		fmt.Printf("Workflow: %s version %s\n", run.DriverID, run.DriverVersionID)
		return nil
	})
}

func runWorkflowList(_ *cobra.Command, _ []string) error {
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		drivers, err := h.Store.Drivers().List(ctx, ws, store.DriverFilter{})
		if err != nil {
			return err
		}
		items := []map[string]any{}
		for _, driver := range drivers {
			item := map[string]any{
				"driver_id":         driver.DriverID,
				"name":              driver.Name,
				"status":            driver.Status,
				"active_version_id": driver.ActiveVersionID,
				"built_in":          isBuiltinWorkflow(driver.DriverID),
			}
			if driver.ActiveVersionID != "" {
				if version, err := h.Store.DriverVersions().Get(ctx, ws, driver.ActiveVersionID); err == nil {
					item["approved"] = driverpkg.DriverVersionApproved(driver, version)
					item["effective_trust"] = driverpkg.DriverVersionEffectiveTrust(driver, version)
				}
			}
			items = append(items, item)
		}
		if workflowListJSON {
			return cmdstore.WriteJSON(map[string]any{"workflows": items})
		}
		for _, item := range items {
			fmt.Printf("%s\t%s\t%s\n", item["driver_id"], item["status"], item["active_version_id"])
		}
		return nil
	})
}

func runWorkflowVersions(_ *cobra.Command, args []string) error {
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		driverID, err := workflows.ResolveDriverID(ctx, h.Store, ws, args[0])
		if err != nil {
			return fmt.Errorf("resolve workflow driver: %w", err)
		}
		driver, err := h.Store.Drivers().Get(ctx, ws, driverID)
		if err != nil {
			return err
		}
		versions, err := h.Store.DriverVersions().List(ctx, ws, store.DriverVersionFilter{DriverID: driverID})
		if err != nil {
			return err
		}
		out := make([]workflowVersionOutput, 0, len(versions))
		for _, version := range versions {
			out = append(out, workflowVersionOutput{
				Version:        version,
				Active:         driver.ActiveVersionID == version.VersionID,
				Approved:       driverpkg.DriverVersionApproved(driver, version),
				EffectiveTrust: driverpkg.DriverVersionEffectiveTrust(driver, version),
			})
		}
		if workflowVersionsJSON {
			return cmdstore.WriteJSON(map[string]any{"driver_id": driverID, "versions": out})
		}
		for _, item := range out {
			active := ""
			if item.Active {
				active = "active"
			}
			fmt.Printf("%s\t%s\tapproved=%t\ttrust=%s\n", item.Version.VersionID, active, item.Approved, item.EffectiveTrust)
		}
		return nil
	})
}

func runWorkflowReadyz(_ *cobra.Command, _ []string) error {
	status := workflowReadinessStatus()
	if workflowReadyzJSON {
		return cmdstore.WriteJSON(status)
	}
	for key, value := range status {
		fmt.Printf("%s=%v\n", key, value)
	}
	return nil
}

func workflowReadinessStatus() map[string]any {
	sandboxMode := workflowSandboxMode()
	status := map[string]any{
		"node":                         commandAvailable("node"),
		"flue":                         commandAvailable("flue") || os.Getenv("LOOM_REAL_FLUE_CMD") != "" || os.Getenv("LOOM_REAL_FLUE_CMD_JSON") != "",
		"loom_sdk":                     packageRootAvailable(os.Getenv("LOOM_SDK_ROOT"), "sdk"),
		"flue_runtime":                 flueRuntimeAvailable(),
		"daytona_sdk":                  packageRootAvailable(os.Getenv("DAYTONA_SDK_ROOT"), filepath.Join("..", "flue", "node_modules", ".pnpm", "node_modules", "@daytona", "sdk")),
		"sandbox_mode":                 sandboxMode,
		"untrusted_execution_possible": sandboxMode == driverpkg.SandboxModeContainer,
	}
	status["ok"] = status["node"].(bool) && status["flue"].(bool) && status["loom_sdk"].(bool) && status["flue_runtime"].(bool)
	return status
}

func flueRuntimeAvailable() bool {
	return packageRootAvailable(os.Getenv("LOOM_FLUE_RUNTIME_ROOT"), "") ||
		packageRootAvailable(os.Getenv("FLUE_RUNTIME_ROOT"), filepath.Join("..", "flue", "packages", "runtime")) ||
		os.Getenv("FLUE_REPO") != ""
}

func workflowSandboxMode() string {
	mode := strings.TrimSpace(os.Getenv(driverpkg.SandboxModeEnvVar))
	if mode == "" {
		return driverpkg.SandboxProviderProcess
	}
	return strings.ToLower(mode)
}

func workflowMissingPrerequisites(source *workflows.LocalSource) []string {
	status := workflowReadinessStatus()
	required := []string{"node", "flue", "loom_sdk", "flue_runtime"}
	if source != nil && source.Manifest.Dependencies["@daytona/sdk"] != "" {
		required = append(required, "daytona_sdk")
	}
	missing := []string{}
	for _, name := range required {
		if ok, _ := status[name].(bool); !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

func workflowRunRequiresLocalPreflight(driverID string, payload json.RawMessage) bool {
	switch strings.TrimSpace(driverID) {
	case workflows.BuiltinEpicRunnerWorkflowName:
		runner := strings.TrimSpace(payloadRunner(payload))
		return runner == "" || runner == runtimepreflight.LocalTaskRunnerEntrypoint
	case workflows.BuiltinScoutWorkflowName:
		// The scout's analysis leaf always execs the workspace-default backend
		// CLI on the host, so the same fail-closed backend check applies.
		return true
	default:
		return false
	}
}

func payloadRunner(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var fields struct {
		Runner string `json:"runner"`
	}
	if err := json.Unmarshal(payload, &fields); err != nil {
		return ""
	}
	return fields.Runner
}

func workflowPayload(values []string, epicID string) (json.RawMessage, error) {
	out := map[string]string{}
	if strings.TrimSpace(epicID) != "" {
		out["epicId"] = strings.TrimSpace(epicID)
	}
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("input must be key=value: %q", value)
		}
		out[key] = val
	}
	if len(out) == 0 {
		return json.RawMessage(`{}`), nil
	}
	payload, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode payload: %w", err)
	}
	return payload, nil
}

func workflowActor() string {
	if actor := os.Getenv("LOOM_FLEET_ACTOR"); actor != "" {
		return actor
	}
	if actor := os.Getenv("USER"); actor != "" {
		return actor
	}
	return "loom-cli"
}

func isBuiltinWorkflow(name string) bool {
	for _, builtin := range workflows.BuiltinWorkflowNames() {
		if builtin == name {
			return true
		}
	}
	return false
}

func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func packageRootAvailable(envRoot, fallback string) bool {
	candidates := []string{}
	if strings.TrimSpace(envRoot) != "" {
		candidates = append(candidates, envRoot)
	}
	if cwd, err := os.Getwd(); err == nil && fallback != "" {
		candidates = append(candidates, filepath.Join(cwd, fallback))
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(filepath.Clean(candidate), "package.json")); err == nil {
			return true
		}
	}
	return false
}

// runWorkflowDigest prints the canonical source digest of a built-in
// workflow: over the sources compiled into this binary, or — with --file —
// over the caller's STAGED copies of those sources, so a registration attests
// the bytes it actually ships. It never opens a store or workspace, so the
// command works before any stack is up (the e2e register scripts call it to
// stamp `loom driver register --source-digest`).
func runWorkflowDigest(cmd *cobra.Command, args []string) error {
	name := strings.TrimSpace(args[0])
	spec, ok := workflows.BuiltinWorkflow(name)
	if !ok {
		return fmt.Errorf("unknown built-in workflow %q (known: %s)", name, strings.Join(workflows.BuiltinWorkflowNames(), ", "))
	}
	files := spec.Files
	if len(workflowDigestFiles) > 0 {
		staged, err := readDigestFileOverrides(spec, workflowDigestFiles)
		if err != nil {
			return err
		}
		files = staged
	}
	digest := workflows.SourceDigest(files)
	if workflowDigestJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{
			"workflow":      name,
			"source_digest": digest,
		})
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), digest)
	return nil
}

// readDigestFileOverrides resolves --file <spec-key>=<path> pairs into the
// source map to hash. The provided key set must EXACTLY equal the workflow's
// spec key set — a missing, unknown, or duplicated key would re-introduce the
// file-set drift the canonical recipe exists to prevent — while the CONTENT
// comes from the staged files, so the stamped digest attests the bytes the
// registration actually ships (not whatever this binary happens to embed).
func readDigestFileOverrides(spec workflows.Spec, pairs []string) (map[string]string, error) {
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		key, path, ok := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		path = strings.TrimSpace(path)
		if !ok || key == "" || path == "" {
			return nil, fmt.Errorf("--file must be <spec-key>=<path>, got %q", pair)
		}
		if _, known := spec.Files[key]; !known {
			return nil, fmt.Errorf("--file key %q is not part of this workflow's source set (want: %s)", key, strings.Join(sortedSpecKeys(spec), ", "))
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("duplicate --file key %q", key)
		}
		content, err := os.ReadFile(path) //nolint:gosec // operator-provided CLI path, read-only
		if err != nil {
			return nil, fmt.Errorf("read --file %s: %w", key, err)
		}
		out[key] = string(content)
	}
	if len(out) != len(spec.Files) {
		missing := make([]string, 0, len(spec.Files))
		for key := range spec.Files {
			if _, ok := out[key]; !ok {
				missing = append(missing, key)
			}
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("--file must cover the workflow's full source set; missing: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

func sortedSpecKeys(spec workflows.Spec) []string {
	keys := make([]string, 0, len(spec.Files))
	for key := range spec.Files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
