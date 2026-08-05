package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultManifest = "test/modular-monolith/supervisor-disabled-matrix.yaml"
	matrixSchema    = 1
	matrixSuite     = "supervisor-disabled"
)

var (
	errProofFailed = errors.New("supervisor-disabled proof failed")
	requiredEnv    = map[string]string{
		"LOOM_LOCAL_MODE_PLANE":  "ts",
		"LOOM_TASK_READY_EVENTS": "1",
	}
	rowEnvironmentKeys = []string{
		"LOCAL_MODE_COMPOSE_FILES",
		"LOCAL_MODE_COMPOSE_UP_FLAGS",
		"LOOM_LOCAL_MODE_PLANE",
		"LOOM_TASK_READY_EVENTS",
	}
	// hostEnvironmentAllowlist is the complete ambient environment inherited by
	// proof commands. Runtime selectors such as LOOM_*, LOCAL_MODE_*, COMPOSE_*,
	// container hosts, ports, and source roots must come from the reviewed row or
	// from repository defaults, never from the shell that launched the proof.
	hostEnvironmentAllowlist = []string{
		"APPDATA",
		"COMSPEC",
		"HOME",
		"LANG",
		"LC_ALL",
		"LC_CTYPE",
		"LOCALAPPDATA",
		"LOGNAME",
		"PATH",
		"PATHEXT",
		"SHELL",
		"SystemRoot",
		"TEMP",
		"TERM",
		"TMP",
		"TMPDIR",
		"USER",
		"USERPROFILE",
		"WINDIR",
		"XDG_RUNTIME_DIR",
	}
	requiredAssertions = []string{
		"local-mode-plane-ts",
		"task-ready-events-enabled",
		"zero-auto-agentdefs",
		"zero-daemon-processes",
		"zero-daemon-sockets",
		"public-api-plan-agent",
		"public-api-task-agent",
		"planner-review-design",
		"coder-completion",
		"planner-transcript",
		"coder-transcript",
		"coder-diff",
	}
)

type matrix struct {
	SchemaVersion int    `yaml:"schema_version"`
	Suite         string `yaml:"suite"`
	Rows          []row  `yaml:"rows"`
}

type row struct {
	ID          string            `yaml:"id"`
	Phase       string            `yaml:"phase"`
	State       string            `yaml:"state"`
	Owner       string            `yaml:"owner"`
	Blocker     string            `yaml:"blocker,omitempty"`
	Coordinates coordinates       `yaml:"coordinates"`
	Env         map[string]string `yaml:"env"`
	Setup       []step            `yaml:"setup"`
	Verify      []step            `yaml:"verify"`
	Teardown    []step            `yaml:"teardown"`
	Assertions  []string          `yaml:"assertions"`
}

type coordinates struct {
	Depth        string `yaml:"depth"`
	Realness     string `yaml:"realness"`
	Provisioning string `yaml:"provisioning"`
	Polarity     string `yaml:"polarity"`
	Target       string `yaml:"target"`
}

type step struct {
	ID             string   `yaml:"id"`
	Argv           []string `yaml:"argv"`
	TimeoutSeconds int      `yaml:"timeout_seconds"`
}

type executor interface {
	Run(context.Context, string, step, map[string]string, io.Writer, io.Writer) error
}

type osExecutor struct{}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr, osExecutor{})
	if err == nil {
		return
	}
	if !errors.Is(err, errProofFailed) {
		writeReport(os.Stderr, "[supervisor-disabled] ERROR %v\n", err)
	}
	os.Exit(1)
}

