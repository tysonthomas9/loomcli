package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot returns the directory containing the Makefile under test.
// It defaults to the working directory but respects MAKEFILE_TEST_DIR if set,
// so the test can be invoked from anywhere.
func repoRoot(t *testing.T) string {
	t.Helper()
	if d := os.Getenv("MAKEFILE_TEST_DIR"); d != "" {
		return d
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	return wd
}

// runMake is a small helper that runs make with the given args in the repo root
// and returns combined stdout+stderr. It fails the test on non-zero exit.
func runMake(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("make", args...) //nolint:norawexec
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestMakefileLocalModeProjectIsCheckoutScoped(t *testing.T) {
	t.Parallel()

	first := runLocalModeInfo(t, repoRoot(t), nil)
	second := runLocalModeInfo(t, repoRoot(t), nil)
	if first["checkout_id"] == "" || first["compose_project"] == "" {
		t.Fatalf("local-mode-info missing checkout identity: %+v", first)
	}
	if first["checkout_id"] != second["checkout_id"] || first["compose_project"] != second["compose_project"] {
		t.Fatalf("same checkout produced unstable identity: first=%+v second=%+v", first, second)
	}
	if !strings.HasSuffix(first["compose_project"], first["checkout_id"]) {
		t.Fatalf("compose project %q is not scoped by checkout %q", first["compose_project"], first["checkout_id"])
	}

	other := runLocalModeInfo(t, t.TempDir(), nil)
	if other["checkout_id"] == first["checkout_id"] || other["compose_project"] == first["compose_project"] {
		t.Fatalf("different roots shared local-mode identity: first=%+v other=%+v", first, other)
	}

	overridden := runLocalModeInfo(t, t.TempDir(), map[string]string{"LOCAL_MODE_COMPOSE_PROJECT": "explicit-review-stack"})
	if overridden["compose_project"] != "explicit-review-stack" {
		t.Fatalf("explicit project override lost: %+v", overridden)
	}

	quotedRoot := filepath.Join(t.TempDir(), "reviewer's checkout")
	if err := os.MkdirAll(quotedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	quoted := runLocalModeInfo(t, quotedRoot, nil)
	if quoted["checkout_id"] == "" || !strings.HasSuffix(quoted["compose_project"], quoted["checkout_id"]) {
		t.Fatalf("quoted checkout path produced invalid identity: %+v", quoted)
	}
	explicitQuoted := runLocalModeInfo(t, repoRoot(t), map[string]string{"LOCAL_MODE_SOURCE_ROOT": quotedRoot})
	if explicitQuoted["source_root"] != quotedRoot || explicitQuoted["checkout_id"] != expectedCheckoutID(t, quotedRoot) {
		t.Fatalf("explicit quoted source root did not determine checkout identity: %+v", explicitQuoted)
	}
}

func TestMakefileLocalModeExplicitProjectPropagatesToLifecycleTargets(t *testing.T) {
	t.Parallel()

	const project = "explicit-review-stack"
	for _, target := range []string{"local-mode-up", "local-mode-verify", "local-mode-logs", "local-mode-down"} {
		target := target
		t.Run(target, func(t *testing.T) {
			out := runMake(t,
				"-n",
				"LOCAL_MODE_COMPOSE=fake-compose",
				"LOCAL_MODE_COMPOSE_PROJECT="+project,
				target,
			)
			if !strings.Contains(out, `compose="fake-compose"`) {
				t.Fatalf("%s did not select the fake Compose provider:\n%s", target, out)
			}
			if !strings.Contains(out, "-p "+project+" ") {
				t.Fatalf("%s did not propagate explicit project %q:\n%s", target, project, out)
			}
		})
	}
}

func TestMakefileLocalModeCommandLineSourceRootRequiresPairedIdentity(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "reviewer's checkout")
	cmd := exec.Command("make", "-s", "LOCAL_MODE_SOURCE_ROOT="+root, "local-mode-info") //nolint:norawexec -- verify fail-closed Make override handling
	cmd.Dir = repoRoot(t)
	// A parent `make gate` exports its resolved identity. That inherited value
	// must not count as the explicit pair for a different command-line root.
	cmd.Env = environmentWithOverrides(map[string]string{
		"LOCAL_MODE_CHECKOUT_ID": "inherited-parent-checkout",
	})
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("unpaired command-line source root unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(string(out), "requires LOCAL_MODE_CHECKOUT_ID") {
		t.Fatalf("unpaired source-root failure was not explicit:\n%s", out)
	}

	paired := runMake(t,
		"-s",
		"LOCAL_MODE_SOURCE_ROOT="+root,
		"LOCAL_MODE_CHECKOUT_ID=paired-checkout",
		"local-mode-info",
	)
	if !strings.Contains(paired, "source_root="+root) || !strings.Contains(paired, "checkout_id=paired-checkout") {
		t.Fatalf("paired command-line identity override was not preserved:\n%s", paired)
	}
}

func environmentWithOverrides(overrides map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[key]; !replaced {
			env = append(env, entry)
		}
	}
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
}

func TestDesktopSidecarChecksPackagedBuiltinsAtPhase5Owner(t *testing.T) {
	t.Parallel()

	path := filepath.Join(
		repoRoot(t),
		"desktop",
		"scripts",
		"prepare-sidecar.sh",
	)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	if !strings.Contains(
		script,
		"./internal/infra/workflowdistribution/authoring",
	) {
		t.Fatal("desktop sidecar does not test packaged builtins at the Phase 5 workflow-distribution owner")
	}
	if strings.Contains(script, "./internal/workflows") {
		t.Fatal("desktop sidecar still references the retired internal/workflows package")
	}
}

func TestMakefileLocalModeVerifyAlwaysReadsLiveManifest(t *testing.T) {
	t.Parallel()

	dryRun := runMake(t,
		"-n",
		"LOCAL_MODE_COMPOSE=fake-compose",
		"LOCAL_MODE_RUN_MANIFEST_JSON=caller-injected",
		"local-mode-verify",
	)
	for _, want := range []string{
		"exec -T loom-local sh -c",
		`manifest="${LOCAL_MODE_RUN_MANIFEST:-/tmp/loom-local-mode-run.json}"`,
		`while [ ! -s "$manifest" ]`,
		`cat "$manifest"`,
	} {
		if !strings.Contains(dryRun, want) {
			t.Fatalf("local-mode-verify did not read the container-selected manifest path; missing %q:\n%s", want, dryRun)
		}
	}

	fakeCompose := filepath.Join(t.TempDir(), "fake-compose")
	customManifest := filepath.Join(t.TempDir(), "custom-run-manifest.json")
	liveManifest := `{"checkout_id":"live-container","source_root":"/live/container","compose_project":"manifest-proof-stack","run_id":"live-run","started_at":"2026-07-15T03:00:00Z","backend":"localdogfood","workspace":"LOCALMODE","plan_task_id":"LM-1","code_task_id":"LM-2","plan_task_title":"plan","code_task_title":"code"}`
	if err := os.WriteFile(customManifest, []byte(liveManifest+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeComposeScript := `#!/bin/sh
last=""
for arg do last="$arg"; done
LOCAL_MODE_RUN_MANIFEST="${FAKE_CUSTOM_MANIFEST:?}" sh -c "$last"
`
	if err := os.WriteFile(fakeCompose, []byte(fakeComposeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command( //nolint:norawexec -- fake Compose proves Make ignores caller-supplied manifests
		"make", "-s",
		"LOCAL_MODE_COMPOSE="+fakeCompose,
		"LOCAL_MODE_CHECKOUT_ID=requested-checkout",
		"LOCAL_MODE_COMPOSE_PROJECT=manifest-proof-stack",
		"local-mode-verify",
	)
	cmd.Dir = repoRoot(t)
	cmd.Env = make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "LOCAL_MODE_RUN_MANIFEST_JSON" {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env,
		"LOCAL_MODE_RUN_MANIFEST_JSON=caller-injected",
		"FAKE_CUSTOM_MANIFEST="+customManifest,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("fake live-manifest mismatch unexpectedly verified:\n%s", out)
	}
	if !strings.Contains(string(out), "manifest checkout live-container does not match requested checkout requested-checkout") {
		t.Fatalf("caller manifest bypassed the live Compose manifest:\n%s", out)
	}
}

func TestMakefileLocalModeCodexVerifySetsExpectedBackend(t *testing.T) {
	t.Parallel()

	const project = "explicit-codex-review-stack"
	out := runMake(t,
		"-n",
		"LOCAL_MODE_COMPOSE=fake-compose",
		"LOCAL_MODE_COMPOSE_PROJECT="+project,
		"local-mode-codex-verify",
	)
	if !strings.Contains(out, "LOCAL_MODE_EXPECTED_BACKEND=codex") {
		t.Fatalf("local-mode-codex-verify did not require the Codex backend:\n%s", out)
	}
	if !strings.Contains(out, "-p "+project+" ") {
		t.Fatalf("local-mode-codex-verify did not propagate explicit project %q:\n%s", project, out)
	}
}

func TestMakefileLocalModeCodexWorkflowsUsesToolchainOverlay(t *testing.T) {
	t.Parallel()

	const project = "explicit-codex-workflow-stack"
	out := runMake(t,
		"-n",
		"LOCAL_MODE_COMPOSE=fake-compose",
		"LOCAL_MODE_COMPOSE_PROJECT="+project,
		"FLUE_SRC=/tmp/fake-flue",
		"local-mode-codex-workflows-up",
	)
	for _, want := range []string{
		"packages/cli/bin/flue.mjs",
		"-p " + project + " ",
		"-f test/local-mode/docker-compose.yml",
		"-f test/local-mode/docker-compose.codex.yml",
		"-f test/local-mode/docker-compose.workflow-build.yml",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("local-mode-codex-workflows-up output missing %q:\n%s", want, out)
		}
	}
}

func TestMakefileLocalModeWorkflowOverlayRunsToolchainPreflight(t *testing.T) {
	t.Parallel()

	out := runMake(t,
		"-n",
		"LOCAL_MODE_COMPOSE=fake-compose",
		"LOCAL_MODE_COMPOSE_FILES=test/local-mode/docker-compose.workflow-build.yml",
		"FLUE_SRC=/tmp/fake-flue",
		"local-mode-up",
	)
	preflight := strings.Index(out, "@rolldown/binding-")
	compose := strings.Index(out, "fake-compose")
	if preflight < 0 {
		t.Fatalf("workflow-build overlay did not add the toolchain preflight:\n%s", out)
	}
	if compose < 0 || preflight > compose {
		t.Fatalf("workflow-build preflight did not run before Compose:\n%s", out)
	}
}

func TestMakefileLocalModeWorkflowBuildCheckFailsBeforeCompose(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("make", "-s", "FLUE_SRC="+t.TempDir(), "local-mode-workflow-build-check") //nolint:norawexec -- exercises the Make preflight boundary
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("incomplete Flue checkout unexpectedly passed preflight:\n%s", out)
	}
	if !strings.Contains(string(out), "Flue workflow build toolchain is incomplete") ||
		!strings.Contains(string(out), "pnpm install --frozen-lockfile") ||
		!strings.Contains(string(out), "set FLUE_SRC=/path/to/flue") {
		t.Fatalf("workflow build preflight was not actionable:\n%s", out)
	}
}

func TestMakefileLocalModeDaytonaBuildCheckRequiresDaytonaSDK(t *testing.T) {
	t.Parallel()

	flueRoot := t.TempDir()
	out := runMake(t,
		"-n",
		"FLUE_SRC="+flueRoot,
		"local-mode-daytona-build-check",
	)
	for _, want := range []string{
		"node_modules/.pnpm/node_modules/@daytona/sdk/package.json",
		"Daytona SDK is missing from the pinned Flue checkout",
		"Run pnpm install in that checkout before starting the Daytona profile",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("workflow build preflight output missing %q:\n%s", want, out)
		}
	}
}

func TestMakefileLocalModeWorkflowBuildCheckRequiresContainerRolldownBinding(t *testing.T) {
	t.Parallel()

	flueRoot := t.TempDir()
	for _, rel := range []string{
		"packages/cli/bin/flue.mjs",
		"packages/cli/dist/flue.js",
		"packages/runtime/package.json",
		"packages/runtime/dist/node/index.mjs",
		"packages/runtime/node_modules/@hono/node-server/package.json",
		"packages/runtime/node_modules/hono/package.json",
		"node_modules/.pnpm/node_modules/@daytona/sdk/package.json",
		"node_modules/.pnpm/rolldown@1.0.3/node_modules/rolldown/package.json",
	} {
		path := filepath.Join(flueRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command( //nolint:norawexec -- exercises the Make preflight boundary
		"make", "-s",
		"FLUE_SRC="+flueRoot,
		"LOCAL_MODE_CONTAINER_ARCH=arm64",
		"local-mode-workflow-build-check",
	)
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("host-only Rolldown install unexpectedly passed the Linux-container preflight:\n%s", out)
	}
	for _, want := range []string{
		"Flue cannot load Rolldown inside the Linux/arm64/glibc local-mode container",
		"@rolldown/binding-linux-arm64-gnu",
		`"os":["current","linux"]`,
		`"cpu":["current","arm64"]`,
		"pnpm install --frozen-lockfile --force --filter @flue/cli... --filter @flue/runtime... --filter hello-world...",
		"Then rerun the selected local-mode target",
	} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("workflow build preflight output missing %q:\n%s", want, out)
		}
	}
}

func expectedCheckoutID(t *testing.T, sourceRoot string) string {
	t.Helper()
	cmd := exec.Command("git", "hash-object", "--stdin") //nolint:norawexec -- mirrors the Makefile's stable checkout identity contract
	cmd.Stdin = strings.NewReader(sourceRoot + "\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git hash-object: %v", err)
	}
	hash := strings.TrimSpace(string(out))
	if len(hash) < 12 {
		t.Fatalf("short git hash-object output %q", hash)
	}
	return hash[:12]
}

