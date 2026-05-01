//go:build parity

package paritytest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec" //nolint:norawexec // parity harness must spawn real bd + fdb subprocesses
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/netutil"
	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// cliReportPath is where the Go harness writes its CLI-parity report. Kept
// next to the UI parity suite outputs so operators can triage CLI + UI
// signal from one directory.
const cliReportPath = "../../../test/parity/ui/cli-report.json"

// cliFleetOnlyReportPath is the fleet-db-only report path. It is separate
// from cliReportPath so deletion-gate runs do not overwrite comparison runs.
const cliFleetOnlyReportPath = "../../../test/parity/ui/cli-fleetdb-only-report.json"

// fdbBinaryDefault is the built fdb binary path. Overridden via FDB_BIN env
// var. We intentionally use the same /tmp/... convention as fleet-db so the
// two binaries live next to each other.
const fdbBinaryDefault = "/tmp/fdb"

// cliFixture is a CLI-specific fixture. Same spirit as fleet-db's JSON-RPC
// fixtures but each step is a pair of CLI arg vectors (bd + fdb) rather
// than a method + params blob. Captured variables propagate across steps
// via ${name} substitution (same syntax as the RPC-level fixtures) so a
// later `show ${issue_id}` step resolves to whatever ID each backend
// assigned during the prior `create`.
type cliFixture struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	Steps       []cliStep `json:"steps"`
}

// cliStep is one bd-vs-fdb CLI comparison. BdArgs + FdbArgs are the raw
// argument vectors passed to the two binaries respectively. CaptureVars
// extracts values from the per-backend stdout JSON into per-backend
// variable namespaces via jq-lite-ish dotted keys (e.g. "$.id").
type cliStep struct {
	ID                string            `json:"id"`
	Description       string            `json:"description,omitempty"`
	BdArgs            []string          `json:"bd_args"`
	FdbArgs           []string          `json:"fdb_args"`
	CaptureVars       map[string]string `json:"capture_vars,omitempty"`
	ExpectError       bool              `json:"expect_error,omitempty"`
	CompareField      string            `json:"compare_field,omitempty"`
	ExpectJSON        map[string]any    `json:"expect_json,omitempty"`
	ExpectResultCount *int              `json:"expect_result_count,omitempty"`
}

// cliHarness owns the pair of backends (bd workspace dir + fdb HTTP URL)
// and orchestrates per-step CLI invocations. Subprocess lifecycle is
// managed by t.Cleanup registered from the underlying spawners.
type cliHarness struct {
	bdDir      string
	fdbBaseURL string
	fdbWS      string
	fdbBinary  string
}

type cliFleetOnlyHarness struct {
	fdbBaseURL string
	fdbWS      string
	fdbBinary  string
}

// TestCLIParity is the flagship CLI-level harness test. It spawns a bd
// daemon and a fleet-db HTTP server (with embedded miniredis), then walks
// every cli-fixtures/*.json fixture, running the bd_args vector against
// `bd` and the fdb_args vector against `fdb` for each step. Stdout JSON
// is captured on both sides, normalized to a common shape, and diffed.
//
// Semantics (same as the RPC-level suites):
//   - infra failures (spawn, fixture load) fail the subtest
//   - diff entries are DATA, not failures — a fixture with N diffs still
//     passes the subtest; operators inspect cli-report.json for signal
//
// The aggregated report is written to test/parity/ui/cli-report.json (UI
// suite colocates its artifacts there) so the parity audit has both CLI
// and UI signal in one directory.
func TestCLIParity(t *testing.T) {
	h := spawnCLIHarness(t)

	fixtures, err := discoverCLIFixtures("testdata/cli-fixtures")
	if err != nil {
		t.Fatalf("discoverCLIFixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no CLI fixtures found under testdata/cli-fixtures — expected at least one")
	}

	report := NewReport("1.0.0", "dual_run")

	for _, path := range fixtures {
		fx, err := loadCLIFixture(path)
		if err != nil {
			t.Fatalf("loadCLIFixture(%s): %v", path, err)
		}

		t.Run(fx.ID, func(t *testing.T) {
			diffs, err := h.runCLIFixture(t.Context(), *fx)
			if err != nil {
				t.Fatalf("runCLIFixture: %v", err)
			}

			report.AddFixture(fx.ID, fx.Title, diffs, len(fx.Steps))

			t.Logf("fixture %s: %d diffs", fx.ID, len(diffs))
			for _, d := range diffs {
				fleetJSON, _ := json.Marshal(d.FleetDB)
				beadsJSON, _ := json.Marshal(d.Beads)
				t.Logf("  diff: step=%s field=%s fleet=%s beads=%s verdict=%s",
					d.StepID, d.Field, string(fleetJSON), string(beadsJSON), d.Verdict)
			}
		})
	}

	report.Finalize()
	outPath, err := resolveCLIReportPath()
	if err != nil {
		t.Fatalf("resolve cli-report path: %v", err)
	}
	if err := report.WriteJSON(outPath); err != nil {
		t.Fatalf("WriteJSON(%s): %v", outPath, err)
	}
	t.Logf("cli-parity report: fixtures=%d steps=%d diffs=%d verdict=%s path=%s",
		report.Summary.FixturesRun, report.Summary.StepsExecuted,
		report.Summary.DiffsFound, report.Verdict, outPath)
}

