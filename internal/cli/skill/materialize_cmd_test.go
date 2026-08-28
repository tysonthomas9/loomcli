package skill

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestSkillMaterializeMaterializesCurrentWorkdir(t *testing.T) {
	st := materializeTestStore(t)
	createMaterializeTestSkill(t, st, domain.WorkspaceSkillRef("review-guide"), "Review guidance", "# Review guide\n")
	withMaterializeStore(t, st)
	t.Setenv(bootstrap.EnvWorkspace, testWorkspace)
	t.Setenv("LOOM_AGENT_ROLE", "")
	t.Setenv(bootstrap.EnvAgentName, "")
	target := t.TempDir()
	t.Chdir(target)

	output, err := executeSkillCommand(t, "", "materialize")
	if err != nil {
		t.Fatalf("materialize error = %v; output = %s", err, output)
	}
	content, err := os.ReadFile(filepath.Join(target, ".agents", "skills", "review-guide", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "# Review guide") {
		t.Fatalf("materialized content = %q", content)
	}
}

func TestSkillMaterializeResolvesRoleFromAgentName(t *testing.T) {
	st := materializeTestStore(t)
	ctx := context.Background()
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: testWorkspace, Name: "reviewer",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: testWorkspace, Name: "nova", RoleName: "reviewer",
	}); err != nil {
		t.Fatal(err)
	}
	createMaterializeTestSkill(t, st, domain.RoleSkillRef("reviewer", "review-checklist"), "Review checklist", "# Checklist\n")
	withMaterializeStore(t, st)
	t.Setenv(bootstrap.EnvWorkspace, testWorkspace)
	t.Setenv("LOOM_AGENT_ROLE", "")
	t.Setenv(bootstrap.EnvAgentName, "nova")
	target := t.TempDir()
	t.Chdir(target)

	if output, err := executeSkillCommand(t, "", "materialize"); err != nil {
		t.Fatalf("materialize error = %v; output = %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(target, ".agents", "skills", "review-checklist", "SKILL.md")); err != nil {
		t.Fatalf("agent role skill was not materialized: %v", err)
	}
}

func TestSkillMaterializeStoreUnavailableWarnsAndSucceeds(t *testing.T) {
	st := materializeTestStore(t)
	unavailable := storeWithSkills{
		Store: st,
		skills: unavailableListSkillStore{
			SkillStore: st.Skills(),
			err:        &net.DNSError{Err: "temporary failure", IsTemporary: true},
		},
	}
	withMaterializeStore(t, unavailable)
	t.Setenv(bootstrap.EnvWorkspace, testWorkspace)
	t.Setenv("LOOM_AGENT_ROLE", "")
	t.Setenv(bootstrap.EnvAgentName, "")
	t.Chdir(t.TempDir())

	output, err := executeSkillCommand(t, "", "materialize")
	if err != nil {
		t.Fatalf("materialize error = %v, want nil; output = %s", err, output)
	}
	if !strings.Contains(output, "Warning: skill store unavailable") || !strings.Contains(output, "temporary failure") {
		t.Fatalf("warning output = %q", output)
	}
}

func TestSkillMaterializeRefusalReturnsBlockingExitCode(t *testing.T) {
	st := materializeTestStore(t)
	createMaterializeTestSkill(t, st, domain.WorkspaceSkillRef("collision"), "Collision test", "# Managed\n")
	withMaterializeStore(t, st)
	t.Setenv(bootstrap.EnvWorkspace, testWorkspace)
	t.Setenv("LOOM_AGENT_ROLE", "")
	t.Setenv(bootstrap.EnvAgentName, "")
	target := t.TempDir()
	t.Chdir(target)
	collision := filepath.Join(target, ".agents", "skills", "collision", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(collision), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(collision, []byte("user owned"), 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := executeSkillCommand(t, "", "materialize")
	if err == nil {
		t.Fatalf("materialize error = nil; output = %s", output)
	}
	if got := cli.CommandExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2; error = %v", got, err)
	}
	if !strings.Contains(err.Error(), "skill materialization was refused") || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("refusal error = %v", err)
	}
}

type unavailableListSkillStore struct {
	store.SkillStore
	err error
}

func (s unavailableListSkillStore) List(context.Context, string, store.SkillFilter) ([]*domain.Skill, error) {
	return nil, s.err
}

func materializeTestStore(t *testing.T) *memstore.Store {
	t.Helper()
	st := memstore.New()
	_, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{
		Key: testWorkspace, Name: "Skills Test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func createMaterializeTestSkill(t *testing.T, st *memstore.Store, ref domain.SkillRef, description, body string) {
	t.Helper()
	snapshot, err := domain.BuildSkillFileTree(ref.Name, description, []byte(body), nil)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := publishSkillSnapshot(t.Context(), st.WorkspaceFiles(), testWorkspace, *snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: testWorkspace, Ref: ref, Description: description,
		FileTreeRevision: revision, Source: "test",
	}); err != nil {
		t.Fatal(err)
	}
}

func withMaterializeStore(t *testing.T, st store.Store) {
	t.Helper()
	original := skillMaterializeOpenStore
	skillMaterializeOpenStore = func(context.Context) (*bootstrap.StoreHandle, error) {
		return &bootstrap.StoreHandle{Store: nonClosingStore{Store: st}}, nil
	}
	t.Cleanup(func() { skillMaterializeOpenStore = original })
}

type nonClosingStore struct {
	store.Store
}

func (nonClosingStore) Close() error { return nil }

func TestSkillMaterializeOpenFailureIsUnavailable(t *testing.T) {
	original := skillMaterializeOpenStore
	skillMaterializeOpenStore = func(context.Context) (*bootstrap.StoreHandle, error) {
		return nil, errors.New("fleet-db is down")
	}
	t.Cleanup(func() { skillMaterializeOpenStore = original })
	t.Chdir(t.TempDir())

	output, err := executeSkillCommand(t, "", "materialize")
	if err != nil {
		t.Fatalf("materialize error = %v, want nil", err)
	}
	if !strings.Contains(output, "fleet-db is down") {
		t.Fatalf("warning output = %q", output)
	}
}