func runLocalModeInfo(t *testing.T, dir string, overrides map[string]string) map[string]string {
	t.Helper()
	cmd := exec.Command("make", "-s", "-f", repoRoot(t)+"/Makefile", "local-mode-info") //nolint:norawexec
	cmd.Dir = dir
	blocked := map[string]struct{}{
		"LOCAL_MODE_SOURCE_ROOT":     {},
		"LOCAL_MODE_CHECKOUT_ID":     {},
		"LOCAL_MODE_COMPOSE_PROJECT": {},
		"LOCAL_MODE_RUN_ID":          {},
	}
	cmd.Env = make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, skip := blocked[key]; !skip {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	for key, value := range overrides {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("local-mode-info failed in %s: %v\n%s", dir, err, out)
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}

// TestMakefileRemovedBeadsTargets verifies vendored beads maintenance targets
// do not remain in active Makefile surfaces.
func TestMakefileRemovedBeadsTargets(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/Makefile")
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	content := string(data)

	for _, forbidden := range []string{"install-bd", "sync-beads", "update-beads", "BEADS_REMOTE", "BEADS_BRANCH", "BEADS_PREFIX", "third_party/beads"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("Makefile still contains removed beads hook %q", forbidden)
		}
	}
}

// ---------------------------------------------------------------------------
// Dev workflow tests
// ---------------------------------------------------------------------------

// TestMakefileDevPhonyDeclarations verifies that dev and dev-check are
// declared as .PHONY targets.
func TestMakefileDevPhonyDeclarations(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/Makefile")
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	content := string(data)

	for _, target := range []string{"dev", "dev-check"} {
		found := false
		for _, line := range strings.Split(content, "\n") {
			if strings.HasPrefix(line, ".PHONY") && strings.Contains(line, target) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf(".PHONY declaration missing target %q", target)
		}
	}
}

// TestMakeHelp_IncludesDevTargets verifies that `make help` output
// mentions both dev and dev-check targets.
func TestMakeHelp_IncludesDevTargets(t *testing.T) {
	t.Parallel()

	out := runMake(t, "help")

	for _, target := range []string{"dev", "dev-check"} {
		if !strings.Contains(out, target) {
			t.Errorf("make help output missing %q\nOutput:\n%s", target, out)
		}
	}
}

// TestMakeDryRun_Dev verifies that `make -n dev` invokes the dev-check
// prerequisite and then runs ./scripts/dev.sh.
func TestMakeDryRun_Dev(t *testing.T) {
	t.Parallel()

	out := runMake(t, "-n", "dev")

	if !strings.Contains(out, "./scripts/dev.sh") {
		t.Errorf("make -n dev should reference ./scripts/dev.sh\nOutput:\n%s", out)
	}
}

// TestMakeDevCheck verifies that `make dev-check` succeeds when all
// dependencies (air, node) are present and prints a confirmation message.
func TestMakeDevCheck(t *testing.T) {
	t.Parallel()

	// This test only runs if both air and node are installed.
	// Skip gracefully if they're not present (CI may not have them).
	if _, err := exec.LookPath("air"); err != nil {
		t.Skip("air not installed, skipping dev-check test")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed, skipping dev-check test")
	}

	out := runMake(t, "dev-check")

	if !strings.Contains(out, "All dev dependencies found") {
		t.Errorf("make dev-check should print success message\nOutput:\n%s", out)
	}
}

// TestMakeDevCheckFailsWithoutAir verifies that `make dev-check` exits
// non-zero and prints an error when air is not on PATH.
func TestMakeDevCheckFailsWithoutAir(t *testing.T) {
	t.Parallel()

	// Run make dev-check with a PATH that excludes air.
	// We set PATH to only include system essentials so air won't be found.
	cmd := exec.Command("make", "dev-check") //nolint:norawexec
	cmd.Dir = repoRoot(t)
	// Use a minimal PATH that has make but likely not air
	cmd.Env = append(os.Environ(), "PATH=/usr/bin:/bin")
	out, err := cmd.CombinedOutput()
	if err == nil {
		// If air happens to be in /usr/bin or /bin, skip the test
		if strings.Contains(string(out), "All dev dependencies found") {
			t.Skip("air found on minimal PATH, cannot test failure mode")
		}
		t.Errorf("make dev-check should fail when air is not found, but it succeeded\nOutput:\n%s", out)
		return
	}

	if !strings.Contains(string(out), "air not found") {
		t.Errorf("make dev-check error output should mention 'air not found'\nOutput:\n%s", out)
	}
}

// TestMakefileDevTargetDependsOnDevCheck verifies that the dev target
// has dev-check as a prerequisite by inspecting the Makefile source.
func TestMakefileDevTargetDependsOnDevCheck(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/Makefile")
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}

	found := false
	for _, line := range strings.Split(string(data), "\n") {
		// Look for "dev: dev-check" target rule
		if strings.HasPrefix(line, "dev:") && strings.Contains(line, "dev-check") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Makefile 'dev' target should depend on 'dev-check'")
	}
}