// TestCLIFleetDBOnly is the deletion-gate variant of TestCLIParity. It runs
// the same CLI fixture catalog against fleet-db only, with PATH stripped so any
// accidental bd/git subprocess dependency fails immediately.
func TestCLIFleetDBOnly(t *testing.T) {
	emptyPath := t.TempDir()
	t.Setenv("PATH", emptyPath)

	fixtures, err := discoverCLIFixtures("testdata/cli-fixtures")
	if err != nil {
		t.Fatalf("discoverCLIFixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no CLI fixtures found under testdata/cli-fixtures — expected at least one")
	}

	report := NewReport("1.0.0", "fleet_db_only")

	for _, path := range fixtures {
		fx, err := loadCLIFixture(path)
		if err != nil {
			t.Fatalf("loadCLIFixture(%s): %v", path, err)
		}

		t.Run(fx.ID, func(t *testing.T) {
			h := spawnCLIFleetOnlyHarness(t)
			failures, err := h.runCLIFleetOnlyFixture(t.Context(), *fx)
			if err != nil {
				t.Fatalf("runCLIFleetOnlyFixture: %v", err)
			}
			report.AddFixture(fx.ID, fx.Title, failures, len(fx.Steps))

			for _, d := range failures {
				t.Errorf("fleet-db-only failure: step=%s field=%s fleet=%v verdict=%s",
					d.StepID, d.Field, d.FleetDB, d.Verdict)
			}
		})
	}

	report.Finalize()
	outPath, err := resolveCLIFleetOnlyReportPath()
	if err != nil {
		t.Fatalf("resolve cli-fleetdb-only-report path: %v", err)
	}
	if err := report.WriteJSON(outPath); err != nil {
		t.Fatalf("WriteJSON(%s): %v", outPath, err)
	}
	t.Logf("cli-fleetdb-only report: fixtures=%d steps=%d failures=%d verdict=%s path=%s",
		report.Summary.FixturesRun, report.Summary.StepsExecuted,
		report.Summary.UnapprovedDiffs, report.Verdict, outPath)
}

// resolveCLIReportPath finds the repo root (where go.mod lives) by walking
// upward from pwd so test/parity/ui/cli-report.json resolves regardless of
// where `go test` was invoked from.
func resolveCLIReportPath() (string, error) {
	return resolveReportPath("test", "parity", "ui", "cli-report.json", cliReportPath)
}

func resolveCLIFleetOnlyReportPath() (string, error) {
	return resolveReportPath("test", "parity", "ui", "cli-fleetdb-only-report.json", cliFleetOnlyReportPath)
}

func resolveReportPath(parts ...string) (string, error) {
	fallback := parts[len(parts)-1]
	relParts := parts[:len(parts)-1]
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(append([]string{dir}, relParts...)...), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Fallback to relative path for exotic layouts.
			return fallback, nil
		}
		dir = parent
	}
}

// discoverCLIFixtures returns sorted absolute paths to every *.json file
// under dir. Sort order keeps test output deterministic.
func discoverCLIFixtures(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}

// loadCLIFixture reads a single CLI fixture JSON file.
func loadCLIFixture(path string) (*cliFixture, error) {
	data, err := os.ReadFile(path) // #nosec G304 — fixture path is under the package testdata dir
	if err != nil {
		return nil, fmt.Errorf("read cli fixture %q: %w", path, err)
	}
	var fx cliFixture
	if err := json.Unmarshal(data, &fx); err != nil {
		return nil, fmt.Errorf("parse cli fixture %q: %w", path, err)
	}
	if fx.ID == "" {
		return nil, fmt.Errorf("cli fixture %q: missing id", path)
	}
	if len(fx.Steps) == 0 {
		return nil, fmt.Errorf("cli fixture %q: no steps", path)
	}
	return &fx, nil
}

