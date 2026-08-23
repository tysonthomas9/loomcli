package workflows

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/workflows/authoringkit"
	"github.com/tysonthomas9/loomcli/internal/workflows/packaged"
)

// clearAuthoringOverrides blanks every env var hasAuthoringOverride consults so a
// resolver test exercises the kit/developer precedence rather than an ambient
// override leaking in from CI (e.g. FLUE_REPO).
func clearAuthoringOverrides(t *testing.T) {
	t.Helper()
	for _, k := range []string{"LOOM_REAL_FLUE_CMD_JSON", "LOOM_REAL_FLUE_CMD", "LOOM_SDK_ROOT", "LOOM_FLUE_RUNTIME_ROOT", "FLUE_RUNTIME_ROOT", "FLUE_REPO", "DAYTONA_SDK_ROOT"} {
		t.Setenv(k, "")
	}
}

// TestPackagedModeFailsClosedOnMissingKit is the H2 proof: in a packaged build
// (desktop runtime) a missing/invalid kit surfaces the kit's own error and never
// falls through to a developer PATH toolchain.
func TestPackagedModeFailsClosedOnMissingKit(t *testing.T) {
	clearAuthoringOverrides(t)
	t.Setenv("LOOM_LOCAL_RUNTIME", "desktop") // packaged.FailClosed() == true
	t.Setenv("LOOM_AUTHORING_KIT_DIR", filepath.Join(t.TempDir(), "no-such-kit"))
	authoringkit.ResetForTest()
	t.Cleanup(authoringkit.ResetForTest)

	_, err := ResolveAuthoringToolchain()
	if err == nil {
		t.Fatal("packaged build resolved a toolchain with no kit; must fail closed")
	}
	if !errors.Is(err, authoringkit.ErrMissing) {
		t.Fatalf("packaged build should surface the kit-missing error, got %v", err)
	}
}

// TestDeveloperModeResolvesWithoutKit confirms the non-packaged path still falls
// through to the developer toolchain when no kit is present (it may then fail on
// a missing Node/Flue, but it must NOT fail with the kit error).
func TestDeveloperModeResolvesWithoutKit(t *testing.T) {
	clearAuthoringOverrides(t)
	t.Setenv("LOOM_LOCAL_RUNTIME", "") // not packaged
	t.Setenv("LOOM_AUTHORING_KIT_DIR", filepath.Join(t.TempDir(), "no-such-kit"))
	authoringkit.ResetForTest()
	t.Cleanup(authoringkit.ResetForTest)

	_, err := ResolveAuthoringToolchain()
	if err != nil && errors.Is(err, authoringkit.ErrMissing) {
		t.Fatalf("developer mode must not fail closed on the kit error: %v", err)
	}
}

