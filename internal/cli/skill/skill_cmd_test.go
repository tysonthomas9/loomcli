package skill

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const testWorkspace = "TEST"

func TestSkillCRUDCommandsWithMemstore(t *testing.T) {
	withoutAgentName(t)
	st := memstore.New()
	withSkillCommandStore(t, st)

	if _, err := st.Roles().Create(context.Background(), store.RoleCreate{
		WorkspaceKey: testWorkspace,
		Name:         "reviewer",
	}); err != nil {
		t.Fatalf("create reviewer role: %v", err)
	}

	tmp := t.TempDir()
	scriptPath := filepath.Join(tmp, "check.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho checked\n"), 0o755); err != nil {
		t.Fatalf("write executable source: %v", err)
	}
	referencePath := filepath.Join(tmp, "guide.txt")
	if err := os.WriteFile(referencePath, []byte("replacement guide\n"), 0o644); err != nil {
		t.Fatalf("write reference source: %v", err)
	}

	out, err := executeSkillCommand(t, "workspace body\n", "create", "review-code",
		"--description", "Review code changes",
		"--file", scriptPath+":scripts/check.sh")
	if err != nil {
		t.Fatalf("create workspace skill: %v", err)
	}
	if !strings.Contains(out, "Created skill TEST/workspace:review-code") {
		t.Fatalf("create output = %q", out)
	}

	workspaceSkill, err := st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("review-code"))
	if err != nil {
		t.Fatalf("get created workspace skill: %v", err)
	}
	if workspaceSkill.Content != "workspace body\n" || workspaceSkill.Description != "Review code changes" {
		t.Fatalf("created skill = %+v", workspaceSkill)
	}
	if len(workspaceSkill.Files) != 1 || workspaceSkill.Files[0].Path != "scripts/check.sh" || !workspaceSkill.Files[0].Executable {
		t.Fatalf("created files = %+v, want executable scripts/check.sh", workspaceSkill.Files)
	}
	if workspaceSkill.Source != manualSkillSource {
		t.Fatalf("created source = %q, want %q", workspaceSkill.Source, manualSkillSource)
	}

	if _, err := executeSkillCommand(t, "role body\n", "create", "review-code",
		"--description", "Role-specific review",
		"--scope", "role=reviewer"); err != nil {
		t.Fatalf("create role skill: %v", err)
	}

	listOut, err := executeSkillCommand(t, "", "list", "--role", "reviewer")
	if err != nil {
		t.Fatalf("list resolved role skills: %v", err)
	}
	for _, want := range []string{
		"scope=workspace",
		"scope=role=reviewer",
		"provenance=created_by=memstore source=manual",
		"status=overridden",
	} {
		if !strings.Contains(listOut, want) {
			t.Errorf("resolved list output %q does not contain %q", listOut, want)
		}
	}

	showOut, err := executeSkillCommand(t, "", "show", "review-code")
	if err != nil {
		t.Fatalf("show workspace skill: %v", err)
	}
	for _, want := range []string{
		"Scope:             workspace",
		"Content revision:",
		"workspace body",
		"Path:            scripts/check.sh",
		"Executable:      true",
		"Revision:",
		"#!/bin/sh",
	} {
		if !strings.Contains(showOut, want) {
			t.Errorf("show output does not contain %q:\n%s", want, showOut)
		}
	}

	updateOut, err := executeSkillCommand(t, "replacement body\n", "update", "review-code",
		"--description", "Updated review guidance",
		"--content", "-",
		"--file", referencePath+":references/guide.txt")
	if err != nil {
		t.Fatalf("update workspace skill: %v", err)
	}
	if !strings.Contains(updateOut, "Updated skill TEST/workspace:review-code") {
		t.Fatalf("update output = %q", updateOut)
	}
	workspaceSkill, err = st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("review-code"))
	if err != nil {
		t.Fatalf("get updated workspace skill: %v", err)
	}
	if workspaceSkill.Description != "Updated review guidance" || workspaceSkill.Content != "replacement body\n" {
		t.Fatalf("updated skill = %+v", workspaceSkill)
	}
	if len(workspaceSkill.Files) != 1 || workspaceSkill.Files[0].Path != "references/guide.txt" || workspaceSkill.Files[0].Executable {
		t.Fatalf("replacement files = %+v", workspaceSkill.Files)
	}

	deleteOut, err := executeSkillCommand(t, "", "delete", "review-code", "--scope", "role=reviewer")
	if err != nil {
		t.Fatalf("delete role skill: %v", err)
	}
	if !strings.Contains(deleteOut, "Deleted skill TEST/role:reviewer:review-code") {
		t.Fatalf("delete output = %q", deleteOut)
	}
	if _, err := st.Skills().Get(context.Background(), testWorkspace, domain.RoleSkillRef("reviewer", "review-code")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted role skill Get error = %v, want ErrNotFound", err)
	}
	if _, err := st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("review-code")); err != nil {
		t.Fatalf("role delete removed workspace skill: %v", err)
	}
}