// spawnCLIHarness spins up both backends in forms that expose their
// addresses (bd workspace dir / fdb HTTP URL) so CLI subprocesses can be
// pointed at them. We don't reuse spawnBeads/spawnFleetDB verbatim because
// those return IssueBackend instances and hide the workspace dir / URL;
// instead we inline the minimum setup and register the same t.Cleanup
// pattern.
func spawnCLIHarness(t *testing.T) *cliHarness {
	t.Helper()

	// --- bd side ---
	checkBeadsPrereqs(t)
	bdDir := t.TempDir()
	initBeadsWorkspace(t, bdDir)

	daemonCmd := startBeadsDaemon(t, bdDir)

	// Wait for the daemon socket so subsequent bd subprocesses talk to the
	// same daemon instead of each spawning their own short-lived one.
	socketPath := cliSocketPath(bdDir)
	if err := waitForSocketFile(socketPath, beadsSpawnTimeout); err != nil {
		if buf, ok := daemonCmd.Stderr.(*bytes.Buffer); ok && buf.Len() > 0 {
			t.Logf("bd daemon stderr: %s", buf.String())
		}
		terminateProcess(daemonCmd, 2*time.Second)
		t.Fatalf("bd daemon did not become ready in %s: %v", beadsSpawnTimeout, err)
	}
	t.Cleanup(func() {
		terminateProcess(daemonCmd, 2*time.Second)
	})

	// --- fdb / fleet-db side ---
	fdbBinary := resolveFDBBinary(t)
	fleetBinary := fleetDBBinary(t)
	mr := startMiniRedis(t)
	port := pickFreePortOrFatal(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	cmd, logPath := startFleetDBProcess(t, fleetBinary, addr, mr.Addr())

	baseURL := "http://" + addr
	healthCtx, healthCancel := context.WithTimeout(context.Background(), fleetSpawnTimeout)
	if err := netutil.WaitForHealthz(healthCtx, baseURL, time.Second); err != nil {
		healthCancel()
		logDump, _ := os.ReadFile(logPath) // #nosec G304 — test log diagnostic
		t.Logf("fleet-db log:\n%s", string(logDump))
		terminateProcess(cmd, 3*time.Second)
		t.Fatalf("fleet-db did not become healthy in %s: %v", fleetSpawnTimeout, err)
	}
	healthCancel()
	if err := createFleetWorkspace(baseURL, defaultWorkspaceID); err != nil {
		terminateProcess(cmd, 3*time.Second)
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		terminateProcess(cmd, 3*time.Second)
	})
	// mr is registered for Close inside startMiniRedis; retain the handle
	// locally so a future diagnostic path can poke at redis state without
	// needing another spawner. Referenced once to keep the Go import live.
	_ = mr

	return &cliHarness{
		bdDir:      bdDir,
		fdbBaseURL: baseURL,
		fdbWS:      defaultWorkspaceID,
		fdbBinary:  fdbBinary,
	}
}

func spawnCLIFleetOnlyHarness(t *testing.T) *cliFleetOnlyHarness {
	t.Helper()

	fdbBinary := resolveFDBBinary(t)
	fleetBinary := fleetDBBinary(t)
	mr := startMiniRedis(t)
	port := pickFreePortOrFatal(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	cmd, logPath := startFleetDBProcess(t, fleetBinary, addr, mr.Addr())

	baseURL := "http://" + addr
	healthCtx, healthCancel := context.WithTimeout(context.Background(), fleetSpawnTimeout)
	if err := netutil.WaitForHealthz(healthCtx, baseURL, time.Second); err != nil {
		healthCancel()
		logDump, _ := os.ReadFile(logPath) // #nosec G304 — test log diagnostic
		t.Logf("fleet-db log:\n%s", string(logDump))
		terminateProcess(cmd, 3*time.Second)
		t.Fatalf("fleet-db did not become healthy in %s: %v", fleetSpawnTimeout, err)
	}
	healthCancel()
	if err := createFleetWorkspace(baseURL, defaultWorkspaceID); err != nil {
		terminateProcess(cmd, 3*time.Second)
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		terminateProcess(cmd, 3*time.Second)
	})
	_ = mr

	return &cliFleetOnlyHarness{
		fdbBaseURL: baseURL,
		fdbWS:      defaultWorkspaceID,
		fdbBinary:  fdbBinary,
	}
}

// cliSocketPath resolves the bd daemon RPC socket for the given workspace
// dir via loomcli's production rpc helper — same convention spawn.go uses.
func cliSocketPath(dir string) string {
	return rpc.ShortSocketPath(dir)
}

// waitForSocketFile polls until the socket file exists on disk or the
// timeout expires. We don't need to dial it (bd CLI does that itself);
// file existence is enough to guarantee the daemon is accepting clients.
func waitForSocketFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("socket file %q never appeared", path)
}

// resolveFDBBinary picks the fdb binary path and skips the test if the
// binary is missing. FDB_BIN overrides the default.
func resolveFDBBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("FDB_BIN")
	if binary == "" {
		binary = fdbBinaryDefault
	}
	if _, err := os.Stat(binary); err != nil {
		t.Skipf("fdb binary not found at %s (set FDB_BIN or build: cd ~/codebase/fleet-db && go build -o /tmp/fdb ./cmd/fdb): %v", binary, err)
	}
	return binary
}

// runCLIFixture executes every step in fx against both CLIs and returns
// per-step DiffEntry rows. Variables captured from bd stdout live in
// beadsVars; those captured from fdb stdout live in fleetVars. Diffs
// record both stdout values, unsubstituted variable names appear only if
// the capture selector missed.
func (h *cliHarness) runCLIFixture(ctx context.Context, fx cliFixture) ([]DiffEntry, error) {
	if len(fx.Steps) == 0 {
		return nil, fmt.Errorf("cli fixture %q: no steps", fx.ID)
	}

	beadsVars := map[string]string{}
	fleetVars := map[string]string{}

	var diffs []DiffEntry
	for _, step := range fx.Steps {
		bdArgs := substituteVarArgs(step.BdArgs, beadsVars)
		fdbArgs := substituteVarArgs(step.FdbArgs, fleetVars)

		bdStdout, bdErr := h.runBD(ctx, bdArgs)
		fdbStdout, fdbErr := h.runFDB(ctx, fdbArgs)

		// Capture variables from stdout JSON before diffing so a step that
		// produces a diff can still feed subsequent steps with its own IDs.
		if step.CaptureVars != nil {
			if bdErr == nil {
				captureVarsJSON(bdStdout, step.CaptureVars, beadsVars)
			}
			if fdbErr == nil {
				captureVarsJSON(fdbStdout, step.CaptureVars, fleetVars)
			}
		}

		stepDiffs := diffCLIOutputs(
			fx.ID, step.ID, joinCmd(bdArgs, fdbArgs),
			bdStdout, fdbStdout,
			bdErr, fdbErr,
			step.ExpectError, step.CompareField,
		)
		diffs = append(diffs, stepDiffs...)
	}
	return diffs, nil
}