// ---------------------------------------------------------------------------
// gate-e2e-full tests
// ---------------------------------------------------------------------------

// TestMakefileGateE2EFullPhonyDeclaration verifies that gate-e2e-full is
// declared as a .PHONY target.
func TestMakefileGateE2EFullPhonyDeclaration(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/Makefile")
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	content := string(data)

	found := false
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, ".PHONY") && strings.Contains(line, "gate-e2e-full") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf(".PHONY declaration missing target %q", "gate-e2e-full")
	}
}

// TestMakefileGateE2EFullDependsOnGateE2E verifies that the gate-e2e-full
// target has gate-e2e as a prerequisite by inspecting the Makefile source.
func TestMakefileGateE2EFullDependsOnGateE2E(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/Makefile")
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}

	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "gate-e2e-full:") && strings.Contains(line, "gate-e2e") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Makefile 'gate-e2e-full' target should depend on 'gate-e2e'")
	}
}

// TestMakefileGateE2EFullRecipe verifies that the gate-e2e-full target
// runs go test with the container build tag against the e2e package.
func TestMakefileGateE2EFullRecipe(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/Makefile")
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	content := string(data)

	checks := []struct {
		name   string
		substr string
	}{
		{"container build tag", "-tags container"},
		{"e2e package", "./e2e/"},
		{"timeout", "-timeout 15m"},
	}

	for _, c := range checks {
		if !strings.Contains(content, c.substr) {
			t.Errorf("gate-e2e-full recipe should contain %s (%q)", c.name, c.substr)
		}
	}
}

