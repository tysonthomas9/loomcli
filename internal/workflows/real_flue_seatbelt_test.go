//go:build darwin

package workflows

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestRealFlueBuildUnderSeatbelt is the DoD-2 integration proof: a REAL Flue
// build of the epic-runner builtin runs through runFlueBuild under the live
// Seatbelt sandbox, produces dist/server.mjs, stages the @loom/sdk runtime into
// the dist, and the built server then loads under Node — resolving that staged
// external — via Flue's ready handshake.
//
// It is gated on LOOM_REAL_FLUE_TEST=1 with the authoring toolchain pointed at a
// Flue checkout built at the pin, e.g.:
//
//	LOOM_REAL_FLUE_TEST=1 \
//	LOOM_REAL_FLUE_CMD_JSON='["/path/to/node","/path/to/flue/packages/cli/bin/flue.mjs"]' \
//	LOOM_SDK_ROOT=/path/to/loomcli/sdk \
//	FLUE_RUNTIME_ROOT=/path/to/flue/packages/runtime \
//	DAYTONA_SDK_ROOT=/path/to/flue/node_modules/.pnpm/node_modules/@daytona/sdk \
//	go test ./internal/workflows/ -run TestRealFlueBuildUnderSeatbelt -v
func TestRealFlueBuildUnderSeatbelt(t *testing.T) {
	if os.Getenv("LOOM_REAL_FLUE_TEST") != "1" {
		t.Skip("set LOOM_REAL_FLUE_TEST=1 with a built Flue toolchain to run the seatbelt integration proof")
	}
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		t.Skip("sandbox-exec unavailable")
	}
	dest := filepath.Join(t.TempDir(), "dist")
	serverPath, output, err := BuildBuiltinBundle(context.Background(), BuiltinEpicRunnerWorkflowName, dest)
	if err != nil {
		t.Fatalf("real Flue build under seatbelt failed: %v\n%s", err, output)
	}
	if info, err := os.Stat(serverPath); err != nil || info.IsDir() || info.Size() == 0 {
		t.Fatalf("server.mjs not produced as a non-empty file: path=%s err=%v", serverPath, err)
	}
	for _, rel := range []string{"package.json", "index.js"} {
		if _, err := os.Stat(filepath.Join(dest, "node_modules", "@loom", "sdk", rel)); err != nil {
			t.Fatalf("staged @loom/sdk missing %s in dist: %v", rel, err)
		}
	}
	t.Logf("real Flue build succeeded under seatbelt; server=%s", serverPath)

	// Runtime load-smoke: fork server.mjs and wait for Flue's ready handshake.
	// This proves the whole module graph loads under Node, including the staged
	// dist/node_modules/@loom/sdk external — i.e. H4 works end to end.
	node := realFlueNode(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	smoke := filepath.Join(cwd, "..", "..", "desktop", "scripts", "smoke-load-server.mjs")
	if _, err := os.Stat(smoke); err != nil {
		t.Skipf("load-smoke script not found (%v); build proof already passed", err)
	}
	out, err := exec.CommandContext(context.Background(), node, smoke, serverPath, BuiltinEpicRunnerWorkflowName, "30000").CombinedOutput()
	if err != nil {
		t.Fatalf("server.mjs load-smoke failed — H4 runtime resolution of @loom/sdk: %v\n%s", err, out)
	}
	t.Logf("server.mjs load-smoke passed: %s", strings.TrimSpace(string(out)))
}

// TestKitModeCustomBuildRegistersUntrusted is the DoD-2 acceptance in TRUE kit
// mode: with ONLY the embedded kit (LOOM_AUTHORING_KIT_DIR, no developer
// overrides, no PATH flue/node escape hatches), a custom workflow source builds
// under the seatbelt and registers as exactly one INACTIVE, UNTRUSTED
// DriverVersion — approval, not the build, is the trust boundary.
//
// Gated like TestRealFlueBuildUnderSeatbelt: LOOM_REAL_FLUE_TEST=1 plus a staged
// kit at LOOM_AUTHORING_KIT_DIR, and every developer override UNSET so the
// resolver is forced down the kit path.
func TestKitModeCustomBuildRegistersUntrusted(t *testing.T) {
	if os.Getenv("LOOM_REAL_FLUE_TEST") != "1" {
		t.Skip("set LOOM_REAL_FLUE_TEST=1 with a staged kit to run the kit-mode custom-build acceptance")
	}
	if strings.TrimSpace(os.Getenv("LOOM_AUTHORING_KIT_DIR")) == "" {
		t.Skip("set LOOM_AUTHORING_KIT_DIR to a staged kit for kit-mode acceptance")
	}
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		t.Skip("sandbox-exec unavailable")
	}
	// An override would flip the resolver off the kit path and invalidate the
	// proof, so fail loudly rather than silently testing the wrong mode.
	for _, k := range []string{"LOOM_REAL_FLUE_CMD_JSON", "LOOM_REAL_FLUE_CMD", "LOOM_SDK_ROOT", "LOOM_FLUE_RUNTIME_ROOT", "FLUE_RUNTIME_ROOT", "FLUE_REPO", "DAYTONA_SDK_ROOT"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			t.Fatalf("override %s=%q is set; kit-mode acceptance must run with no overrides", k, v)
		}
	}

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "CUSTOM", Name: "custom"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	files := map[string]string{
		"workflows/custom-smoke.ts": "export async function run(ctx) { return { marker: ctx.payload?.marker || 'kit-custom' }; }\n",
	}
	result, diagnostics, err := BuildAndRegister(ctx, st, BuildAndRegisterOptions{
		WorkspaceKey: "CUSTOM",
		Name:         "custom-smoke",
		Entrypoint:   "workflows/custom-smoke.ts",
		Files:        files,
		Activate:     false,
		SourceRef:    "test://kit-custom-smoke",
		CreatedBy:    "test",
		WorkDir:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("kit-mode custom BuildAndRegister failed: %v\n%s", err, diagnostics)
	}
	if result.Driver.ActiveVersionID != "" {
		t.Fatalf("custom build activated a version (%q); a custom build must be inactive", result.Driver.ActiveVersionID)
	}
	if got := result.Version.Manifest[driverpkg.ManifestTrustLevelKey]; got != string(domain.DriverTrustUntrusted) {
		t.Fatalf("custom version trust = %q, want untrusted", got)
	}
	t.Logf("kit-mode custom build registered inactive+untrusted version %s", result.Version.VersionID)
}

// realFlueNode returns the Node the build toolchain uses: the first element of
// LOOM_REAL_FLUE_CMD_JSON, else `node` on PATH.
func realFlueNode(t *testing.T) string {
	t.Helper()
	if raw := strings.TrimSpace(os.Getenv("LOOM_REAL_FLUE_CMD_JSON")); raw != "" {
		var parts []string
		if err := json.Unmarshal([]byte(raw), &parts); err == nil && len(parts) > 0 && parts[0] != "" {
			return parts[0]
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable for load-smoke")
	}
	return node
}