func run(ctx context.Context, args []string, out, errOut io.Writer, commandExecutor executor) error {
	flags := flag.NewFlagSet("supervisordisabled", flag.ContinueOnError)
	flags.SetOutput(errOut)
	manifestPath := flags.String("manifest", defaultManifest, "path to the supervisor-disabled matrix")
	validateOnly := flags.Bool("validate", false, "validate the matrix without running any rows")
	fleetDBSourceRoot := flags.String("fleetdb-source-root", "", "explicit paired fleet-db source root")
	fleetDBPort := flags.String("fleetdb-port", "", "isolated host FleetDB port")
	apiPort := flags.String("api-port", "", "isolated host Loom API port")
	uiPort := flags.String("ui-port", "", "isolated host Loom UI port")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("usage: supervisordisabled [--manifest path] [--validate]")
	}

	m, err := loadMatrix(*manifestPath)
	if err != nil {
		return fmt.Errorf("load matrix: %w", err)
	}
	if err := validateMatrix(m); err != nil {
		return fmt.Errorf("validate matrix: %w", err)
	}
	if *validateOnly {
		writeReport(out, "[supervisor-disabled] validation ok suite=%s rows=%d\n", m.Suite, len(m.Rows))
		return nil
	}
	overrides, err := proofEnvironmentOverrides(*fleetDBSourceRoot, *fleetDBPort, *apiPort, *uiPort)
	if err != nil {
		return err
	}
	for index := range m.Rows {
		for key, value := range overrides {
			m.Rows[index].Env[key] = value
		}
	}
	if commandExecutor == nil {
		return errors.New("command executor is required")
	}
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}
	return executeMatrix(ctx, m, workDir, out, errOut, commandExecutor)
}

