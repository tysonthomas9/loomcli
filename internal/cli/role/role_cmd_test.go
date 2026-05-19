package role

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestRoleCommandsAgainstLocalStore(t *testing.T) {
	handle := setupRoleFleetWorkspace(t)
	defer handle.Close()

	resetRoleFlagGlobals(t)
	if out := captureRoleStdout(t, func() {
		if err := runRoleList(nil, nil); err != nil {
			t.Fatalf("runRoleList empty: %v", err)
		}
	}); !strings.Contains(out, "No roles in workspace WS") {
		t.Fatalf("empty list output = %q", out)
	}

	roleAddDescription = strings.Repeat("d", 70)
	roleAddPromptFile = "prompts/task.md"
	roleAddModel = "gpt-5"
	roleAddBackend = "codex"
	roleAddSkills = []string{"go", "review"}
	roleAddMaxConc = 2
	roleAddReadOnly = true
	if out := captureRoleStdout(t, func() {
		if err := runRoleAdd(nil, []string{"task"}); err != nil {
			t.Fatalf("runRoleAdd: %v", err)
		}
	}); !strings.Contains(out, "Created role WS/task") {
		t.Fatalf("add output = %q", out)
	}

	roleListJSON = false
	if out := captureRoleStdout(t, func() {
		if err := runRoleList(nil, nil); err != nil {
			t.Fatalf("runRoleList: %v", err)
		}
	}); !strings.Contains(out, "task") || !strings.Contains(out, "...") {
		t.Fatalf("list output = %q", out)
	}

	roleShowJSON = false
	if out := captureRoleStdout(t, func() {
		if err := runRoleShow(nil, []string{"task"}); err != nil {
			t.Fatalf("runRoleShow: %v", err)
		}
	}); !strings.Contains(out, "Model:        gpt-5") ||
		!strings.Contains(out, "Prompt file:  prompts/task.md") ||
		!strings.Contains(out, "Max concurrency: 2") ||
		!strings.Contains(out, "Read-only:    true") {
		t.Fatalf("show output = %q", out)
	}

	roleShowJSON = true
	if out := captureRoleStdout(t, func() {
		if err := runRoleShow(nil, []string{"task"}); err != nil {
			t.Fatalf("runRoleShow json: %v", err)
		}
	}); !strings.Contains(out, `"name": "task"`) {
		t.Fatalf("show json output = %q", out)
	}

	if out := captureRoleStdout(t, func() {
		if err := runRoleSet(nil, []string{"task", "model", "gpt-5.1"}); err != nil {
			t.Fatalf("runRoleSet: %v", err)
		}
	}); !strings.Contains(out, "Set WS/task.model = gpt-5.1") {
		t.Fatalf("set output = %q", out)
	}

	if out := captureRoleStdout(t, func() {
		if err := runRoleUnset(nil, []string{"task", "max_concurrency"}); err != nil {
			t.Fatalf("runRoleUnset: %v", err)
		}
	}); !strings.Contains(out, "Cleared WS/task.max_concurrency") {
		t.Fatalf("unset output = %q", out)
	}

	if out := captureRoleStdout(t, func() {
		if err := runRoleRemove(nil, []string{"task"}); err != nil {
			t.Fatalf("runRoleRemove: %v", err)
		}
	}); !strings.Contains(out, "Removed role WS/task") {
		t.Fatalf("remove output = %q", out)
	}
}