func TestSkillWriteCommandsRefuseWhenAgentNameIsSet(t *testing.T) {
	st := memstore.New()
	storeCalls := 0
	original := skillWithActiveWorkspace
	skillWithActiveWorkspace = func(fn func(context.Context, *bootstrap.StoreHandle, string) error) error {
		storeCalls++
		return fn(context.Background(), &bootstrap.StoreHandle{Store: st}, testWorkspace)
	}
	t.Cleanup(func() { skillWithActiveWorkspace = original })
	t.Setenv("LOOM_AGENT_NAME", "task-worker-1")

	writes := []struct {
		name string
		args []string
	}{
		{name: "create", args: []string{"create", "guarded", "--description", "blocked"}},
		{name: "install", args: []string{"install"}},
		{name: "update", args: []string{"update", "guarded", "--description", "blocked"}},
		{name: "delete", args: []string{"delete", "guarded"}},
	}
	for _, tt := range writes {
		t.Run(tt.name, func(t *testing.T) {
			_, err := executeSkillCommand(t, "body", tt.args...)
			if err == nil {
				t.Fatal("write command error = nil, want A1 refusal")
			}
			for _, want := range []string{
				"LOOM_AGENT_NAME is set",
				"agent-initiated skill writes are deferred by amendment A1",
				"human operator shell",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("write refusal %q does not contain %q", err, want)
				}
			}
		})
	}
	if storeCalls != 0 {
		t.Fatalf("write commands acquired the store %d times, want 0", storeCalls)
	}

	if _, err := executeSkillCommand(t, "", "list"); err != nil {
		t.Fatalf("list should stay open to agents: %v", err)
	}
	if _, err := executeSkillCommand(t, "", "show", "missing"); err == nil || strings.Contains(err.Error(), "agent-initiated") {
		t.Fatalf("show should reach the store and return its normal error, got %v", err)
	}
	if storeCalls != 2 {
		t.Fatalf("read commands acquired the store %d times, want 2", storeCalls)
	}
}

func TestSkillProvenanceConflictAndForceDelete(t *testing.T) {
	withoutAgentName(t)
	st := memstore.New()
	withSkillCommandStore(t, st)
	st.SetSkillActor("owner-a")
	if _, err := executeSkillCommand(t, "body", "create", "owned-skill", "--description", "Owned"); err != nil {
		t.Fatalf("create owned skill: %v", err)
	}

	st.SetSkillActor("writer-b")
	_, updateErr := executeSkillCommand(t, "", "update", "owned-skill", "--description", "Taken over")
	if updateErr == nil {
		t.Fatal("update conflict error = nil")
	}
	for _, want := range []string{"provenance conflict", "owned by owner-a", "force", "does not expose --force"} {
		if !strings.Contains(updateErr.Error(), want) {
			t.Errorf("update conflict %q does not contain %q", updateErr, want)
		}
	}

	_, deleteErr := executeSkillCommand(t, "", "delete", "owned-skill")
	if deleteErr == nil {
		t.Fatal("delete conflict error = nil")
	}
	for _, want := range []string{"provenance conflict", "owned by owner-a", "re-run with --force", "skill.force_overwrite"} {
		if !strings.Contains(deleteErr.Error(), want) {
			t.Errorf("delete conflict %q does not contain %q", deleteErr, want)
		}

	}
	if _, err := executeSkillCommand(t, "", "delete", "owned-skill", "--force"); err != nil {
		t.Fatalf("force delete: %v", err)
	}
	if _, err := st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("owned-skill")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("force-deleted skill Get error = %v, want ErrNotFound", err)
	}
}