func (h *cliFleetOnlyHarness) runCLIFleetOnlyFixture(ctx context.Context, fx cliFixture) ([]DiffEntry, error) {
	if len(fx.Steps) == 0 {
		return nil, fmt.Errorf("cli fixture %q: no steps", fx.ID)
	}

	fleetVars := map[string]string{}

	var failures []DiffEntry
	for _, step := range fx.Steps {
		fdbArgs := substituteVarArgs(step.FdbArgs, fleetVars)

		fdbStdout, fdbErr := h.runFDB(ctx, fdbArgs)
		if step.CaptureVars != nil && fdbErr == nil {
			captureVarsJSON(fdbStdout, step.CaptureVars, fleetVars)
		}

		if step.ExpectError {
			if fdbErr == nil {
				failures = append(failures, fleetOnlyFailure(fx.ID, step.ID, joinFleetCmd(fdbArgs), "_outcome", "expected error, got success"))
			}
			continue
		}
		if fdbErr != nil {
			failures = append(failures, fleetOnlyFailure(fx.ID, step.ID, joinFleetCmd(fdbArgs), "_outcome", describeCLIOutcome(fdbStdout, fdbErr)))
			continue
		}
		if expectsJSON(fdbArgs) || len(step.ExpectJSON) > 0 || step.ExpectResultCount != nil {
			parsed, ok := parseMaybeJSON(fdbStdout)
			if !ok {
				failures = append(failures, fleetOnlyFailure(fx.ID, step.ID, joinFleetCmd(fdbArgs), "_stdout_json", "expected JSON stdout"))
				continue
			}
			failures = append(failures, assertFleetOnlyJSON(fx.ID, step, joinFleetCmd(fdbArgs), parsed)...)
		}
	}
	return failures, nil
}

func assertFleetOnlyJSON(fixtureID string, step cliStep, method string, parsed any) []DiffEntry {
	var failures []DiffEntry
	if step.ExpectResultCount != nil {
		if got, ok := resultCount(parsed); !ok || got != *step.ExpectResultCount {
			failures = append(failures, fleetOnlyFailure(fixtureID, step.ID, method, "_result_count", map[string]any{
				"got":       got,
				"want":      *step.ExpectResultCount,
				"countable": ok,
				"stdout":    parsed,
			}))
		}
	}
	for selector, want := range step.ExpectJSON {
		got, ok := applyJSONSelector(parsed, selector)
		if !ok || !fieldsEqualNormalized(got, want) {
			failures = append(failures, fleetOnlyFailure(fixtureID, step.ID, method, selector, map[string]any{
				"got":   got,
				"want":  want,
				"found": ok,
			}))
		}
	}
	return failures
}

func resultCount(v any) (int, bool) {
	switch t := v.(type) {
	case []any:
		return len(t), true
	case map[string]any:
		for _, key := range []string{"total", "count"} {
			if n, ok := numberToInt(t[key]); ok {
				return n, true
			}
		}
		for _, key := range []string{"issues", "items", "results", "data"} {
			if arr, ok := t[key].([]any); ok {
				return len(arr), true
			}
		}
	}
	return 0, false
}

func numberToInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}

// runBD invokes `bd <args...>` in the bd workspace dir with a 30s deadline.
// Stdout is returned verbatim for parsing; non-zero exit is surfaced as an
// error with combined stdout+stderr in the message so the diff report has
// enough context to triage.
func (h *cliHarness) runBD(ctx context.Context, args []string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "bd", args...) //nolint:norawexec,gosec
	cmd.Dir = h.bdDir
	cmd.Env = append(os.Environ(), "BD_ACTOR="+parityActor)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("bd %v: %w (stderr: %s)", args, err, stderr.String())
	}
	return stdout.String(), nil
}

// runFDB invokes `fdb -server <url> -workspace <ws> -actor <actor>
// <args...>` with a 30s deadline. Global flags are always prepended so
// the fixture's `fdb_args` stays focused on the subcommand + its flags,
// mirroring the mental model operators have when invoking fdb manually.
func (h *cliHarness) runFDB(ctx context.Context, args []string) (string, error) {
	return runFDBCommand(ctx, h.fdbBinary, h.fdbBaseURL, h.fdbWS, args)
}

func (h *cliFleetOnlyHarness) runFDB(ctx context.Context, args []string) (string, error) {
	return runFDBCommand(ctx, h.fdbBinary, h.fdbBaseURL, h.fdbWS, args)
}

