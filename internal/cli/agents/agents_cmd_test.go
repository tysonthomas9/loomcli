package agents

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestAgentsAddCreatesScriptedInstance(t *testing.T) {
	st := memstore.New()
	installAgentsFakeFlueBuild(t)
	workspaceDir := t.TempDir()
	t.Chdir(workspaceDir)
	withAgentsStore(t, st, workspaceDir)
	agentsAddRole, agentsAddSchedule = "scout", "0 9 * * 1-5"
	agentsAddName, agentsAddTimezone = "Scout West", "America/Los_Angeles"
	if err := runAgentsAdd(nil, []string{"scout-west"}); err != nil {
		t.Fatalf("runAgentsAdd: %v", err)
	}
	svc, err := st.AgentServices().Get(t.Context(), "WS", "scout-west")
	if err != nil || svc.RoleName != "scout" || svc.TriggerKind != domain.AgentServiceTriggerKindCron {
		t.Fatalf("service = %#v, err %v", svc, err)
	}
	binding, err := st.TriggerBindings().Get(t.Context(), "WS", "binding-cron-scout-west")
	if err != nil || binding.RouteKey != "cron.scout-west" || binding.Schedule != "0 9 * * 1-5" || binding.ScheduleTimezone != "America/Los_Angeles" {
		t.Fatalf("binding = %#v, err %v", binding, err)
	}
}

func TestAgentsAddSurfacesGrammarAndRoleValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   string
		role string
		want string
	}{
		{name: "grammar", id: "Scout_West", role: "scout", want: "must match [a-z0-9]"},
		{name: "plain role", id: "reviewer-one", role: "reviewer", want: "not a scripted role"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := memstore.New()
			withAgentsStore(t, st, t.TempDir())
			agentsAddRole, agentsAddSchedule = tc.role, "@daily"
			err := runAgentsAdd(nil, []string{tc.id})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestAgentsListPrintsRequiredColumns(t *testing.T) {
	st := memstore.New()
	seedAgentsCLIFixture(t, st)
	out := withAgentsStore(t, st, t.TempDir())
	if err := runAgentsList(nil, nil); err != nil {
		t.Fatalf("runAgentsList: %v", err)
	}
	for _, want := range []string{"ID", "ROLE", "TRIGGER KIND", "SCHEDULE", "STATE", "scout-west", "scout", "cron", "@daily", "running"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output = %q, missing %q", out.String(), want)
		}
	}
}

func TestAgentsEnableAndDisable(t *testing.T) {
	st := memstore.New()
	seedAgentsCLIFixture(t, st)
	withAgentsStore(t, st, t.TempDir())
	if err := runAgentsDisable(nil, []string{"scout-west"}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	svc, _ := st.AgentServices().Get(t.Context(), "WS", "scout-west")
	if svc.DesiredState != domain.AgentServiceDesiredStopped {
		t.Fatalf("state after disable = %q", svc.DesiredState)
	}
	if err := runAgentsEnable(nil, []string{"scout-west"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	svc, _ = st.AgentServices().Get(t.Context(), "WS", "scout-west")
	if svc.DesiredState != domain.AgentServiceDesiredRunning {
		t.Fatalf("state after enable = %q", svc.DesiredState)
	}
}

func TestAgentsRemoveUsesOrderedDelete(t *testing.T) {
	st := memstore.New()
	seedAgentsCLIFixture(t, st)
	withAgentsStore(t, st, t.TempDir())
	if err := runAgentsRemove(nil, []string{"scout-west"}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	svc, err := st.AgentServices().Get(t.Context(), "WS", "scout-west")
	if err != nil || svc.DeletedAt == nil || svc.DesiredState != domain.AgentServiceDesiredStopped {
		t.Fatalf("removed service = %#v, err %v", svc, err)
	}
	if _, err := st.TriggerBindings().Get(t.Context(), "WS", "binding-cron-scout-west"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("binding err = %v, want ErrNotFound", err)
	}
	if err := runAgentsRemove(nil, []string{"scout-west"}); err != nil {
		t.Fatalf("idempotent remove: %v", err)
	}
}

func withAgentsStore(t *testing.T, st store.Store, workspaceDir string) *bytes.Buffer {
	t.Helper()
	oldWith, oldDir, oldOut := agentsWithActiveWorkspace, agentsWorkspaceDir, agentsOutput
	out := &bytes.Buffer{}
	agentsWithActiveWorkspace = func(fn func(context.Context, *bootstrap.StoreHandle, string) error) error {
		return fn(t.Context(), &bootstrap.StoreHandle{Store: st}, "WS")
	}
	agentsWorkspaceDir = func(string) string { return workspaceDir }
	agentsOutput = out
	resetAgentsFlags()
	t.Cleanup(func() {
		agentsWithActiveWorkspace, agentsWorkspaceDir, agentsOutput = oldWith, oldDir, oldOut
		resetAgentsFlags()
	})
	return out
}

func resetAgentsFlags() {
	agentsAddRole, agentsAddSchedule, agentsAddName, agentsAddTimezone = "", "", "", ""
}

func seedAgentsCLIFixture(t *testing.T, st store.Store) {
	t.Helper()
	ctx := t.Context()
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS", Name: "scout", Kind: string(domain.RoleKindWorker)}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{WorkspaceKey: "WS", DriverID: "scout", Name: "scout", Status: domain.DriverStatusActive}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "scout-v1", DriverID: "scout", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle", ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("create version: %v", err)
	}
	if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: "WS", ServiceID: "scout-west", Name: "Scout West",
		TriggerKind: domain.AgentServiceTriggerKindCron, DesiredState: domain.AgentServiceDesiredRunning, RoleName: "scout",
	}); err != nil {
		t.Fatalf("create service: %v", err)
	}
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "binding-cron-scout-west", Name: "Scout West cron", SourceKind: "cron",
		RouteKey: "cron.scout-west", DriverID: "scout", DriverVersionID: "scout-v1",
		TargetAgentServiceID: "scout-west", TargetEntrypoint: "run", Schedule: "@daily", Enabled: true,
	}); err != nil {
		t.Fatalf("create binding: %v", err)
	}
}

func installAgentsFakeFlueBuild(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-flue")
	body := `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output" ]; then shift; out="$1"; fi
  shift
done
mkdir -p "$out"
cat > "$out/server.mjs" <<'EOF'
export async function run() { return {}; }
EOF
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake flue: %v", err)
	}
	sdkRoot := filepath.Join(dir, "sdk")
	runtimeRoot := filepath.Join(dir, "runtime")
	for _, dep := range []string{sdkRoot, runtimeRoot, filepath.Join(runtimeRoot, "node_modules", "@hono", "node-server"), filepath.Join(runtimeRoot, "node_modules", "hono")} {
		if err := os.MkdirAll(dep, 0o755); err != nil {
			t.Fatalf("create fake dependency: %v", err)
		}
	}
	_ = os.WriteFile(filepath.Join(sdkRoot, "package.json"), []byte(`{"name":"@loom/sdk"}`), 0o644)
	_ = os.WriteFile(filepath.Join(runtimeRoot, "package.json"), []byte(`{"name":"@flue/runtime"}`), 0o644)
	t.Setenv("LOOM_REAL_FLUE_CMD", script)
	t.Setenv("LOOM_SDK_ROOT", sdkRoot)
	t.Setenv("LOOM_FLUE_RUNTIME_ROOT", runtimeRoot)
	t.Setenv("FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_REPO", "")
}
