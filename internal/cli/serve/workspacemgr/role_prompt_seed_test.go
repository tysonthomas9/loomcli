package workspacemgr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/roleprompts"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestSeedBuiltInRolesWritesPromptFiles proves seeding materializes the
// TS-contract prompt body for the task-running roles (plan/task) under
// <wsDir>/.loom/prompts and records the path on the role, while lead (no
// default body) is seeded with no PromptFile.
func TestSeedBuiltInRolesWritesPromptFiles(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	wsDir := t.TempDir()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := seedBuiltInRoles(ctx, managedAgentsForTest(st), "WS", wsDir); err != nil {
		t.Fatalf("seedBuiltInRoles: %v", err)
	}

	promptsDir := filepath.Join(wsDir, ".loom", "prompts")
	for _, name := range []string{"plan", "task"} {
		role, err := st.Roles().Get(ctx, "WS", name)
		if err != nil {
			t.Fatalf("get role %q: %v", name, err)
		}
		if strings.TrimSpace(role.PromptFile) == "" {
			t.Fatalf("role %q PromptFile empty; want a seeded prompt path", name)
		}
		if !strings.HasPrefix(role.PromptFile, promptsDir) {
			t.Fatalf("role %q PromptFile = %q, want under %q", name, role.PromptFile, promptsDir)
		}
		data, err := os.ReadFile(role.PromptFile)
		if err != nil {
			t.Fatalf("read seeded prompt %q: %v", role.PromptFile, err)
		}
		want, _ := roleprompts.DefaultPromptBody(name)
		if string(data) != want {
			t.Fatalf("role %q prompt body mismatch with embedded default", name)
		}
	}

	lead, err := st.Roles().Get(ctx, "WS", "lead")
	if err != nil {
		t.Fatalf("get lead role: %v", err)
	}
	if lead.PromptFile != "" {
		t.Fatalf("lead PromptFile = %q, want empty (no default body)", lead.PromptFile)
	}
}

// TestEnsureBuiltinRolePromptIdempotency covers the backfill core: an empty
// PromptFile is materialized with the default body; a non-empty (operator or
// already-seeded) PromptFile is NEVER clobbered.
func TestEnsureBuiltinRolePromptIdempotency(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	wsDir := t.TempDir()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	// Simulate a pre-existing workspace whose builtin roles carry no prompt file.
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS", Name: "plan", TaskFilter: "needs_plan"}); err != nil {
		t.Fatalf("create plan role: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS", Name: "task", TaskFilter: "has_design"}); err != nil {
		t.Fatalf("create task role: %v", err)
	}

	// Empty PromptFile → materialized.
	ensureBuiltinRolePrompt(ctx, st, managedAgentsForTest(st), "WS", wsDir, "plan")
	planRole, err := st.Roles().Get(ctx, "WS", "plan")
	if err != nil {
		t.Fatalf("get plan role: %v", err)
	}
	if strings.TrimSpace(planRole.PromptFile) == "" {
		t.Fatal("plan PromptFile still empty; backfill did not materialize the body")
	}
	data, err := os.ReadFile(planRole.PromptFile)
	if err != nil {
		t.Fatalf("read backfilled prompt: %v", err)
	}
	if want, _ := roleprompts.DefaultPromptBody("plan"); string(data) != want {
		t.Fatal("backfilled prompt body mismatch with embedded default")
	}

	// Non-empty PromptFile → untouched (never clobber an operator customization).
	customPath := filepath.Join(wsDir, "custom-task.md")
	if err := os.WriteFile(customPath, []byte("CUSTOM OPERATOR PROMPT"), 0o644); err != nil {
		t.Fatalf("write custom prompt: %v", err)
	}
	if _, err := st.Roles().Update(ctx, "WS", "task", store.RoleUpdate{PromptFile: &customPath}); err != nil {
		t.Fatalf("set custom PromptFile: %v", err)
	}
	ensureBuiltinRolePrompt(ctx, st, managedAgentsForTest(st), "WS", wsDir, "task")
	taskRole, err := st.Roles().Get(ctx, "WS", "task")
	if err != nil {
		t.Fatalf("get task role: %v", err)
	}
	if taskRole.PromptFile != customPath {
		t.Fatalf("task PromptFile = %q, want unchanged %q (operator prompt clobbered)", taskRole.PromptFile, customPath)
	}
	got, err := os.ReadFile(customPath)
	if err != nil {
		t.Fatalf("read custom prompt: %v", err)
	}
	if string(got) != "CUSTOM OPERATOR PROMPT" {
		t.Fatalf("custom prompt content changed to %q", string(got))
	}
}

// TestEnsureBuiltinRolePromptsBackfillsExistingWorkspace exercises the exported
// serve-start sweep end-to-end through the local state cache: a workspace whose
// plan role has an empty PromptFile is materialized, and a re-run is a no-op.
func TestEnsureBuiltinRolePromptsBackfillsExistingWorkspace(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	st := memstore.New()
	wsDir := t.TempDir()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "WS", Name: "plan", TaskFilter: "needs_plan"}); err != nil {
		t.Fatalf("create plan role: %v", err)
	}
	// Register the workspace's local path so localWorkspacePath can resolve it.
	if err := saveLocalWorkspaceState("WS", wsDir, nil, false); err != nil {
		t.Fatalf("save local workspace state: %v", err)
	}

	if err := EnsureBuiltinRolePrompts(ctx, st, managedAgentsForTest(st)); err != nil {
		t.Fatalf("EnsureBuiltinRolePrompts: %v", err)
	}
	planRole, err := st.Roles().Get(ctx, "WS", "plan")
	if err != nil {
		t.Fatalf("get plan role: %v", err)
	}
	if strings.TrimSpace(planRole.PromptFile) == "" {
		t.Fatal("plan PromptFile still empty after backfill sweep")
	}

	// Second sweep must not change the already-materialized PromptFile.
	firstPath := planRole.PromptFile
	if err := EnsureBuiltinRolePrompts(ctx, st, managedAgentsForTest(st)); err != nil {
		t.Fatalf("second EnsureBuiltinRolePrompts: %v", err)
	}
	planRole2, _ := st.Roles().Get(ctx, "WS", "plan")
	if planRole2.PromptFile != firstPath {
		t.Fatalf("PromptFile changed on idempotent re-run: %q -> %q", firstPath, planRole2.PromptFile)
	}
}