func runFDBCommand(ctx context.Context, binary, baseURL, workspace string, args []string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	all := append([]string{
		"-server", baseURL,
		"-workspace", workspace,
		"-actor", parityActor,
	}, args...)
	cmd := exec.CommandContext(cctx, binary, all...) //nolint:norawexec,gosec
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("fdb %v: %w (stderr: %s)", args, err, stderr.String())
	}
	return stdout.String(), nil
}

func expectsJSON(args []string) bool {
	for _, arg := range args {
		if arg == "-json" || arg == "--json" {
			return true
		}
	}
	return false
}

func joinFleetCmd(fdbArgs []string) string {
	fdb := "fdb " + strings.Join(fdbArgs, " ")
	if len(fdb) > 200 {
		return fdb[:200] + "..."
	}
	return fdb
}

func fleetOnlyFailure(fixtureID, stepID, method, field string, value any) DiffEntry {
	return DiffEntry{
		FixtureID: fixtureID,
		StepID:    stepID,
		Method:    method,
		Field:     field,
		DriftTag:  "fleet_db_only",
		FleetDB:   value,
		Verdict:   "fail",
	}
}

// joinCmd returns a human-readable shorthand for the method label on a
// DiffEntry. Format: "bd <args> | fdb <args>" truncated to a reasonable
// length so the report stays scannable.
func joinCmd(bdArgs, fdbArgs []string) string {
	bd := "bd " + strings.Join(bdArgs, " ")
	fdb := "fdb " + strings.Join(fdbArgs, " ")
	joined := bd + " | " + fdb
	if len(joined) > 200 {
		return joined[:200] + "..."
	}
	return joined
}

// cliVarPattern mirrors runner.go's varPattern but works on a single
// string (CLI args are []string so we substitute per-arg).
var cliVarPattern = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)

// substituteVarArgs replaces ${name} tokens in each arg with the per-
// backend var map. Returns a fresh slice so the fixture's original args
// are never mutated between backends.
func substituteVarArgs(args []string, vars map[string]string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = cliVarPattern.ReplaceAllStringFunc(a, func(match string) string {
			name := cliVarPattern.FindStringSubmatch(match)[1]
			if v, ok := vars[name]; ok {
				return v
			}
			return match
		})
	}
	return out
}

// captureVarsJSON extracts values from stdout into vars. Each CaptureVars
// entry maps a variable name to a dotted selector like "$.id" or "$.issues[0].id".
// Selectors that fail silently leave the variable unset — the next step
// will see an unsubstituted ${name} and diff will surface the skew.
func captureVarsJSON(stdout string, capture, vars map[string]string) {
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return
	}
	var obj any
	if err := json.Unmarshal([]byte(stdout), &obj); err != nil {
		// Not JSON — nothing to capture.
		return
	}
	for name, selector := range capture {
		val, ok := applyJSONSelector(obj, selector)
		if !ok {
			continue
		}
		switch v := val.(type) {
		case string:
			vars[name] = v
		case float64:
			vars[name] = fmt.Sprintf("%g", v)
		case bool:
			vars[name] = fmt.Sprintf("%t", v)
		default:
			b, _ := json.Marshal(v)
			vars[name] = string(b)
		}
	}
}

// applyJSONSelector resolves a jq-lite selector like "$.id" or
// "$.issues[0].id" against a parsed JSON value. The leading "$." is
// accepted and stripped; a bare "id" (no $) is also accepted. Only dotted
// paths and single-index array access are supported — anything more
// sophisticated would invite a real jq dependency which we want to avoid
// in tests.
func applyJSONSelector(obj any, selector string) (any, bool) {
	s := strings.TrimPrefix(selector, "$.")
	s = strings.TrimPrefix(s, "$")
	if s == "" {
		return obj, true
	}
	parts := tokenizeSelector(s)
	cur := obj
	for _, p := range parts {
		switch t := p.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, false
			}
			cur, ok = m[t]
			if !ok {
				return nil, false
			}
		case int:
			arr, ok := cur.([]any)
			if !ok || t < 0 || t >= len(arr) {
				return nil, false
			}
			cur = arr[t]
		}
	}
	return cur, true
}

// tokenizeSelector breaks "issues[0].id" into ["issues", 0, "id"]. Order
// is preserved; int tokens are array indices, string tokens are map keys.
func tokenizeSelector(s string) []any {
	var out []any
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			out = append(out, buf.String())
			buf.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch ch {
		case '.':
			flush()
		case '[':
			flush()
			// Walk until matching ]
			j := i + 1
			for j < len(s) && s[j] != ']' {
				j++
			}
			idxStr := s[i+1 : j]
			// Only support integers for now.
			var n int
			_, _ = fmt.Sscanf(idxStr, "%d", &n)
			out = append(out, n)
			i = j
		default:
			buf.WriteByte(ch)
		}
	}
	flush()
	return out
}

