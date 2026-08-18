package supervisor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func labelSliceEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// The label gate is only real if the constraint survives every hop between the
// role definition and the router that runs inside the worker:
//
//	domain.Role → store.RoleCreate → fleet-db wire → config.RoleConfig →
//	LOOM_ROLE_* env → cli.RoleConstraints → MatchTask
//
// The env hop is the one that is easy to leave out, and leaving it out is not
// visible from either side: the daemon would gate its own claim-side queue
// inspection while the worker, which rebuilds its constraints from the
// environment, happily claimed anything.
//
//nolint:funlen // One test per hop would not prove the chain, which is the point.
func TestRoleLabelGate_SurvivesDomainStoreWireConfigEnvRoundTrip(t *testing.T) {
	wantLabels := []string{"needs-design", "area:api"}
	wantExclude := []string{"wip"}

	// Hop 1-2: domain.Role → store.RoleCreate.
	role := domain.Role{
		WorkspaceKey:  "ws",
		Name:          "architect",
		Kind:          domain.RoleKindWorker,
		TaskFilter:    "any",
		PromptFile:    "prompts/architect.md", // a custom role needs one to spawn
		Labels:        wantLabels,
		ExcludeLabels: wantExclude,
	}
	create := store.RoleCreate{
		WorkspaceKey:  role.WorkspaceKey,
		Name:          role.Name,
		Kind:          string(role.Kind),
		TaskFilter:    role.TaskFilter,
		PromptFile:    role.PromptFile,
		Labels:        role.Labels,
		ExcludeLabels: role.ExcludeLabels,
	}

	// Hop 3: the fleet-db wire. The role store must send fleet-db's exact JSON
	// keys and decode them back off the response.
	var sent map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		echo := map[string]any{"workspace_key": "ws"}
		for k, v := range sent {
			echo[k] = v
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(echo)
	}))
	defer ts.Close()

	client, err := fleetdb.New(fleetdb.Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatalf("fleetdb.New: %v", err)
	}
	wireRole, err := client.Roles().Create(t.Context(), create)
	if err != nil {
		t.Fatalf("fleetdb Create: %v", err)
	}
	if got, _ := sent["labels"].([]any); len(got) != 2 || got[0] != "needs-design" || got[1] != "area:api" {
		t.Fatalf("request body labels = %v, want [needs-design area:api] (body %v)", sent["labels"], sent)
	}
	if got, _ := sent["exclude_labels"].([]any); len(got) != 1 || got[0] != "wip" {
		t.Fatalf("request body exclude_labels = %v, want [wip] (body %v)", sent["exclude_labels"], sent)
	}
	if !labelSliceEqual(wireRole.Labels, wantLabels) || !labelSliceEqual(wireRole.ExcludeLabels, wantExclude) {
		t.Fatalf("decoded role gate = (%v, %v), want (%v, %v)",
			wireRole.Labels, wireRole.ExcludeLabels, wantLabels, wantExclude)
	}

	// Hop 4-5: the store, then the daemon config projection built from it.
	st := memstore.New()
	if _, err := st.Roles().Create(t.Context(), create); err != nil {
		t.Fatalf("memstore Create: %v", err)
	}
	stored, err := st.Roles().Get(t.Context(), "ws", "architect")
	if err != nil {
		t.Fatalf("memstore Get: %v", err)
	}
	if !labelSliceEqual(stored.Labels, wantLabels) || !labelSliceEqual(stored.ExcludeLabels, wantExclude) {
		t.Fatalf("stored role gate = (%v, %v), want (%v, %v)",
			stored.Labels, stored.ExcludeLabels, wantLabels, wantExclude)
	}

	projectDir := t.TempDir()
	cfg, err := cfgpkg.TestingPrimeDaemonConfigCacheFromStore(t.Context(), st, "ws", projectDir)
	if err != nil {
		t.Fatalf("project daemon config from store: %v", err)
	}
	roleCfg, ok := cfg.Roles["architect"]
	if !ok {
		t.Fatalf("role missing from daemon config: %v", cfg.Roles)
	}
	if !labelSliceEqual(roleCfg.Labels, wantLabels) || !labelSliceEqual(roleCfg.ExcludeLabels, wantExclude) {
		t.Fatalf("config role gate = (%v, %v), want (%v, %v)",
			roleCfg.Labels, roleCfg.ExcludeLabels, wantLabels, wantExclude)
	}

	// Hop 6: the spawn environment the supervisor hands the worker.
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
		ProjectDir:     projectDir,
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		EmitEvent:      func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "architect"},
		RoleConfig:   roleCfg,
		WorktreePath: projectDir,
	}
	cmd, err := s.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	envMap := make(map[string]string, len(cmd.Env))
	for _, env := range cmd.Env {
		if idx := strings.IndexByte(env, '='); idx >= 0 {
			envMap[env[:idx]] = env[idx+1:]
		}
	}
	if envMap["LOOM_ROLE_LABELS"] != "needs-design,area:api" {
		t.Fatalf("LOOM_ROLE_LABELS = %q, want %q", envMap["LOOM_ROLE_LABELS"], "needs-design,area:api")
	}
	if envMap["LOOM_ROLE_EXCLUDE_LABELS"] != "wip" {
		t.Fatalf("LOOM_ROLE_EXCLUDE_LABELS = %q, want %q", envMap["LOOM_ROLE_EXCLUDE_LABELS"], "wip")
	}

	// Hop 7: the worker rebuilds its constraints from that environment, and the
	// gate it ends up with is the one the role was defined with.
	t.Setenv("LOOM_ROLE_LABELS", envMap["LOOM_ROLE_LABELS"])
	t.Setenv("LOOM_ROLE_EXCLUDE_LABELS", envMap["LOOM_ROLE_EXCLUDE_LABELS"])
	t.Setenv("LOOM_ROLE_TASK_FILTER", envMap["LOOM_ROLE_TASK_FILTER"])

	constraints := cli.MergeRoleConstraints(cli.RoleConfigFromEnv(), cli.AgentEntryFromEnv())
	if !labelSliceEqual(constraints.Labels, wantLabels) || !labelSliceEqual(constraints.ExcludeLabels, wantExclude) {
		t.Fatalf("constraints gate = (%v, %v), want (%v, %v)",
			constraints.Labels, constraints.ExcludeLabels, wantLabels, wantExclude)
	}

	// And it actually gates, in the worker, on the reconstructed constraints.
	open := func(labels ...string) backend.IssueData {
		return backend.IssueData{ID: "T-1", Status: "open", IssueType: "task", Labels: labels}
	}
	if m := cli.MatchTask(open("needs-design", "area:api"), constraints); m.Score == 0 {
		t.Errorf("matching issue rejected: %q", m.Reason)
	}
	if m := cli.MatchTask(open("needs-design"), constraints); m.Score != 0 {
		t.Errorf("issue missing a required label accepted with score %d", m.Score)
	}
	if m := cli.MatchTask(open("needs-design", "area:api", "wip"), constraints); m.Score != 0 {
		t.Errorf("excluded issue accepted with score %d", m.Score)
	}
}

// A role with no label gate must not gain either variable — absent stays absent,
// which is what keeps every role that exists today spawning byte-identically.
func TestBuildCommand_NoLabelGateEnvWhenUnset(t *testing.T) {
	tmpDir := t.TempDir()
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{}} },
		ProjectDir:     tmpDir,
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		EmitEvent:      func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "plan"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent"},
		WorktreePath: tmpDir,
	}

	cmd, err := s.buildCommand(ap)
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "LOOM_ROLE_LABELS=") {
			t.Error("LOOM_ROLE_LABELS set for a role with no label gate")
		}
		if strings.HasPrefix(env, "LOOM_ROLE_EXCLUDE_LABELS=") {
			t.Error("LOOM_ROLE_EXCLUDE_LABELS set for a role with no label gate")
		}
	}
}