// TestMakeHelp_IncludesGateE2EFullTarget verifies that `make help` output
// mentions the gate-e2e-full target with a description of Docker container tests.
func TestMakeHelp_IncludesGateE2EFullTarget(t *testing.T) {
	t.Parallel()

	out := runMake(t, "help")

	if !strings.Contains(out, "gate-e2e-full") {
		t.Errorf("make help output missing %q\nOutput:\n%s", "gate-e2e-full", out)
	}
	// Also verify the help text distinguishes it from gate-e2e
	if !strings.Contains(out, "gate-e2e") {
		t.Errorf("make help output missing %q\nOutput:\n%s", "gate-e2e", out)
	}
}

// ---------------------------------------------------------------------------
// Worktree-safe hooks tests (loomcli-pbu1k)
// ---------------------------------------------------------------------------

// TestMakefileEnsureHooksIsPhony verifies that ensure-hooks is declared
// as a .PHONY target.
func TestMakefileEnsureHooksIsPhony(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/Makefile")
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}

	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, ".PHONY") && strings.Contains(line, "ensure-hooks") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf(".PHONY declaration missing target %q", "ensure-hooks")
	}
}

// TestMakefileDevDependsOnEnsureHooks verifies that the dev target has
// ensure-hooks as a prerequisite (not the old .git/hooks/pre-push file target).
func TestMakefileDevDependsOnEnsureHooks(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/Makefile")
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}

	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "dev:") {
			if strings.Contains(line, "ensure-hooks") {
				found = true
			}
			if strings.Contains(line, ".git/hooks") {
				t.Errorf("Makefile 'dev' target should not use .git/hooks/ as a prerequisite: %s", line)
			}
		}
	}
	if !found {
		t.Errorf("Makefile 'dev' target should depend on 'ensure-hooks'")
	}
}