// TestOverrideWinsOverKit is the DoD "override-wins-over-kit" case: when an
// explicit developer override is set, ResolveAuthoringToolchain uses it and never
// consults the kit — even with LOOM_AUTHORING_KIT_DIR configured — because
// hasAuthoringOverride short-circuits before authoringkit.Lookup().
func TestOverrideWinsOverKit(t *testing.T) {
	clearAuthoringOverrides(t)
	t.Setenv("LOOM_AUTHORING_KIT_DIR", t.TempDir())
	authoringkit.ResetForTest()
	t.Cleanup(authoringkit.ResetForTest)

	runtimeRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(runtimeRoot, "package.json"), []byte(`{"name":"@flue/runtime"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", `["node","/nonexistent/flue.mjs"]`)
	t.Setenv("LOOM_SDK_ROOT", t.TempDir())
	t.Setenv("FLUE_RUNTIME_ROOT", runtimeRoot)

	tc, err := ResolveAuthoringToolchain()
	if err != nil {
		if strings.Contains(err.Error(), "node_runtime_missing") {
			t.Skip("node unavailable; cannot exercise override precedence")
		}
		t.Fatalf("override resolve failed: %v", err)
	}
	if tc.Source != "override" {
		t.Fatalf("Source = %q, want override (an explicit override must win over any kit)", tc.Source)
	}
}

// TestDeveloperNodeMissingIsTagged is the M2 proof: when Node cannot be resolved
// the developer resolver tags the error node_runtime_missing (uniform with the
// kit resolver). LOOM_NODE_BIN pointing at a nonexistent binary forces the miss.
func TestDeveloperNodeMissingIsTagged(t *testing.T) {
	clearAuthoringOverrides(t)
	t.Setenv("LOOM_NODE_BIN", filepath.Join(t.TempDir(), "definitely-not-node"))
	_, err := resolveDeveloperToolchain()
	if err == nil {
		t.Skip("Node unexpectedly resolved; cannot exercise the missing-node path")
	}
	if !strings.Contains(err.Error(), "node_runtime_missing") {
		t.Fatalf("developer node error not tagged node_runtime_missing: %v", err)
	}
}

// TestAuthoringReadinessReportsSandboxMode is the M1 proof: the readiness object
// reports the real sandbox mode instead of a hardcoded "none".
func TestAuthoringReadinessReportsSandboxMode(t *testing.T) {
	clearAuthoringOverrides(t)
	t.Setenv("LOOM_AUTHORING_KIT_DIR", filepath.Join(t.TempDir(), "no-such-kit"))
	authoringkit.ResetForTest()
	t.Cleanup(authoringkit.ResetForTest)

	r := AuthoringReadiness()
	sandbox, ok := r["sandbox"].(string)
	if !ok {
		t.Fatalf("readiness missing string sandbox field: %#v", r)
	}
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err == nil {
		if sandbox != "seatbelt" {
			t.Fatalf("with sandbox-exec present, sandbox mode should be seatbelt, got %q", sandbox)
		}
	} else if sandbox == "" {
		t.Fatalf("sandbox mode must be reported, got empty")
	}
}

// TestStageFlueBuildDependenciesKitStagesDaytonaFromKit guards the kit-mode
// daytona wiring: a kit build must resolve @daytona/sdk (and its nested transitive
// closure) from the kit's own tree, not from DAYTONA_SDK_ROOT/FLUE_REPO which a
// real desktop install never has. It also guards the Bug #4 fix: kit deps are
// symlinked (like the developer/override path) so rolldown bundles @flue/runtime
// into server.mjs rather than externalizing a real copy the dist wouldn't ship.
func TestStageFlueBuildDependenciesKitStagesDaytonaFromKit(t *testing.T) {
	// Point the env-based resolvers at nothing so a regression (falling back to
	// daytonaSDKRoot) would fail to find daytona rather than silently succeed.
	clearAuthoringOverrides(t)

	kit := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(kit, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// flue-runtime with the hono deps the linker stages from the runtime root.
	write("flue-runtime/package.json", `{"name":"@flue/runtime"}`)
	write("flue-runtime/node_modules/@hono/node-server/package.json", `{"name":"@hono/node-server"}`)
	write("flue-runtime/node_modules/hono/package.json", `{"name":"hono"}`)
	// daytona shipped self-contained: the package plus its nested closure (the
	// @daytona/api-client that was the first casualty of the bare-copy bug).
	write("daytona-sdk/package.json", `{"name":"@daytona/sdk"}`)
	write("daytona-sdk/cjs/index.js", `require("@daytona/api-client");`)
	write("daytona-sdk/node_modules/@daytona/api-client/package.json", `{"name":"@daytona/api-client"}`)

	tc := AuthoringToolchain{
		Source:      "kit",
		RuntimeRoot: filepath.Join(kit, "flue-runtime"),
		DaytonaRoot: filepath.Join(kit, "daytona-sdk"),
	}
	root := t.TempDir()
	if err := stageFlueBuildDependencies(root, tc); err != nil {
		t.Fatalf("stageFlueBuildDependencies (kit): %v", err)
	}

	daytona := filepath.Join(root, "node_modules", "@daytona", "sdk")
	// The dep is symlinked to the KIT's own daytona tree (Bug #2/#4): a link, and
	// its target is the kit root, not anything the environment could resolve.
	fi, err := os.Lstat(daytona)
	if err != nil {
		t.Fatalf("@daytona/sdk not staged from kit: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("kit @daytona/sdk should be symlinked so rolldown bundles it")
	}
	if target, err := os.Readlink(daytona); err != nil {
		t.Fatal(err)
	} else if target != tc.DaytonaRoot {
		t.Fatalf("daytona link target = %q, want the kit's daytona %q", target, tc.DaytonaRoot)
	}
	// The nested closure must be reachable through the link, or vite cannot
	// resolve @daytona/api-client while bundling (Bug #3).
	if _, err := os.Stat(filepath.Join(daytona, "node_modules", "@daytona", "api-client", "package.json")); err != nil {
		t.Fatalf("daytona transitive closure (@daytona/api-client) not reachable via kit link: %v", err)
	}
	// @flue/runtime is staged (symlinked) from the kit too.
	flueRuntime := filepath.Join(root, "node_modules", "@flue", "runtime")
	if fi, err := os.Lstat(flueRuntime); err != nil {
		t.Fatalf("@flue/runtime not staged from kit: %v", err)
	} else if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("kit @flue/runtime should be symlinked so rolldown bundles it")
	}
	if _, err := os.Stat(filepath.Join(flueRuntime, "package.json")); err != nil {
		t.Fatalf("@flue/runtime not reachable via kit link: %v", err)
	}
}

// TestStageLoomSDKRuntimeStagesExternal is the H4 proof: the @loom/sdk runtime
// (which server.mjs imports as an external) is staged into the dist tree so it
// travels with the artifact RegisterFlueDriver copies.
func TestStageLoomSDKRuntimeStagesExternal(t *testing.T) {
	sdkRoot := t.TempDir()
	for _, rel := range packaged.LoomSDKRuntimeFiles {
		if err := os.WriteFile(filepath.Join(sdkRoot, rel), []byte("// "+rel+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dist := t.TempDir()
	if err := stageLoomSDKRuntime(sdkRoot, dist); err != nil {
		t.Fatalf("stageLoomSDKRuntime: %v", err)
	}
	for _, rel := range packaged.LoomSDKRuntimeFiles {
		staged := filepath.Join(dist, "node_modules", "@loom", "sdk", rel)
		got, err := os.ReadFile(staged)
		if err != nil {
			t.Fatalf("expected staged %s: %v", rel, err)
		}
		if string(got) != "// "+rel+"\n" {
			t.Fatalf("staged %s content mismatch: %q", rel, got)
		}
	}
	if err := stageLoomSDKRuntime("", dist); err == nil {
		t.Fatal("empty sdk root should error")
	}
	// A minimal SDK missing the runtime files does not fail the build — staging
	// copies what the SDK provides and skips the rest (completeness is the SDK's
	// own guarantee, not the copy's).
	emptyDist := t.TempDir()
	if err := stageLoomSDKRuntime(t.TempDir(), emptyDist); err != nil {
		t.Fatalf("stub sdk root should not error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(emptyDist, "node_modules", "@loom", "sdk", "index.js")); !os.IsNotExist(err) {
		t.Fatalf("absent runtime file should not have been staged, stat err=%v", err)
	}
}