func TestBuildRolePatchSetAndUnset(t *testing.T) {
	tests := []struct {
		key   string
		value string
		check func(t *testing.T)
	}{
		{"description", "desc", func(t *testing.T) {
			p, err := buildRolePatch("description", "desc", false)
			if err != nil || p.Description == nil || *p.Description != "desc" {
				t.Fatalf("description patch = %+v err=%v", p, err)
			}
		}},
		{"prompt_file", "prompt.md", func(t *testing.T) {
			p, err := buildRolePatch("prompt_file", "prompt.md", false)
			if err != nil || p.PromptFile == nil || *p.PromptFile != "prompt.md" {
				t.Fatalf("prompt patch = %+v err=%v", p, err)
			}
		}},
		{"model", "gpt", func(t *testing.T) {
			p, err := buildRolePatch("model", "gpt", false)
			if err != nil || p.Model == nil || *p.Model != "gpt" {
				t.Fatalf("model patch = %+v err=%v", p, err)
			}
		}},
		{"task_filter", "kind:task", func(t *testing.T) {
			p, err := buildRolePatch("task_filter", "kind:task", false)
			if err != nil || p.TaskFilter == nil || *p.TaskFilter != "kind:task" {
				t.Fatalf("task filter patch = %+v err=%v", p, err)
			}
		}},
		{"backend", "codex", func(t *testing.T) {
			p, err := buildRolePatch("backend", "codex", false)
			if err != nil || p.Backend == nil || *p.Backend != "codex" {
				t.Fatalf("backend patch = %+v err=%v", p, err)
			}
		}},
		{"read_only", "true", func(t *testing.T) {
			p, err := buildRolePatch("read_only", "true", false)
			if err != nil || p.ReadOnly == nil || !*p.ReadOnly {
				t.Fatalf("read_only patch = %+v err=%v", p, err)
			}
			p, err = buildRolePatch("read_only", "", true)
			if err != nil || p.ReadOnly == nil || *p.ReadOnly {
				t.Fatalf("read_only unset patch = %+v err=%v", p, err)
			}
		}},
		{"max_priority", "7", func(t *testing.T) {
			p, err := buildRolePatch("max_priority", "7", false)
			if err != nil || p.MaxPriority == nil || *p.MaxPriority == nil || **p.MaxPriority != 7 {
				t.Fatalf("priority patch = %+v err=%v", p, err)
			}
			p, err = buildRolePatch("max_priority", "", true)
			if err != nil || p.MaxPriority == nil || *p.MaxPriority != nil {
				t.Fatalf("priority unset patch = %+v err=%v", p, err)
			}
		}},
		{"max_concurrency", "3", func(t *testing.T) {
			p, err := buildRolePatch("max_concurrency", "3", false)
			if err != nil || p.MaxConcurrency == nil || *p.MaxConcurrency == nil || **p.MaxConcurrency != 3 {
				t.Fatalf("concurrency patch = %+v err=%v", p, err)
			}
		}},
		{"max_budget_usd", "1.5", func(t *testing.T) {
			p, err := buildRolePatch("max_budget_usd", "1.5", false)
			if err != nil || p.MaxBudgetUSD == nil || *p.MaxBudgetUSD == nil || **p.MaxBudgetUSD != 1.5 {
				t.Fatalf("budget patch = %+v err=%v", p, err)
			}
			p, err = buildRolePatch("max_budget_usd", "", true)
			if err != nil || p.MaxBudgetUSD == nil || *p.MaxBudgetUSD != nil {
				t.Fatalf("budget unset patch = %+v err=%v", p, err)
			}
		}},
		{"skills", "go, test,,review", func(t *testing.T) {
			p, err := buildRolePatch("skills", "go, test,,review", false)
			if err != nil || p.Skills == nil || strings.Join(*p.Skills, "|") != "go|test|review" {
				t.Fatalf("skills patch = %+v err=%v", p, err)
			}
		}},
		{"path_patterns", "*.go,internal/**", func(t *testing.T) {
			p, err := buildRolePatch("path_patterns", "*.go,internal/**", false)
			if err != nil || p.PathPatterns == nil || len(*p.PathPatterns) != 2 {
				t.Fatalf("patterns patch = %+v err=%v", p, err)
			}
		}},
		{"allowed_tools", "git", func(t *testing.T) {
			p, err := buildRolePatch("allowed_tools", "git", false)
			if err != nil || p.AllowedTools == nil || len(*p.AllowedTools) != 1 {
				t.Fatalf("allowed patch = %+v err=%v", p, err)
			}
		}},
		{"denied_tools", "rm", func(t *testing.T) {
			p, err := buildRolePatch("denied_tools", "rm", false)
			if err != nil || p.DeniedTools == nil || len(*p.DeniedTools) != 1 {
				t.Fatalf("denied patch = %+v err=%v", p, err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.key, tt.check)
	}
}

func TestBuildRolePatchValidationErrors(t *testing.T) {
	for _, tt := range []struct {
		key   string
		value string
		want  string
	}{
		{"read_only", "maybe", "true/false"},
		{"max_priority", "high", "integer"},
		{"max_concurrency", "many", "integer"},
		{"max_budget_usd", "free", "number"},
		{"unknown", "x", "unknown key"},
	} {
		t.Run(tt.key, func(t *testing.T) {
			_, err := buildRolePatch(tt.key, tt.value, false)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestSliceCSVPtrEmptyIsPresentEmptySlice(t *testing.T) {
	got := sliceCSVPtr("")
	if got == nil || len(*got) != 0 {
		t.Fatalf("sliceCSVPtr empty = %#v", got)
	}
}

func setupRoleFleetWorkspace(t *testing.T) *bootstrap.StoreHandle {
	t.Helper()
	requireRoleFleetDB(t)
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv(bootstrap.EnvWorkspace, "WS")
	t.Setenv(bootstrap.EnvFleetDBActor, "role-test")
	t.Setenv(bootstrap.EnvFleetDBURL, "")

	ctx := context.Background()
	handle, err := bootstrap.OpenStore(ctx, configDir, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := handle.Store.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		_ = handle.Close()
		t.Fatalf("create workspace: %v", err)
	}
	return handle
}

func requireRoleFleetDB(t *testing.T) {
	t.Helper()
	if os.Getenv("FLEET_DB_BIN") != "" {
		return
	}
	if _, err := exec.LookPath("fleet-db"); err != nil {
		t.Skip("fleet-db binary not available")
	}
}

func resetRoleFlagGlobals(t *testing.T) {
	t.Helper()
	origDesc, origPrompt, origModel, origBackend := roleAddDescription, roleAddPromptFile, roleAddModel, roleAddBackend
	origSkills := roleAddSkills
	origMaxConc := roleAddMaxConc
	origReadOnly := roleAddReadOnly
	origListJSON, origShowJSON := roleListJSON, roleShowJSON
	t.Cleanup(func() {
		roleAddDescription, roleAddPromptFile, roleAddModel, roleAddBackend = origDesc, origPrompt, origModel, origBackend
		roleAddSkills = origSkills
		roleAddMaxConc = origMaxConc
		roleAddReadOnly = origReadOnly
		roleListJSON, roleShowJSON = origListJSON, origShowJSON
	})
}

func captureRoleStdout(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	var b bytes.Buffer
	if _, err := b.ReadFrom(r); err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	return b.String()
}