// TestMakefileHooksUsesGitHooksDir verifies that the hooks target uses
// the GIT_HOOKS_DIR variable (not hardcoded .git/hooks/).
func TestMakefileHooksUsesGitHooksDir(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/Makefile")
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	content := string(data)

	// Verify GIT_HOOKS_DIR is defined with git rev-parse
	if !strings.Contains(content, "GIT_HOOKS_DIR := $(shell git rev-parse --git-path hooks)") {
		t.Errorf("Makefile should define GIT_HOOKS_DIR using git rev-parse --git-path hooks")
	}
}

// TestMakefileNoHardcodedGitHooks verifies that no recipe lines in the
// Makefile contain hardcoded .git/hooks/ paths.
func TestMakefileNoHardcodedGitHooks(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/Makefile")
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}

	for i, line := range strings.Split(string(data), "\n") {
		// Skip comment lines
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(line, ".git/hooks/") {
			t.Errorf("line %d contains hardcoded .git/hooks/ path: %s", i+1, line)
		}
	}
}

func TestPrePushHookClearsGitLocalEnv(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/scripts/hooks/pre-push")
	if err != nil {
		t.Fatalf("reading pre-push hook: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "git rev-parse --local-env-vars") {
		t.Fatalf("pre-push hook should discover all local Git env vars before running the gate")
	}
	if !strings.Contains(content, "unset \"$git_env\"") {
		t.Fatalf("pre-push hook should unset each local Git env var before running the gate")
	}
	if strings.Index(content, "git rev-parse --local-env-vars") > strings.Index(content, "make check") {
		t.Fatalf("pre-push hook should clear Git env vars before running make check")
	}
}

// ---------------------------------------------------------------------------
// .gitignore tests
// ---------------------------------------------------------------------------

// TestGitignoreIncludesTmp verifies that .gitignore contains a tmp/ entry
// to exclude air's build artifacts.
func TestGitignoreIncludesTmp(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/.gitignore")
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}

	found := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "tmp/" || trimmed == "tmp" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf(".gitignore should contain 'tmp/' entry for air build artifacts")
	}
}