func TestSkillPreconditionErrorSuggestsReread(t *testing.T) {
	withoutAgentName(t)
	st := memstore.New()
	ctx := context.Background()
	created, err := st.Skills().Create(ctx, store.SkillCreate{
		WorkspaceKey: testWorkspace,
		Ref:          domain.WorkspaceSkillRef("stale-skill"),
		Description:  "Stale",
		Content:      "first",
	})
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}
	if _, err := st.Skills().PutFile(ctx, testWorkspace, created.Ref(), store.SkillFileWrite{
		Path:    domain.SkillFileNameSKILLMD,
		Content: "second",
		IfMatch: created.ContentRevision,
	}); err != nil {
		t.Fatalf("advance content revision: %v", err)
	}
	_, staleErr := st.Skills().PutFile(ctx, testWorkspace, created.Ref(), store.SkillFileWrite{
		Path:    domain.SkillFileNameSKILLMD,
		Content: "third",
		IfMatch: created.ContentRevision,
	})
	if !errors.Is(staleErr, domain.ErrSkillPreconditionFailed) {
		t.Fatalf("stale write error = %v, want ErrSkillPreconditionFailed", staleErr)
	}

	wrappedSkills := &updateErrorSkillStore{SkillStore: st.Skills(), err: staleErr}
	withSkillCommandStore(t, storeWithSkills{Store: st, skills: wrappedSkills})
	_, commandErr := executeSkillCommand(t, "", "update", "stale-skill", "--description", "Changed")
	if commandErr == nil {
		t.Fatal("command stale error = nil")
	}
	for _, want := range []string{
		"stale revision",
		"SKILL.md",
		"re-read with \"loom skill show stale-skill --scope workspace\"",
		"merge the latest content",
	} {
		if !strings.Contains(commandErr.Error(), want) {
			t.Errorf("stale command error %q does not contain %q", commandErr, want)
		}
	}
	if strings.Contains(commandErr.Error(), "provenance conflict") {
		t.Fatalf("stale error rendered as provenance conflict: %v", commandErr)
	}
}