// diffCLIOutputs compares the bd + fdb stdouts (or their errors) and
// emits per-field DiffEntry rows. Design:
//
//   - both succeeded, both stdouts are JSON → field-by-field map diff
//     (skipping known-drift fields like id/timestamps, same ignore set
//     as the RPC-level runner.go)
//   - both succeeded, non-JSON → record a single "_stdout" diff if the
//     strings differ; otherwise emit nothing
//   - one succeeded, one errored, and ExpectError isn't set → one
//     "_outcome" row marking a fail verdict
//   - both errored → best-effort _stderr diff (the error strings are
//     captured in the message field)
//
// fleet_db column corresponds to fdb output; beads column to bd output.
// This convention matches the rest of the report schema.
func diffCLIOutputs(
	fixtureID, stepID, method string,
	bdOut, fdbOut string,
	bdErr, fdbErr error,
	expectError bool,
	compareField string,
) []DiffEntry {
	bdOK := bdErr == nil
	fdbOK := fdbErr == nil

	if expectError {
		// Fixture expected both CLIs to error. Success on either side is a
		// hard diff; matching errors pass.
		if bdOK && fdbOK {
			return []DiffEntry{{
				FixtureID: fixtureID,
				StepID:    stepID,
				Method:    method,
				Field:     "_outcome",
				DriftTag:  "strict",
				FleetDB:   "success",
				Beads:     "success",
				Verdict:   "fail",
			}}
		}
		return nil // both errored as expected — no diff
	}

	if bdOK != fdbOK {
		return []DiffEntry{{
			FixtureID: fixtureID,
			StepID:    stepID,
			Method:    method,
			Field:     "_outcome",
			DriftTag:  "strict",
			FleetDB:   describeCLIOutcome(fdbOut, fdbErr),
			Beads:     describeCLIOutcome(bdOut, bdErr),
			Verdict:   "fail",
		}}
	}

	if !bdOK && !fdbOK {
		// Both errored — compare stderr messages for kind of failure. We
		// record them as strict diffs only when substantively different so
		// a benign wording difference between bd's cobra and fdb's flag
		// parser doesn't drown signal.
		bdMsg := normalizeErrorMsg(bdErr.Error())
		fdbMsg := normalizeErrorMsg(fdbErr.Error())
		if bdMsg == fdbMsg {
			return nil
		}
		return []DiffEntry{{
			FixtureID: fixtureID,
			StepID:    stepID,
			Method:    method,
			Field:     "_error_msg",
			DriftTag:  "strict",
			FleetDB:   fdbMsg,
			Beads:     bdMsg,
			Verdict:   "fail",
		}}
	}

	// Both succeeded. Parse stdouts.
	bdVal, bdIsJSON := parseMaybeJSON(bdOut)
	fdbVal, fdbIsJSON := parseMaybeJSON(fdbOut)

	if !bdIsJSON || !fdbIsJSON {
		// Non-JSON comparison — only surface a diff if the whitespace-
		// normalized strings differ. Most CLI text formats diverge on
		// cosmetic details (ANSI colors, column widths) so the default
		// should be "text is too noisy to diff cleanly; use --json".
		bdT := strings.TrimSpace(bdOut)
		fdbT := strings.TrimSpace(fdbOut)
		if bdT == fdbT {
			return nil
		}
		return []DiffEntry{{
			FixtureID: fixtureID,
			StepID:    stepID,
			Method:    method,
			Field:     "_stdout_text",
			DriftTag:  "text",
			FleetDB:   truncate(fdbT, 500),
			Beads:     truncate(bdT, 500),
			Verdict:   "fail",
		}}
	}

	// If the fixture pinpointed a single field, drill down to that. We
	// unwrap one-element arrays on both sides first so selectors like
	// "$.status" work regardless of bd's list-wrapper convention.
	if compareField != "" {
		bdField, _ := applyJSONSelector(unwrapSingleton(bdVal), compareField)
		fdbField, _ := applyJSONSelector(unwrapSingleton(fdbVal), compareField)
		if fieldsEqualNormalized(bdField, fdbField) {
			return nil
		}
		return []DiffEntry{{
			FixtureID: fixtureID,
			StepID:    stepID,
			Method:    method,
			Field:     compareField,
			DriftTag:  "strict",
			FleetDB:   fdbField,
			Beads:     bdField,
			Verdict:   "fail",
		}}
	}

	// bd's `show` returns a single-element array; fdb's returns a bare
	// object. Normalize by unwrapping one-element arrays so the diff is
	// apples-to-apples.
	bdVal = unwrapSingleton(bdVal)
	fdbVal = unwrapSingleton(fdbVal)

	// Full map diff if both are objects; otherwise scalar compare.
	bdMap, bdIsObj := bdVal.(map[string]any)
	fdbMap, fdbIsObj := fdbVal.(map[string]any)
	if bdIsObj && fdbIsObj {
		return diffMapsCLI(fixtureID, stepID, method, bdMap, fdbMap)
	}

	// Both arrays — compare element counts only. Ordering drifts between
	// backends (bd sorts differently) and per-element ID drift would
	// explode the report. Counts capture the essential query correctness
	// (did the same filter match the same number of rows?).
	bdArr, bdIsArr := bdVal.([]any)
	fdbArr, fdbIsArr := fdbVal.([]any)
	if bdIsArr && fdbIsArr {
		if len(bdArr) == len(fdbArr) {
			return nil
		}
		return []DiffEntry{{
			FixtureID: fixtureID,
			StepID:    stepID,
			Method:    method,
			Field:     "_result_count",
			DriftTag:  "strict",
			FleetDB:   len(fdbArr),
			Beads:     len(bdArr),
			Verdict:   "fail",
		}}
	}

	if fieldsEqualNormalized(bdVal, fdbVal) {
		return nil
	}
	return []DiffEntry{{
		FixtureID: fixtureID,
		StepID:    stepID,
		Method:    method,
		Field:     "_stdout_json",
		DriftTag:  "strict",
		FleetDB:   fdbVal,
		Beads:     bdVal,
		Verdict:   "fail",
	}}
}