// TestGitignoreIncludesNodeModules verifies that .gitignore excludes
// frontend node_modules (pre-existing requirement).
func TestGitignoreIncludesNodeModules(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/.gitignore")
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}

	if !strings.Contains(string(data), "node_modules") {
		t.Errorf(".gitignore should contain 'node_modules' entry")
	}
}

// TestAirTomlExists verifies that the .air.toml configuration file
// exists at the repo root for the dev workflow.
func TestAirTomlExists(t *testing.T) {
	t.Parallel()

	path := repoRoot(t) + "/.air.toml"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf(".air.toml should exist at repo root for dev workflow")
	}
}

// TestAirTomlTmpDir verifies that .air.toml configures tmp/ as the
// temporary build directory, matching the .gitignore entry.
func TestAirTomlTmpDir(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/.air.toml")
	if err != nil {
		t.Fatalf("reading .air.toml: %v", err)
	}

	if !strings.Contains(string(data), `tmp_dir = "tmp"`) {
		t.Errorf(`.air.toml should set tmp_dir = "tmp"`)
	}
}

// TestAirTomlBuildCmd verifies that .air.toml builds the correct binary.
func TestAirTomlBuildCmd(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/.air.toml")
	if err != nil {
		t.Fatalf("reading .air.toml: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "./cmd/loom") {
		t.Errorf(".air.toml build cmd should reference ./cmd/loom")
	}
	if !strings.Contains(content, `bin = "./tmp/loom"`) {
		t.Errorf(`.air.toml should set bin = "./tmp/loom"`)
	}
}

// TestAirTomlExcludesFrontend verifies that .air.toml excludes the
// frontend directory to avoid triggering Go rebuilds on JS changes.
func TestAirTomlExcludesFrontend(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/.air.toml")
	if err != nil {
		t.Fatalf("reading .air.toml: %v", err)
	}

	if !strings.Contains(string(data), "internal/webui/frontend") {
		t.Errorf(".air.toml should exclude internal/webui/frontend from watch")
	}
}

// ---------------------------------------------------------------------------
// Coverage threshold tests (loomcli-c3jj9.3)
// ---------------------------------------------------------------------------

// TestMakefileCheckFrontendUsesTestCoverage verifies that the check-frontend
// target's step 5 runs `npm run test:coverage` (not test:unit) to enforce
// the frontend coverage threshold.
func TestMakefileCheckFrontendUsesTestCoverage(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/Makefile")
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "npm run test:coverage") {
		t.Errorf("Makefile check-frontend should run 'npm run test:coverage', not 'test:unit'")
	}

	// Verify the step label mentions the 60% threshold.
	if !strings.Contains(content, "coverage (60% threshold)") {
		t.Errorf("Makefile check-frontend step 5 label should mention '60%% threshold'")
	}
}