func proofEnvironmentOverrides(fleetDBSourceRoot, fleetDBPort, apiPort, uiPort string) (map[string]string, error) {
	overrides := make(map[string]string, 4)
	if sourceRoot := strings.TrimSpace(fleetDBSourceRoot); sourceRoot != "" {
		info, err := os.Stat(sourceRoot)
		if err != nil {
			return nil, fmt.Errorf("fleetdb source root: %w", err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("fleetdb source root %q is not a directory", sourceRoot)
		}
		overrides["LOCAL_MODE_FLEETDB_SOURCE_ROOT"] = sourceRoot
	}
	ports := []struct {
		key   string
		value string
	}{
		{"LOCAL_MODE_FLEETDB_PORT", fleetDBPort},
		{"LOCAL_MODE_API_PORT", apiPort},
		{"LOCAL_MODE_UI_PORT", uiPort},
	}
	seen := make(map[string]string, len(ports))
	for _, port := range ports {
		value := strings.TrimSpace(port.value)
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1024 || parsed > 65535 {
			return nil, fmt.Errorf("%s must be an integer port between 1024 and 65535", port.key)
		}
		if prior, exists := seen[value]; exists {
			return nil, fmt.Errorf("%s and %s must use different ports", prior, port.key)
		}
		seen[value] = port.key
		overrides[port.key] = value
	}
	return overrides, nil
}

func loadMatrix(path string) (matrix, error) {
	file, err := os.Open(path) //nolint:gosec // The operator selects a local manifest; strict decoding validates its contents.
	if err != nil {
		return matrix{}, err
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	var m matrix
	if err := decoder.Decode(&m); err != nil {
		return matrix{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return matrix{}, errors.New("multiple YAML documents are not supported")
		}
		return matrix{}, err
	}
	return m, nil
}

func validateMatrix(m matrix) error {
	if m.SchemaVersion != matrixSchema {
		return fmt.Errorf("schema_version must be %d, got %d", matrixSchema, m.SchemaVersion)
	}
	if m.Suite != matrixSuite {
		return fmt.Errorf("suite must be %q, got %q", matrixSuite, m.Suite)
	}
	if len(m.Rows) == 0 {
		return errors.New("at least one row is required")
	}

	rowIDs := make(map[string]struct{}, len(m.Rows))
	authoritativeRowFound := false
	for rowIndex, candidate := range m.Rows {
		label := fmt.Sprintf("row[%d]", rowIndex)
		if candidate.ID == "" {
			return fmt.Errorf("%s id is required", label)
		}
		if _, exists := rowIDs[candidate.ID]; exists {
			return fmt.Errorf("duplicate row id %q", candidate.ID)
		}
		rowIDs[candidate.ID] = struct{}{}
		if err := validateRow(candidate); err != nil {
			return err
		}
		if candidate.ID == "deterministic-plan-coder" {
			authoritativeRowFound = true
			if err := validateDeterministicPlanCoderContract(candidate); err != nil {
				return err
			}
		}
	}
	if !authoritativeRowFound {
		return errors.New(`required authoritative row "deterministic-plan-coder" is missing`)
	}
	return nil
}

func validateDeterministicPlanCoderContract(candidate row) error {
	const label = `row "deterministic-plan-coder"`
	if candidate.Phase != "execution" {
		return fmt.Errorf("%s phase must be execution, got %q", label, candidate.Phase)
	}
	if candidate.Owner != "execution-reliability-lane" {
		return fmt.Errorf("%s owner must be execution-reliability-lane, got %q", label, candidate.Owner)
	}
	wantCoordinates := coordinates{
		Depth: "end-to-end", Realness: "deterministic", Provisioning: "compose",
		Polarity: "positive-and-negative", Target: "loom-serve-ts-plane",
	}
	if candidate.Coordinates != wantCoordinates {
		return fmt.Errorf("%s coordinates drifted: got %+v, want %+v", label, candidate.Coordinates, wantCoordinates)
	}
	wantEnv := map[string]string{
		"LOOM_LOCAL_MODE_PLANE":       "ts",
		"LOOM_TASK_READY_EVENTS":      "1",
		"LOCAL_MODE_COMPOSE_FILES":    "test/local-mode/docker-compose.workflow-build.yml",
		"LOCAL_MODE_COMPOSE_UP_FLAGS": "--build -d",
	}
	if len(candidate.Env) != len(wantEnv) {
		return fmt.Errorf("%s env contract has %d entries, want exactly %d", label, len(candidate.Env), len(wantEnv))
	}
	for key, want := range wantEnv {
		if got := candidate.Env[key]; got != want {
			return fmt.Errorf("%s env %s must be %q, got %q", label, key, want, got)
		}
	}
	if err := validateExactStage(label, "setup", candidate.Setup, []step{
		{ID: "clean-project", Argv: []string{"make", "local-mode-down"}, TimeoutSeconds: 180},
		{ID: "workflow-toolchain", Argv: []string{"make", "local-mode-workflow-build-check"}, TimeoutSeconds: 120},
		{ID: "stack", Argv: []string{"make", "local-mode-up"}, TimeoutSeconds: 900},
	}); err != nil {
		return err
	}
	if err := validateExactStage(label, "verify", candidate.Verify, []step{
		{ID: "deterministic-evidence", Argv: []string{"make", "local-mode-verify"}, TimeoutSeconds: 300},
	}); err != nil {
		return err
	}
	return validateExactStage(label, "teardown", candidate.Teardown, []step{
		{ID: "cleanup-stack", Argv: []string{"make", "local-mode-down"}, TimeoutSeconds: 180},
	})
}

func validateExactStage(label, stage string, got, want []step) error {
	if len(got) != len(want) {
		return fmt.Errorf("%s %s contract has %d steps, want %d", label, stage, len(got), len(want))
	}
	for index := range want {
		if got[index].ID != want[index].ID || got[index].TimeoutSeconds != want[index].TimeoutSeconds ||
			!slices.Equal(got[index].Argv, want[index].Argv) {
			return fmt.Errorf("%s %s[%d] contract drifted: got %+v, want %+v", label, stage, index, got[index], want[index])
		}
	}
	return nil
}

func validateRow(candidate row) error {
	label := fmt.Sprintf("row %q", candidate.ID)
	if candidate.Phase == "" {
		return fmt.Errorf("%s phase is required", label)
	}
	if candidate.State != "red" && candidate.State != "green" {
		return fmt.Errorf("%s state must be red or green, got %q", label, candidate.State)
	}
	if candidate.Owner == "" {
		return fmt.Errorf("%s owner is required", label)
	}
	if candidate.State == "red" && candidate.Blocker == "" {
		return fmt.Errorf("%s declared red requires a blocker", label)
	}
	if candidate.State == "green" && candidate.Blocker != "" {
		return fmt.Errorf("%s declared green cannot retain a blocker", label)
	}
	if err := validateCoordinates(label, candidate.Coordinates); err != nil {
		return err
	}
	if err := validateExplicitEnvironment(label, candidate.Env); err != nil {
		return err
	}
	for _, key := range []string{"LOOM_LOCAL_MODE_PLANE", "LOOM_TASK_READY_EVENTS"} {
		want := requiredEnv[key]
		if got := candidate.Env[key]; got != want {
			return fmt.Errorf("%s env %s must be %q, got %q", label, key, want, got)
		}
	}
	if err := validateAssertions(label, candidate.Assertions); err != nil {
		return err
	}
	if len(candidate.Setup) == 0 || len(candidate.Verify) == 0 || len(candidate.Teardown) == 0 {
		return fmt.Errorf("%s requires setup, verify, and teardown commands", label)
	}
	return validateSteps(label, candidate)
}

func validateExplicitEnvironment(label string, environment map[string]string) error {
	for key, value := range environment {
		if key == "" || key != strings.TrimSpace(key) || strings.ContainsAny(key, "=\x00") {
			return fmt.Errorf("%s env key %q must be a non-empty trimmed name without '=' or NUL", label, key)
		}
		if strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("%s env %s value must not contain NUL", label, key)
		}
		if !slices.Contains(rowEnvironmentKeys, key) {
			return fmt.Errorf("%s env key %q is not supported by the clean-environment contract", label, key)
		}
	}
	return nil
}

func validateSteps(label string, candidate row) error {
	stepIDs := make(map[string]struct{})
	for _, stage := range []struct {
		name  string
		steps []step
	}{
		{name: "setup", steps: candidate.Setup},
		{name: "verify", steps: candidate.Verify},
		{name: "teardown", steps: candidate.Teardown},
	} {
		for stepIndex, command := range stage.steps {
			stepLabel := fmt.Sprintf("%s %s[%d]", label, stage.name, stepIndex)
			if err := validateStep(stepLabel, label, command, stepIDs); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateStep(stepLabel, rowLabel string, command step, stepIDs map[string]struct{}) error {
	if command.ID == "" {
		return fmt.Errorf("%s id is required", stepLabel)
	}
	if _, exists := stepIDs[command.ID]; exists {
		return fmt.Errorf("%s has duplicate step id %q", rowLabel, command.ID)
	}
	stepIDs[command.ID] = struct{}{}
	if len(command.Argv) == 0 || command.Argv[0] == "" {
		return fmt.Errorf("%s argv executable is required", stepLabel)
	}
	for argIndex, arg := range command.Argv {
		if arg == "" {
			return fmt.Errorf("%s argv[%d] cannot be empty", stepLabel, argIndex)
		}
	}
	if command.TimeoutSeconds <= 0 {
		return fmt.Errorf("%s timeout_seconds must be positive", stepLabel)
	}
	return nil
}

func validateCoordinates(label string, value coordinates) error {
	fields := map[string]string{
		"depth":        value.Depth,
		"realness":     value.Realness,
		"provisioning": value.Provisioning,
		"polarity":     value.Polarity,
		"target":       value.Target,
	}
	for _, name := range []string{"depth", "realness", "provisioning", "polarity", "target"} {
		if fields[name] == "" {
			return fmt.Errorf("%s coordinate %s is required", label, name)
		}
	}
	return nil
}

func validateAssertions(label string, assertions []string) error {
	supported := make(map[string]struct{}, len(requiredAssertions))
	for _, assertion := range requiredAssertions {
		supported[assertion] = struct{}{}
	}
	seen := make(map[string]struct{}, len(assertions))
	for _, assertion := range assertions {
		if _, ok := supported[assertion]; !ok {
			return fmt.Errorf("%s has unsupported assertion %q", label, assertion)
		}
		if _, duplicate := seen[assertion]; duplicate {
			return fmt.Errorf("%s has duplicate assertion %q", label, assertion)
		}
		seen[assertion] = struct{}{}
	}
	for _, assertion := range requiredAssertions {
		if _, ok := seen[assertion]; !ok {
			return fmt.Errorf("%s is missing required assertion %q", label, assertion)
		}
	}
	return nil
}

func executeMatrix(ctx context.Context, m matrix, workDir string, out, errOut io.Writer, commandExecutor executor) error {
	green := 0
	red := 0
	failed := 0
	for _, candidate := range m.Rows {
		if candidate.State == "red" {
			red++
			writeReport(out, "[supervisor-disabled] RED row=%s owner=%s\n", candidate.ID, candidate.Owner)
			writeReport(out, "[supervisor-disabled] blocker=%s\n", candidate.Blocker)
			writeReport(out, "[supervisor-disabled] setup=not-run verify=not-run reason=declared-red\n")
			continue
		}
		if err := executeGreenRow(ctx, candidate, workDir, out, errOut, commandExecutor); err != nil {
			failed++
			writeReport(out, "[supervisor-disabled] FAIL row=%s\n", candidate.ID)
			continue
		}
		green++
		writeReport(out, "[supervisor-disabled] GREEN row=%s owner=%s\n", candidate.ID, candidate.Owner)
	}
	writeReport(out, "[supervisor-disabled] summary green=%d red=%d failed=%d total=%d\n", green, red, failed, len(m.Rows))
	if red != 0 || failed != 0 {
		return errProofFailed
	}
	return nil
}

func executeGreenRow(ctx context.Context, candidate row, workDir string, out, errOut io.Writer, commandExecutor executor) error {
	primaryErr := executeStage(ctx, candidate, "setup", candidate.Setup, false, workDir, out, errOut, commandExecutor)
	if primaryErr == nil {
		primaryErr = executeStage(ctx, candidate, "verify", candidate.Verify, false, workDir, out, errOut, commandExecutor)
	}

	// Teardown uses fresh per-step deadlines so a canceled proof still cleans up.
	teardownErr := executeStage(context.Background(), candidate, "teardown", candidate.Teardown, true, workDir, out, errOut, commandExecutor)
	return errors.Join(primaryErr, teardownErr)
}

func executeStage(parent context.Context, candidate row, stageName string, steps []step, continueOnError bool, workDir string, out, errOut io.Writer, commandExecutor executor) error {
	var stageErr error
	for _, command := range steps {
		writeReport(out, "[supervisor-disabled] RUN row=%s stage=%s step=%s timeout_seconds=%d\n", candidate.ID, stageName, command.ID, command.TimeoutSeconds)
		stepCtx, cancel := context.WithTimeout(parent, time.Duration(command.TimeoutSeconds)*time.Second)
		err := commandExecutor.Run(stepCtx, workDir, command, candidate.Env, out, errOut)
		cancel()
		if err == nil {
			writeReport(out, "[supervisor-disabled] PASS row=%s stage=%s step=%s\n", candidate.ID, stageName, command.ID)
			continue
		}
		stageErr = errors.Join(stageErr, err)
		writeStepFailure(out, candidate.ID, stageName, command, err)
		if !continueOnError {
			break
		}
	}
	return stageErr
}

func writeStepFailure(out io.Writer, rowID, stageName string, command step, err error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		writeReport(out, "[supervisor-disabled] FAIL row=%s stage=%s step=%s reason=timeout timeout_seconds=%d\n", rowID, stageName, command.ID, command.TimeoutSeconds)
	case errors.Is(err, context.Canceled):
		writeReport(out, "[supervisor-disabled] FAIL row=%s stage=%s step=%s reason=canceled\n", rowID, stageName, command.ID)
	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			writeReport(out, "[supervisor-disabled] FAIL row=%s stage=%s step=%s reason=exit exit_code=%d\n", rowID, stageName, command.ID, exitErr.ExitCode())
			return
		}
		writeReport(out, "[supervisor-disabled] FAIL row=%s stage=%s step=%s reason=error detail=%q\n", rowID, stageName, command.ID, err.Error())
	}
}

func writeReport(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}

func (osExecutor) Run(ctx context.Context, workDir string, command step, env map[string]string, stdout, stderr io.Writer) error {
	cmd := exec.Command(command.Argv[0], command.Argv[1:]...) //nolint:gosec // The checked-in matrix is the explicit command allowlist.
	cmd.Dir = workDir
	cmd.Env = cleanEnvironment(workDir, env)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	configureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = terminateProcessGroup(cmd)
		<-done
		return ctx.Err()
	}
}

// cleanEnvironment implements the runner's clean-env contract. Unlisted host
// variables are absent (equivalent to an explicit unset); reviewed row values
// are authoritative over the small host-runtime allowlist, while PWD remains
// runner-owned so commands cannot resolve a different checkout.
func cleanEnvironment(workDir string, explicit map[string]string) []string {
	values := make(map[string]string)
	for _, key := range hostEnvironmentAllowlist {
		if value, ok := os.LookupEnv(key); ok {
			values[key] = value
		}
	}
	for key, value := range explicit {
		values[key] = value
	}
	if workDir != "" {
		values["PWD"] = workDir
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}