// unwrapSingleton peels a one-element array down to its sole element.
// bd's `--json show` emits `[{...}]` for single-issue reads; fdb emits
// the bare object. Both are the same information — we unify so the diff
// engine sees a single object on both sides.
func unwrapSingleton(v any) any {
	if arr, ok := v.([]any); ok && len(arr) == 1 {
		return arr[0]
	}
	return v
}

// parseMaybeJSON attempts to unmarshal s as JSON. Some CLI commands emit
// mixed plaintext+JSON; for those we return (nil, false) and fall back to
// text comparison.
func parseMaybeJSON(s string) (any, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	if s[0] != '{' && s[0] != '[' {
		return nil, false
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, false
	}
	return v, true
}

// cliIgnoredFields mirrors runner.go's ignored set but adds CLI-specific
// fields that can't match by construction: bd prefixes issue IDs with
// the workspace slug and adds audit-trail fields that fdb's slim
// projection doesn't carry.
var cliIgnoredFields = map[string]bool{
	"id":               true,
	"created_at":       true,
	"updated_at":       true,
	"closed_at":        true,
	"source_repo":      true, // bd auto-populates, fdb does not
	"dependencies":     true, // bd emits embedded list, fdb slim omits
	"dependents":       true,
	"comments":         true,
	"events":           true,
	"external_ref":     true, // bd uses, fdb ignores
	"origin_ref":       true, // bd internal
	"parent_path":      true, // bd computed
	"hash":             true, // bd content hash
	"version":          true, // bd mvcc version
	"last_state_at":    true, // bd audit
	"state_versions":   true, // bd audit
	"rig":              true, // bd workspace concept
	"prefix":           true, // bd workspace concept
	"acceptance":       true, // bd: top-level; fdb: nested field name differs
	"closed_by":        true, // differs by actor source
	"created_by":       true, // bd: git user.email; fdb: X-Actor
	"updated_by":       true, // same as above
	"ephemeral":        true, // bd-only concept
	"mol_type":         true, // bd-only concept
	"agent_rig":        true, // bd-only concept
	"role_type":        true, // bd-only concept
	"pinned":           true, // bd-only
	"estimate":         true, // bd-only
	"defer_until":      true, // format differs
	"due_at":           true, // format differs
	"claim_expires_at": true,
	"claimed_at":       true,
	"claimed_by":       true,

	// fdb-only audit/projection fields.
	"workspace": true, // fdb emits workspace key; bd has no equivalent

	// bd-only slim list extras.
	"dependency_count":   true,
	"dependent_count":    true,
	"blocked_by":         true, // bd blocked projection
	"blocked_by_count":   true,
	"blocked_by_details": true,

	// Parent-id is carried as "parent_id" on fdb and "parent" on bd. We
	// normalize them into one shared field in normalizeIssueMap rather
	// than ignore both outright.

	// fdb-only wrapper in blocked queue.
	"issue":    true, // fdb nested {issue, blockers}
	"blockers": true, // fdb nested blocker list

	// Parent ID carries backend-specific IDs (bd: 001-xyz, fdb: PARITY-3)
	// so the raw value diffs across backends by construction. Presence is
	// implicitly asserted by the capture_vars + subsequent show step.
	"parent_id": true,
	"parent":    true,
}

// cliFieldAliases maps each backend's unique field name to a canonical
// shared name. Applied before diffing so "issue_type" (bd) and "type"
// (fdb) collapse to a single row.
var cliFieldAliases = map[string]string{
	"issue_type":  "type",        // bd: issue_type; fdb: type
	"parent":      "parent_id",   // bd: parent; fdb: parent_id
	"text":        "body",        // bd comment text; fdb comment body
	"status":      "status",      // identity — kept explicit so tests make the shared set obvious
	"title":       "title",       // identity
	"priority":    "priority",    // identity
	"owner":       "owner",       // identity
	"assignee":    "assignee",    // identity
	"labels":      "labels",      // identity
	"description": "description", // identity
}

// normalizeIssueMap applies cliFieldAliases to an issue map so field-
// level diffs see the same canonical names on both sides. Also hoists
// fdb's blocked-queue `{issue: {...}, blockers: [...]}` wrapper so that
// top-level fields (title, status, etc.) line up with bd's flat shape.
// Returns a fresh map — the caller's input is not mutated.
func normalizeIssueMap(m map[string]any) map[string]any {
	// Hoist nested fdb-style `issue` wrapper first. This flattens the
	// blocked-queue shape difference documented in the flag-parity audit.
	if nested, ok := m["issue"].(map[string]any); ok {
		hoisted := make(map[string]any, len(m)+len(nested))
		for k, v := range nested {
			hoisted[k] = v
		}
		// Preserve the outer wrapper keys that aren't themselves the
		// nested issue object (the fdb wrapper has a peer `blockers` list).
		for k, v := range m {
			if k == "issue" {
				continue
			}
			if _, exists := hoisted[k]; !exists {
				hoisted[k] = v
			}
		}
		m = hoisted
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if alias, ok := cliFieldAliases[k]; ok {
			// Only alias if the canonical key isn't already in the map —
			// otherwise we'd overwrite the more-recent value with the older
			// alias form, dropping real signal.
			if _, exists := out[alias]; !exists {
				out[alias] = v
			}
			continue
		}
		out[k] = v
	}
	return out
}