// TestViteConfigCoverageThresholds verifies that vite.config.ts sets all
// four coverage threshold categories to 60.
func TestViteConfigCoverageThresholds(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/internal/webui/frontend/vite.config.ts")
	if err != nil {
		t.Fatalf("reading vite.config.ts: %v", err)
	}
	content := string(data)

	// Each threshold category must be set to 60.
	for _, category := range []string{"lines", "branches", "functions", "statements"} {
		needle := category + ": 60"
		if !strings.Contains(content, needle) {
			t.Errorf("vite.config.ts should set %s threshold to 60, expected %q in file", category, needle)
		}
	}
}

// TestGoCoverageScriptDefaultThreshold verifies that check-coverage.sh
// defaults to a 70%% threshold when no argument or env var is provided.
func TestGoCoverageScriptDefaultThreshold(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/scripts/check-coverage.sh")
	if err != nil {
		t.Fatalf("reading check-coverage.sh: %v", err)
	}
	content := string(data)

	// The script should default to 70 when COVERAGE_THRESHOLD is not set.
	if !strings.Contains(content, "COVERAGE_THRESHOLD:-70") {
		t.Errorf("check-coverage.sh should default COVERAGE_THRESHOLD to 70")
	}
}

// TestCIWorkflowHasFrontendCoverageStep verifies that the CI workflow
// includes a dedicated job that runs frontend coverage. After Phase 5 the
// check moved out of the shared coverage job into a standalone
// coverage-frontend job, so we look for the job name and the npm command.
func TestCIWorkflowHasFrontendCoverageStep(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("reading ci.yml: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "coverage-frontend:") {
		t.Errorf("ci.yml should define a 'coverage-frontend' job")
	}

	// Verify it runs npm run test:coverage
	if !strings.Contains(content, "npm run test:coverage") {
		t.Errorf("ci.yml frontend coverage job should run 'npm run test:coverage'")
	}
}

// TestCIWorkflowHasGoCoverageThreshold verifies that the CI workflow
// matches the main Go quality gate coverage threshold.
func TestCIWorkflowHasGoCoverageThreshold(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("reading ci.yml: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "check-coverage.sh coverage.out 60") {
		t.Errorf("ci.yml should invoke check-coverage.sh with threshold 60")
	}
}

// TestGitignoreIncludesFrontendCoverage verifies that .gitignore excludes
// the frontend coverage output directory.
func TestGitignoreIncludesFrontendCoverage(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/.gitignore")
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}

	found := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "internal/webui/frontend/coverage/" || trimmed == "internal/webui/frontend/coverage" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf(".gitignore should contain 'internal/webui/frontend/coverage/' entry")
	}
}