func TestSkillCommandSurfaceIncludesInstall(t *testing.T) {
	cmd := newSkillCommand()
	got := make([]string, 0, len(cmd.Commands()))
	for _, child := range cmd.Commands() {
		got = append(got, child.Name())
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "create,delete,install,list,show,update" {
		t.Fatalf("skill subcommands = %v, want CRUD plus install", got)
	}

	create, _, err := cmd.Find([]string{"create"})
	if err != nil {
		t.Fatalf("find create: %v", err)
	}
	for _, flag := range []string{"description", "content", "scope", "file"} {
		if create.Flags().Lookup(flag) == nil {
			t.Errorf("create is missing --%s", flag)
		}
	}
	if got := create.Flags().Lookup("scope").DefValue; got != "workspace" {
		t.Errorf("create --scope default = %q, want workspace", got)
	}
	if got := create.Flags().Lookup("file").Value.Type(); got != "stringArray" {
		t.Errorf("create --file type = %q, want repeatable stringArray", got)
	}
	install, _, _ := cmd.Find([]string{"install"})
	for _, flag := range []string{"scope", "name"} {
		if install.Flags().Lookup(flag) == nil {
			t.Errorf("install is missing --%s", flag)
		}
	}
	if got := install.Flags().Lookup("scope").DefValue; got != "workspace" {
		t.Errorf("install --scope default = %q, want workspace", got)
	}
	for _, want := range []string{"tree", "one path segment", "@<ref>", "containing /"} {
		if !strings.Contains(install.Long, want) {
			t.Errorf("install Long help %q does not contain %q", install.Long, want)
		}
	}

	list, _, _ := cmd.Find([]string{"list"})
	for _, flag := range []string{"role", "json"} {
		if list.Flags().Lookup(flag) == nil {
			t.Errorf("list is missing --%s", flag)
		}
	}
	show, _, _ := cmd.Find([]string{"show"})
	for _, flag := range []string{"scope", "json"} {
		if show.Flags().Lookup(flag) == nil {
			t.Errorf("show is missing --%s", flag)
		}
	}
	update, _, _ := cmd.Find([]string{"update"})
	for _, flag := range []string{"description", "content", "scope", "file"} {
		if update.Flags().Lookup(flag) == nil {
			t.Errorf("update is missing --%s", flag)
		}
	}
	if update.Flags().Lookup("force") != nil {
		t.Error("update unexpectedly exposes --force")
	}
	deleteCmd, _, _ := cmd.Find([]string{"delete"})
	for _, flag := range []string{"scope", "force"} {
		if deleteCmd.Flags().Lookup(flag) == nil {
			t.Errorf("delete is missing --%s", flag)
		}
	}
}

func TestSkillCreateRejectsBinaryContent(t *testing.T) {
	withoutAgentName(t)
	_, err := executeSkillCommand(t, "binary\x00body", "create", "binary-skill", "--description", "Binary")
	if err == nil || !strings.Contains(err.Error(), "UTF-8 text") {
		t.Fatalf("binary create content error = %v", err)
	}
}

func TestSkillNameAndScopeValidateBeforeStore(t *testing.T) {
	withoutAgentName(t)
	storeCalls := 0
	original := skillWithActiveWorkspace
	skillWithActiveWorkspace = func(func(context.Context, *bootstrap.StoreHandle, string) error) error {
		storeCalls++
		return nil
	}
	t.Cleanup(func() { skillWithActiveWorkspace = original })

	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{name: "invalid name", args: []string{"create", "Bad_Name", "--description", "bad"}, want: "lowercase letters"},
		{name: "invalid scope", args: []string{"show", "valid-name", "--scope", "role"}, want: "role=<name>"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := executeSkillCommand(t, "body", tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
	if storeCalls != 0 {
		t.Fatalf("invalid commands acquired the store %d times, want 0", storeCalls)
	}
}

type storeWithSkills struct {
	store.Store
	skills store.SkillStore
}

func (s storeWithSkills) Skills() store.SkillStore { return s.skills }

type updateErrorSkillStore struct {
	store.SkillStore
	err error
}

func (s *updateErrorSkillStore) Update(context.Context, string, domain.SkillRef, store.SkillUpdate) (*domain.Skill, error) {
	return nil, s.err
}

func executeSkillCommand(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := newSkillCommand()
	var out bytes.Buffer
	cmd.SetArgs(args)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	return out.String(), err
}

func withSkillCommandStore(t *testing.T, st store.Store) {
	t.Helper()
	original := skillWithActiveWorkspace
	skillWithActiveWorkspace = func(fn func(context.Context, *bootstrap.StoreHandle, string) error) error {
		return fn(context.Background(), &bootstrap.StoreHandle{Store: st}, testWorkspace)
	}
	t.Cleanup(func() { skillWithActiveWorkspace = original })
}

func withoutAgentName(t *testing.T) {
	t.Helper()
	value, set := os.LookupEnv("LOOM_AGENT_NAME")
	if err := os.Unsetenv("LOOM_AGENT_NAME"); err != nil {
		t.Fatalf("unset LOOM_AGENT_NAME: %v", err)
	}
	t.Cleanup(func() {
		if set {
			_ = os.Setenv("LOOM_AGENT_NAME", value)
		} else {
			_ = os.Unsetenv("LOOM_AGENT_NAME")
		}
	})
}