// diffMapsCLI delegates to the shared DiffMaps routine with the
// CLI-level ignore set (expanded from the RPC set to absorb bd-vs-fdb
// schema drift — e.g. bd's "source_repo" doesn't appear on fdb) and a
// NormalizeMap hook that handles both the nested fdb {issue, blockers}
// hoist AND cliFieldAliases collapsing in one pass. Aliases in DiffOpts
// are deliberately empty because normalizeIssueMap already applies them.
// Uses fieldsEqualNormalized so priority strings like "P2" compare equal
// to the int 2.
//
// Note on argument order: DiffMaps's left/right slots map to FleetDB/Beads
// output columns by convention. bdMap is beads output (right), fdbMap is
// fleet-db output (left) — so we pass (fdbMap, bdMap) into DiffMaps.
func diffMapsCLI(fixtureID, stepID, method string, bdMap, fdbMap map[string]any) []DiffEntry {
	opts := DiffOpts{
		Ignored:      cliIgnoredFields,
		NormalizeMap: normalizeIssueMap,
		Equal: func(_ string, a, b any) bool {
			return fieldsEqualNormalized(a, b)
		},
	}
	return DiffMaps(opts, fixtureID, stepID, method, fdbMap, bdMap)
}

// fieldsEqualNormalized is CLI-side field equality: a superset of
// diffcore.go:defaultFieldsEqual that additionally treats empty lists as
// equal to nil (bd emits [] for empty labels, fdb pre-B1 sometimes
// omitted the key entirely) and normalizes priority strings ("P2" -> 2).
func fieldsEqualNormalized(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	// Empty slice == nil
	if isEmptySlice(a) && b == nil {
		return true
	}
	if isEmptySlice(b) && a == nil {
		return true
	}
	// Empty string == nil
	if s, ok := a.(string); ok && s == "" && b == nil {
		return true
	}
	if s, ok := b.(string); ok && s == "" && a == nil {
		return true
	}
	// Priority normalization: fdb emits int, bd sometimes emits "2" or "P2"
	if s, ok := a.(string); ok {
		if _, isNum := b.(float64); isNum {
			if n, err := parsePriorityString(s); err == nil {
				return float64(n) == b.(float64)
			}
		}
	}
	if s, ok := b.(string); ok {
		if _, isNum := a.(float64); isNum {
			if n, err := parsePriorityString(s); err == nil {
				return float64(n) == a.(float64)
			}
		}
	}
	if as, aok := a.(string); aok {
		if bs, bok := b.(string); bok {
			if timesEqual(as, bs) {
				return true
			}
		}
	}
	return reflect.DeepEqual(a, b)
}

// isEmptySlice returns true for an []any whose length is 0.
func isEmptySlice(v any) bool {
	if v == nil {
		return false
	}
	if arr, ok := v.([]any); ok {
		return len(arr) == 0
	}
	return false
}

// parsePriorityString turns "P2" / "2" into 2. Returns an error for
// non-priority strings; callers fall back to strict equality.
func parsePriorityString(s string) (int, error) {
	s = strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(s)), "P")
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func timesEqual(a, b string) bool {
	at, aok := parseCLIFieldTime(a)
	bt, bok := parseCLIFieldTime(b)
	return aok && bok && at.Equal(bt)
}

func parseCLIFieldTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// normalizeErrorMsg strips absolute paths + random IDs from error
// messages so "bd show bd-abc123: not found" matches "fdb show PARITY-abc456:
// not found" at the kind-of-error level. We intentionally keep it
// lightweight — this is enough to de-noise the report, not a parser.
func normalizeErrorMsg(msg string) string {
	msg = strings.ToLower(msg)
	// Replace likely issue IDs (word-dash-alphanum with digits).
	msg = regexp.MustCompile(`\b[a-z]+-[a-z0-9]+\b`).ReplaceAllString(msg, "<id>")
	msg = regexp.MustCompile(`\bparity-[a-z0-9]+\b`).ReplaceAllString(msg, "<id>")
	msg = regexp.MustCompile(`/tmp/[^\s]+`).ReplaceAllString(msg, "<path>")
	msg = regexp.MustCompile(`\s+`).ReplaceAllString(msg, " ")
	return strings.TrimSpace(msg)
}

// describeCLIOutcome formats a success/error label for the _outcome row.
func describeCLIOutcome(stdout string, err error) string {
	if err != nil {
		return fmt.Sprintf("error: %s", truncate(err.Error(), 200))
	}
	return fmt.Sprintf("success: %s", truncate(strings.TrimSpace(stdout), 200))
}

// truncate caps s at n characters, appending a marker when shortened.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}