// TestFrontendPackageJsonHasTestCoverageScript verifies that package.json
// defines a test:coverage script that runs vitest with --coverage.
func TestFrontendPackageJsonHasTestCoverageScript(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/internal/webui/frontend/package.json")
	if err != nil {
		t.Fatalf("reading package.json: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, `"test:coverage"`) {
		t.Errorf("package.json should define a test:coverage script")
	}
	if !strings.Contains(content, "vitest run --coverage") {
		t.Errorf("package.json test:coverage should run 'vitest run --coverage'")
	}
}

// TestDevShExists verifies that scripts/dev.sh exists and is executable.
func TestDevShExists(t *testing.T) {
	t.Parallel()

	path := repoRoot(t) + "/scripts/dev.sh"
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		t.Fatalf("scripts/dev.sh should exist")
	}
	if err != nil {
		t.Fatalf("stat scripts/dev.sh: %v", err)
	}

	// Check executable bit (owner)
	if info.Mode()&0100 == 0 {
		t.Errorf("scripts/dev.sh should be executable (mode: %o)", info.Mode())
	}
}

// TestAirToml_NoLegacyDevFlag verifies that .air.toml's args_bin line no
// longer passes the legacy --dev flag and instead passes --frontend-url
// pointing at the local Vite dev server.
func TestAirToml_NoLegacyDevFlag(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/.air.toml")
	if err != nil {
		t.Fatalf("reading .air.toml: %v", err)
	}

	var argsLine string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "args_bin") {
			argsLine = line
			break
		}
	}
	if argsLine == "" {
		t.Fatalf(".air.toml should contain an args_bin line")
	}
	if strings.Contains(argsLine, `"--dev"`) {
		t.Errorf("args_bin should not contain legacy --dev flag: %s", argsLine)
	}
	if !strings.Contains(argsLine, "--frontend-url") {
		t.Errorf("args_bin should contain --frontend-url: %s", argsLine)
	}
	if !strings.Contains(argsLine, "http://localhost:3000") {
		t.Errorf("args_bin should contain http://localhost:3000: %s", argsLine)
	}
}

// TestMakeDryRun_DevLoom_Deprecated verifies that `make -n dev-loom` prints
// a deprecation warning and still invokes ./scripts/dev.sh.
func TestMakeDryRun_DevLoom_Deprecated(t *testing.T) {
	t.Parallel()

	out := runMake(t, "-n", "dev-loom")

	if !strings.Contains(strings.ToLower(out), "deprecated") {
		t.Errorf("make -n dev-loom should mention 'deprecated'\nOutput:\n%s", out)
	}
	if !strings.Contains(out, "./scripts/dev.sh") {
		t.Errorf("make -n dev-loom should reference ./scripts/dev.sh\nOutput:\n%s", out)
	}
}

// TestMakeDryRun_DevVite_Deprecated verifies that `make -n dev-vite` prints
// a deprecation warning and still invokes ./scripts/dev.sh.
func TestMakeDryRun_DevVite_Deprecated(t *testing.T) {
	t.Parallel()

	out := runMake(t, "-n", "dev-vite")

	if !strings.Contains(strings.ToLower(out), "deprecated") {
		t.Errorf("make -n dev-vite should mention 'deprecated'\nOutput:\n%s", out)
	}
	if !strings.Contains(out, "./scripts/dev.sh") {
		t.Errorf("make -n dev-vite should reference ./scripts/dev.sh\nOutput:\n%s", out)
	}
}

// TestRunWebUiWithLoomScript_Deleted verifies that the legacy
// scripts/run-web-ui-with-loom.sh script has been removed.
func TestRunWebUiWithLoomScript_Deleted(t *testing.T) {
	t.Parallel()

	path := repoRoot(t) + "/scripts/run-web-ui-with-loom.sh"
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("scripts/run-web-ui-with-loom.sh should not exist (err=%v)", err)
	}
}

// TestDockerComposeDevYml_Exists verifies that docker-compose.dev.yml exists
// at repo root and defines the expected services and profile.
func TestDockerComposeDevYml_Exists(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/docker-compose.dev.yml")
	if err != nil {
		t.Fatalf("reading docker-compose.dev.yml: %v", err)
	}
	content := string(data)

	for _, needle := range []string{"services:", "server:", "frontend:", "redis:", "profiles:", "fleet"} {
		if !strings.Contains(content, needle) {
			t.Errorf("docker-compose.dev.yml should contain %q", needle)
		}
	}
}
