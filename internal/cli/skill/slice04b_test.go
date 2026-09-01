package skill

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

func TestLoadSkillSnapshotRejectsNameAndDescriptionMismatch(t *testing.T) {
	registry.MarkEvidence(t, 14)
	st := memstore.New()
	original := createTestSkill(t, st, domain.WorkspaceSkillRef("identity-tool"), "Matching description", "body\n", "test")

	for _, tc := range []struct {
		name   string
		mutate func(*domain.Skill)
	}{
		{name: "name", mutate: func(skill *domain.Skill) { skill.Name = "different-name" }},
		{name: "description", mutate: func(skill *domain.Skill) { skill.Description = "Different description" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mismatched := *original
			tc.mutate(&mismatched)
			_, err := loadSkillSnapshot(t.Context(), st.WorkspaceFiles(), &mismatched)
			if !errors.Is(err, domain.ErrIntegrity) {
				t.Fatalf("loadSkillSnapshot error = %v, want ErrIntegrity", err)
			}
		})
	}
}

func TestSkillImportHappyPath(t *testing.T) {
	withoutAgentName(t)
	st := memstore.New()
	withSkillCommandStore(t, st)
	directory := writeLocalSkillFixture(t, "local-tool", "Use the local tool", "local body\n")
	script := filepath.Join(directory, "scripts", "check.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatalf("create scripts directory: %v", err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho checked\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	out, err := executeSkillCommand(t, "", "import", directory)
	if err != nil {
		t.Fatalf("import skill: %v", err)
	}
	if !strings.Contains(out, "Imported skill TEST/workspace:local-tool") {
		t.Fatalf("import output = %q", out)
	}
	sk, err := st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("local-tool"))
	if err != nil {
		t.Fatalf("get imported skill: %v", err)
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("canonicalize fixture: %v", err)
	}
	snapshot := loadTestSkillSnapshot(t, st, sk)
	if sk.Description != "Use the local tool" || string(snapshot.Body) != "local body\n" || sk.Source != "import:"+canonical {
		t.Fatalf("imported skill = %+v", sk)
	}
	files := bundledTreeFiles(snapshot.Files)
	if len(files) != 1 || files[0].Path != "scripts/check.sh" || !files[0].Executable {
		t.Fatalf("imported files = %+v", files)
	}
}

func TestSkillImportConvergesWhenCreateLosesRace(t *testing.T) {
	withoutAgentName(t)
	st := memstore.New()
	racing := &createAlreadyExistsAfterPersistSkillStore{SkillStore: st.Skills()}
	withSkillCommandStore(t, storeWithSkills{Store: st, skills: racing})
	directory := writeLocalSkillFixture(t, "racing-import", "Same desired Skill", "body\n")

	out, err := executeSkillCommand(t, "", "import", directory)
	if err != nil {
		t.Fatalf("import after concurrent create: %v", err)
	}
	if !strings.Contains(out, "Updated skill TEST/workspace:racing-import") {
		t.Fatalf("import output = %q", out)
	}
	sk, err := st.Skills().Get(t.Context(), testWorkspace, domain.WorkspaceSkillRef("racing-import"))
	if err != nil {
		t.Fatalf("get converged Skill: %v", err)
	}
	if sk.Description != "Same desired Skill" {
		t.Fatalf("converged Skill description = %q", sk.Description)
	}
}

type createAlreadyExistsAfterPersistSkillStore struct {
	store.SkillStore
}

func (s *createAlreadyExistsAfterPersistSkillStore) Create(ctx context.Context, in store.SkillCreate) (*domain.Skill, error) {
	if _, err := s.SkillStore.Create(ctx, in); err != nil {
		return nil, err
	}
	return nil, domain.ErrAlreadyExists
}

func TestSkillImportPreservesCompleteDocumentUnlessNameOverride(t *testing.T) {
	source := []byte("---\nname: metadata-tool\ndescription: Metadata\nlicense: MIT\nmetadata:\n  owner: test\n---\nbody\n")
	for _, tt := range []struct {
		name, override string
		preserved      bool
	}{
		{name: "default", preserved: true},
		{name: "override", override: "renamed-tool"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			withoutAgentName(t)
			st := memstore.New()
			withSkillCommandStore(t, st)
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, domain.SkillFileNameSKILLMD), source, 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{"import", directory}
			name := "metadata-tool"
			if tt.override != "" {
				args = append(args, "--name", tt.override)
				name = tt.override
			}
			out, err := executeSkillCommand(t, "", args...)
			if err != nil {
				t.Fatal(err)
			}
			sk, err := st.Skills().Get(t.Context(), testWorkspace, domain.WorkspaceSkillRef(name))
			if err != nil {
				t.Fatal(err)
			}
			stored, err := st.WorkspaceFiles().Download(t.Context(), testWorkspace, sk.FileTreeRevision, domain.SkillFileNameSKILLMD)
			if err != nil {
				t.Fatal(err)
			}
			if tt.preserved && !bytes.Equal(stored, source) {
				t.Fatalf("default import changed SKILL.md: %q", stored)
			}
			if !tt.preserved && (!bytes.Contains(stored, []byte("name: renamed-tool")) || bytes.Contains(stored, []byte("license: MIT"))) {
				t.Fatalf("override did not canonically rebuild SKILL.md: %q", stored)
			}
			if tt.preserved == strings.Contains(out, "dropped SKILL.md frontmatter keys") {
				t.Fatalf("dropped-key notice mismatch: %q", out)
			}
			if tt.preserved {
				bundlePath := filepath.Join(directory, "guide.txt")
				if err := os.WriteFile(bundlePath, []byte("guide\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := executeSkillCommand(t, "", "update", name, "--file", bundlePath); err != nil {
					t.Fatalf("file-only update: %v", err)
				}
				updated, err := st.Skills().Get(t.Context(), testWorkspace, domain.WorkspaceSkillRef(name))
				if err != nil {
					t.Fatal(err)
				}
				stored, err = st.WorkspaceFiles().Download(t.Context(), testWorkspace, updated.FileTreeRevision, domain.SkillFileNameSKILLMD)
				if err != nil || !bytes.Equal(stored, source) {
					t.Fatalf("file-only update changed SKILL.md: %q, %v", stored, err)
				}
				if _, err := executeSkillCommand(t, "replacement body\n", "update", name, "--content", "-"); err != nil {
					t.Fatalf("content-only update: %v", err)
				}
				updated, err = st.Skills().Get(t.Context(), testWorkspace, domain.WorkspaceSkillRef(name))
				if err != nil {
					t.Fatal(err)
				}
				stored, err = st.WorkspaceFiles().Download(t.Context(), testWorkspace, updated.FileTreeRevision, domain.SkillFileNameSKILLMD)
				want := bytes.Replace(source, []byte("body\n"), []byte("replacement body\n"), 1)
				if err != nil || !bytes.Equal(stored, want) {
					t.Fatalf("content-only update changed frontmatter: %q, %v", stored, err)
				}
			}
		})
	}
}

func TestSkillImportRefusesSymlinks(t *testing.T) {
	withoutAgentName(t)
	st := memstore.New()
	withSkillCommandStore(t, st)
	directory := writeLocalSkillFixture(t, "linked-tool", "Linked", "body\n")
	if err := os.Symlink("SKILL.md", filepath.Join(directory, "latest")); err != nil {
		t.Fatalf("create symlink fixture: %v", err)
	}

	_, err := executeSkillCommand(t, "", "import", directory)
	if err == nil || !strings.Contains(err.Error(), "contains symlinks") || !strings.Contains(err.Error(), "latest") {
		t.Fatalf("symlink import error = %v", err)
	}
	if _, err := st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("linked-tool")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("symlink import wrote partial skill: %v", err)
	}
}

func TestSkillImportPreservesBinaryFiles(t *testing.T) {
	withoutAgentName(t)
	st := memstore.New()
	withSkillCommandStore(t, st)
	directory := writeLocalSkillFixture(t, "binary-tool", "Binary", "body\n")
	if err := os.WriteFile(filepath.Join(directory, "zero.dat"), []byte("bad\x00data"), 0o644); err != nil {
		t.Fatalf("write NUL fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "utf8.dat"), []byte{0xff, 0xfe}, 0o644); err != nil {
		t.Fatalf("write invalid UTF-8 fixture: %v", err)
	}

	if _, err := executeSkillCommand(t, "", "import", directory); err != nil {
		t.Fatalf("binary import: %v", err)
	}
	sk, err := st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("binary-tool"))
	if err != nil {
		t.Fatal(err)
	}
	files := bundledTreeFiles(loadTestSkillSnapshot(t, st, sk).Files)
	byPath := make(map[string][]byte, len(files))
	for _, file := range files {
		byPath[file.Path] = file.Bytes
	}
	if string(byPath["zero.dat"]) != "bad\x00data" || len(byPath["utf8.dat"]) != 2 || byPath["utf8.dat"][0] != 0xff {
		t.Fatalf("binary files changed: %+v", files)
	}
}

func TestSkillImportGuardedConflictAndForce(t *testing.T) {
	withoutAgentName(t)
	st := memstore.New()
	withSkillCommandStore(t, st)
	st.SetSkillActor("owner-a")
	createTestSkill(t, st, domain.WorkspaceSkillRef("guarded-import"), "Original", "original body\n", manualSkillSource)
	st.SetSkillActor("writer-b")
	directory := writeLocalSkillFixture(t, "guarded-import", "Replacement", "replacement body\n")

	_, err := executeSkillCommand(t, "", "import", directory)
	if err == nil {
		t.Fatal("guarded import conflict = nil")
	}
	for _, want := range []string{"provenance conflict", "owned by owner-a", "re-run with --force", "skill.force_overwrite"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("guarded import error %q does not contain %q", err, want)
		}
	}
	sk, getErr := st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("guarded-import"))
	if getErr != nil || string(loadTestSkillSnapshot(t, st, sk).Body) != "original body\n" {
		t.Fatalf("guarded import changed existing skill: skill=%+v err=%v", sk, getErr)
	}

	out, err := executeSkillCommand(t, "", "import", directory, "--force")
	if err != nil {
		t.Fatalf("force import: %v", err)
	}
	if !strings.Contains(out, "Updated skill TEST/workspace:guarded-import") {
		t.Fatalf("force import output = %q", out)
	}
	sk, err = st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("guarded-import"))
	canonical, canonicalErr := filepath.EvalSymlinks(directory)
	if canonicalErr != nil {
		t.Fatalf("canonicalize import fixture: %v", canonicalErr)
	}
	if err != nil || string(loadTestSkillSnapshot(t, st, sk).Body) != "replacement body\n" || sk.Source != "import:"+canonical {
		t.Fatalf("force-imported skill=%+v err=%v", sk, err)
	}
}

func TestSkillImportCanonicalizesSymlinkedRoot(t *testing.T) {
	withoutAgentName(t)
	st := memstore.New()
	withSkillCommandStore(t, st)
	target := writeLocalSkillFixture(t, "aliased-tool", "Aliased", "body\n")
	alias := filepath.Join(t.TempDir(), "skill-alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatalf("create root alias: %v", err)
	}
	if _, err := executeSkillCommand(t, "", "import", alias); err != nil {
		t.Fatalf("import through symlinked root: %v", err)
	}
	sk, err := st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("aliased-tool"))
	if err != nil {
		t.Fatalf("get aliased import: %v", err)
	}
	canonical, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("canonicalize target: %v", err)
	}
	if sk.Source != "import:"+canonical {
		t.Fatalf("aliased import source = %q, want canonical target", sk.Source)
	}
}

func TestSkillImportRootRefusesAncestorSwapEscape(t *testing.T) {
	root := writeLocalSkillFixture(t, "confined-tool", "Confined", "body\n")
	references := filepath.Join(root, "references")
	if err := os.MkdirAll(references, 0o755); err != nil {
		t.Fatalf("create references: %v", err)
	}
	if err := os.WriteFile(filepath.Join(references, "guide.txt"), []byte("inside\n"), 0o644); err != nil {
		t.Fatalf("write inside guide: %v", err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "guide.txt"), []byte("outside secret\n"), 0o644); err != nil {
		t.Fatalf("write outside guide: %v", err)
	}

	_, err := readLocalSkillDirectoryWithHook(root, "", func(canonicalRoot string) error {
		inside := filepath.Join(canonicalRoot, "references")
		if err := os.Rename(inside, filepath.Join(canonicalRoot, "references-original")); err != nil {
			return err
		}
		return os.Symlink(outside, inside)
	})
	if err == nil {
		t.Fatal("ancestor swap import error = nil, want os.Root confinement failure")
	}
	for _, want := range []string{"references/guide.txt", "path escapes from parent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ancestor swap error %q does not contain %q", err, want)
		}
	}
}

func TestSkillImportSkipsHiddenPathsWithNotice(t *testing.T) {
	withoutAgentName(t)
	st := memstore.New()
	withSkillCommandStore(t, st)
	directory := writeLocalSkillFixture(t, "clean-tool", "Clean", "body\n")
	if err := os.MkdirAll(filepath.Join(directory, ".git"), 0o755); err != nil {
		t.Fatalf("create .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".git", "config"), []byte("secret\n"), 0o644); err != nil {
		t.Fatalf("write .git config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".env"), []byte("TOKEN=secret\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "guide.txt"), []byte("public\n"), 0o644); err != nil {
		t.Fatalf("write guide: %v", err)
	}

	out, err := executeSkillCommand(t, "", "import", directory)
	if err != nil {
		t.Fatalf("import hidden-path fixture: %v", err)
	}
	if !strings.Contains(out, "Notice: skipped hidden skill paths: .env, .git") {
		t.Fatalf("hidden-path notice = %q", out)
	}
	sk, err := st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("clean-tool"))
	if err != nil {
		t.Fatalf("get clean import: %v", err)
	}
	files := bundledTreeFiles(loadTestSkillSnapshot(t, st, sk).Files)
	if len(files) != 1 || files[0].Path != "guide.txt" {
		t.Fatalf("hidden paths were bundled: %+v", files)
	}
}

func TestSkillImportRejectsTooManyFilesBeforeRead(t *testing.T) {
	directory := writeLocalSkillFixture(t, "large-tree", "Large", "body\n")
	for index := 0; index < maxSkillFileCount; index++ {
		name := filepath.Join(directory, fmt.Sprintf("file-%04d.txt", index))
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatalf("write count fixture %d: %v", index, err)
		}
	}
	_, err := readLocalSkillDirectory(directory, "")
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("more than %d regular files", maxSkillFileCount)) {
		t.Fatalf("file-count import error = %v", err)
	}
}

func TestSkillPackAddListRemoveRoundTrip(t *testing.T) {
	withoutAgentName(t)
	st := memstore.New()
	withSkillCommandStore(t, st)

	out, err := executeSkillCommand(t, "", "pack", "add", "design-tools", "owner/repo@v1", "--path", "/catalog/skills/")
	if err != nil {
		t.Fatalf("add pack: %v", err)
	}
	if !strings.Contains(out, "Added skill pack TEST/design-tools") {
		t.Fatalf("add output = %q", out)
	}
	pack, err := st.SkillPacks().Get(context.Background(), testWorkspace, "design-tools")
	if err != nil {
		t.Fatalf("get pack: %v", err)
	}
	if pack.RepoURL != "https://github.com/owner/repo" || pack.Ref != "v1" || pack.Path != "catalog/skills" {
		t.Fatalf("stored pack = %+v", pack)
	}

	listOut, err := executeSkillCommand(t, "", "pack", "list")
	if err != nil {
		t.Fatalf("list packs: %v", err)
	}
	for _, want := range []string{"design-tools", "source=https://github.com/owner/repo/catalog/skills@v1", "last_sync=never"} {
		if !strings.Contains(listOut, want) {
			t.Errorf("pack list %q does not contain %q", listOut, want)
		}
	}

	removeOut, err := executeSkillCommand(t, "", "pack", "remove", "design-tools")
	if err != nil {
		t.Fatalf("remove pack: %v", err)
	}
	if !strings.Contains(removeOut, "Removed skill pack TEST/design-tools") {
		t.Fatalf("remove output = %q", removeOut)
	}
	if _, err := st.SkillPacks().Get(context.Background(), testWorkspace, "design-tools"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("removed pack Get error = %v", err)
	}
}

func TestSlice04bRunHandlersApplyA1Gate(t *testing.T) {
	t.Setenv("LOOM_AGENT_NAME", "task-worker-1")
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "import", run: func() error {
			return runSkillImport(newSkillImportCommand(), ".", skillImportFlags{scope: "workspace"})
		}},
		{name: "pack add", run: func() error {
			return runSkillPackAdd(newSkillPackAddCommand(), "pack", "owner/repo", skillPackAddFlags{})
		}},
		{name: "pack remove", run: func() error { return runSkillPackRemove(newSkillPackRemoveCommand(), "pack") }},
		{name: "sync", run: func() error { return runSkillSync(newSkillSyncCommand(), "") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), "agent-initiated skill writes are deferred") {
				t.Fatalf("RunE gate error = %v", err)
			}
		})
	}
}

func TestSkillSyncDiscoversMultipleSkillsSkipsConflictAndRecordsSync(t *testing.T) {
	withoutAgentName(t)
	st := memstore.New()
	withSkillCommandStore(t, st)
	st.SetSkillActor("owner-a")
	createTestSkill(t, st, domain.WorkspaceSkillRef("conflict"), "Owned elsewhere", "original\n", manualSkillSource)
	st.SetSkillActor("pack-syncer")
	createTestSkill(t, st, domain.WorkspaceSkillRef("updatable"), "Old", "old\n", manualSkillSource)
	createTestSkill(t, st, domain.WorkspaceSkillRef("from-other-pack"), "Other pack", "other pack body\n", domain.SkillPackSource("other-pack"))
	createTestSkill(t, st, domain.WorkspaceSkillRef("same-pack"), "Old same-pack content", "old same-pack body\n", domain.SkillPackSource("toolkit"))

	const commit = "0123456789abcdef0123456789abcdef01234567"
	tarball := buildSkillTarball(t,
		tarFixtureEntry{name: "pax_global_header", kind: tar.TypeXGlobalHeader, pax: map[string]string{"comment": commit}},
		tarFixtureEntry{name: "repo-v1/SKILL.md", mode: 0o644, body: skillDocument("root-tool", "Root tool")},
		tarFixtureEntry{name: "repo-v1/root-guide.txt", mode: 0o644, body: "root guide\n"},
		tarFixtureEntry{name: "repo-v1/direct/SKILL.md", mode: 0o644, body: skillDocument("updatable", "Updated from pack")},
		tarFixtureEntry{name: "repo-v1/from-other/SKILL.md", mode: 0o644, body: skillDocument("from-other-pack", "Must not overwrite other pack")},
		tarFixtureEntry{name: "repo-v1/.agents/skills/beta/SKILL.md", mode: 0o644, body: skillDocument("beta", "Beta")},
		tarFixtureEntry{name: "repo-v1/skills/alpha/SKILL.md", mode: 0o644, body: skillDocument("alpha", "Alpha")},
		tarFixtureEntry{name: "repo-v1/skills/alpha/check.sh", mode: 0o755, body: "#!/bin/sh\n"},
		tarFixtureEntry{name: "repo-v1/skills/bad/SKILL.md", mode: 0o644, body: skillDocument("Bad_Name", "Bad")},
		tarFixtureEntry{name: "repo-v1/skills/conflict/SKILL.md", mode: 0o644, body: skillDocument("conflict", "Conflict")},
		tarFixtureEntry{name: "repo-v1/skills/same/SKILL.md", mode: 0o644, body: skillDocument("same-pack", "Updated same pack")},
	)
	requests := 0
	server := serveTarball(t, tarball, func(_ *http.Request) { requests++ })
	withGitHubInstaller(t, githubSkillInstaller{HTTPClient: server.Client(), CodeloadBaseURL: server.URL})
	if _, err := executeSkillCommand(t, "", "pack", "add", "toolkit", "owner/repo@v1"); err != nil {
		t.Fatalf("add sync pack: %v", err)
	}

	out, err := executeSkillCommand(t, "", "sync", "toolkit")
	if err != nil {
		t.Fatalf("sync pack: %v\n%s", err, out)
	}
	for _, want := range []string{
		"pack toolkit: root-tool: created",
		"pack toolkit: alpha: created",
		"pack toolkit: beta: created",
		"pack toolkit: same-pack: updated",
		"pack toolkit: updatable: skipped: exists with source manual (not this pack)",
		"pack toolkit: from-other-pack: skipped: exists with source pack:other-pack (not this pack)",
		"pack toolkit: conflict: skipped: exists with source manual (not this pack)",
		"pack toolkit: skills/bad: failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sync output %q does not contain %q", out, want)
		}
	}
	if requests != 1 {
		t.Fatalf("codeload requests = %d, want one fetch for the pack", requests)
	}
	root, err := st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("root-tool"))
	if err != nil {
		t.Fatalf("get root skill: %v", err)
	}
	rootFiles := bundledTreeFiles(loadTestSkillSnapshot(t, st, root).Files)
	if root.Source != domain.SkillPackSource("toolkit") || root.SourceRef != commit || len(rootFiles) != 1 || rootFiles[0].Path != "root-guide.txt" {
		t.Fatalf("root skill = %+v; nested skill directories must not become root bundled files", root)
	}
	alpha, err := st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("alpha"))
	if err != nil {
		t.Fatalf("get alpha skill: %v", err)
	}
	alphaFiles := bundledTreeFiles(loadTestSkillSnapshot(t, st, alpha).Files)
	if alpha.Source != domain.SkillPackSource("toolkit") || alpha.SourceRef != commit || len(alphaFiles) != 1 || !alphaFiles[0].Executable {
		t.Fatalf("alpha skill = %+v", alpha)
	}
	updatable, err := st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("updatable"))
	if err != nil || string(loadTestSkillSnapshot(t, st, updatable).Body) != "old\n" || updatable.Source != manualSkillSource {
		t.Fatalf("manual same-actor skill was overwritten: skill=%+v err=%v", updatable, err)
	}
	fromOther, err := st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("from-other-pack"))
	if err != nil || string(loadTestSkillSnapshot(t, st, fromOther).Body) != "other pack body\n" || fromOther.Source != domain.SkillPackSource("other-pack") {
		t.Fatalf("other-pack same-actor skill was overwritten: skill=%+v err=%v", fromOther, err)
	}
	pack, err := st.SkillPacks().Get(context.Background(), testWorkspace, "toolkit")
	if err != nil {
		t.Fatalf("get synced pack: %v", err)
	}
	if pack.LastSyncStatus != domain.SkillPackSyncOK || pack.LastSyncedCommit != commit || strings.Join(pack.LastSyncedSkills, ",") != "alpha,beta,root-tool,same-pack" || pack.LastSyncedAt.IsZero() {
		t.Fatalf("recorded sync = %+v", pack)
	}
}

func TestSkillSyncHonorsPackPath(t *testing.T) {
	withoutAgentName(t)
	st := memstore.New()
	withSkillCommandStore(t, st)
	tarball := buildSkillTarball(t,
		tarFixtureEntry{name: "repo-v1/catalog/skills/inside/SKILL.md", mode: 0o644, body: skillDocument("inside", "Inside")},
		tarFixtureEntry{name: "repo-v1/skills/outside/SKILL.md", mode: 0o644, body: skillDocument("outside", "Outside")},
	)
	server := serveTarball(t, tarball, nil)
	withGitHubInstaller(t, githubSkillInstaller{HTTPClient: server.Client(), CodeloadBaseURL: server.URL})
	if _, err := executeSkillCommand(t, "", "pack", "add", "scoped", "owner/repo@v1", "--path", "catalog"); err != nil {
		t.Fatalf("add scoped pack: %v", err)
	}
	if _, err := executeSkillCommand(t, "", "sync"); err != nil {
		t.Fatalf("sync scoped pack: %v", err)
	}
	inside, err := st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("inside"))
	if err != nil {
		t.Fatalf("get inside skill: %v", err)
	}
	if inside.Source != domain.SkillPackSource("scoped") || inside.SourceRef != "" {
		t.Fatalf("inside provenance = source %q source_ref %q", inside.Source, inside.SourceRef)
	}
	if _, err := st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("outside")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("outside pack-path skill Get error = %v, want not found", err)
	}
	pack, err := st.SkillPacks().Get(context.Background(), testWorkspace, "scoped")
	if err != nil {
		t.Fatalf("get scoped pack: %v", err)
	}
	if pack.LastSyncedCommit != "" {
		t.Fatalf("missing PAX metadata recorded non-commit value %q", pack.LastSyncedCommit)
	}
	listOut, err := executeSkillCommand(t, "", "pack", "list")
	if err != nil {
		t.Fatalf("list pack without resolved commit: %v", err)
	}
	if !strings.Contains(listOut, "last_sync=ok") || strings.Contains(listOut, "commit=") {
		t.Fatalf("pack list did not render missing commit cleanly: %q", listOut)
	}
}

func TestSkillSyncRejectsDuplicateFrontmatterNamesAndContinuesOtherPacks(t *testing.T) {
	withoutAgentName(t)
	st := memstore.New()
	withSkillCommandStore(t, st)
	duplicateTar := buildSkillTarball(t,
		tarFixtureEntry{name: "dup-v1/skills/one/SKILL.md", mode: 0o644, body: skillDocument("same-name", "One")},
		tarFixtureEntry{name: "dup-v1/skills/two/SKILL.md", mode: 0o644, body: skillDocument("same-name", "Two")},
	)
	healthyTar := buildSkillTarball(t,
		tarFixtureEntry{name: "good-v1/SKILL.md", mode: 0o644, body: skillDocument("healthy", "Healthy")},
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/owner/dup/tar.gz/v1":
			_, _ = w.Write(duplicateTar)
		case "/owner/good/tar.gz/v1":
			_, _ = w.Write(healthyTar)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	withGitHubInstaller(t, githubSkillInstaller{HTTPClient: server.Client(), CodeloadBaseURL: server.URL})
	if _, err := executeSkillCommand(t, "", "pack", "add", "a-duplicate", "owner/dup@v1"); err != nil {
		t.Fatalf("add duplicate pack: %v", err)
	}
	if _, err := executeSkillCommand(t, "", "pack", "add", "b-healthy", "owner/good@v1"); err != nil {
		t.Fatalf("add healthy pack: %v", err)
	}

	out, err := executeSkillCommand(t, "", "sync")
	if err == nil {
		t.Fatalf("duplicate pack sync error = nil\n%s", out)
	}
	for _, want := range []string{"duplicate skill names", "same-name", "skills/one", "skills/two", "pack b-healthy: healthy: created"} {
		if !strings.Contains(out+err.Error(), want) {
			t.Errorf("duplicate sync result does not contain %q: output=%q error=%v", want, out, err)
		}
	}
	if _, err := st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("same-name")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("duplicate pack wrote a skill before failing: %v", err)
	}
	if _, err := st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("healthy")); err != nil {
		t.Fatalf("healthy pack did not continue after duplicate failure: %v", err)
	}
	duplicatePack, err := st.SkillPacks().Get(context.Background(), testWorkspace, "a-duplicate")
	if err != nil || duplicatePack.LastSyncStatus != domain.SkillPackSyncFailed || !strings.Contains(duplicatePack.LastSyncError, "skills/one") {
		t.Fatalf("duplicate pack sync record=%+v err=%v", duplicatePack, err)
	}
}

func TestSkillSyncRejectsStoredSourcePathAndPathField(t *testing.T) {
	withoutAgentName(t)
	st := memstore.New()
	withSkillCommandStore(t, st)
	if _, err := st.SkillPacks().Create(context.Background(), store.SkillPackCreate{
		WorkspaceKey: testWorkspace,
		Name:         "inconsistent",
		RepoURL:      "owner/repo/embedded",
		Ref:          "v1",
		Path:         "field-path",
	}); err != nil {
		t.Fatalf("create inconsistent pack: %v", err)
	}

	out, err := executeSkillCommand(t, "", "sync", "inconsistent")
	if err == nil {
		t.Fatalf("inconsistent pack sync error = nil\n%s", out)
	}
	for _, want := range []string{"subpath \"embedded\" embedded in repo source", "nonempty Path \"field-path\"", "only one field"} {
		if !strings.Contains(out+err.Error(), want) {
			t.Errorf("inconsistent pack result does not contain %q: output=%q error=%v", want, out, err)
		}
	}
	pack, getErr := st.SkillPacks().Get(context.Background(), testWorkspace, "inconsistent")
	if getErr != nil || pack.LastSyncStatus != domain.SkillPackSyncFailed {
		t.Fatalf("inconsistent pack sync record=%+v err=%v", pack, getErr)
	}
}

func writeLocalSkillFixture(t *testing.T, name, description, body string) string {
	t.Helper()
	directory := t.TempDir()
	document := "---\nname: " + name + "\ndescription: " + description + "\n---\n" + body
	if err := os.WriteFile(filepath.Join(directory, domain.SkillFileNameSKILLMD), []byte(document), 0o644); err != nil {
		t.Fatalf("write local SKILL.md: %v", err)
	}
	return directory
}

func createTestSkill(t *testing.T, st *memstore.Store, ref domain.SkillRef, description, body, source string) *domain.Skill {
	t.Helper()
	snapshot, err := domain.BuildSkillFileTree(ref.Name, description, []byte(body), nil)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := publishSkillSnapshot(t.Context(), st.WorkspaceFiles(), testWorkspace, *snapshot)
	if err != nil {
		t.Fatal(err)
	}
	sk, err := st.Skills().Create(t.Context(), store.SkillCreate{
		WorkspaceKey: testWorkspace, Ref: ref, Description: description,
		FileTreeRevision: revision, Source: source,
	})
	if err != nil {
		t.Fatal(err)
	}
	return sk
}

func loadTestSkillSnapshot(t *testing.T, st *memstore.Store, sk *domain.Skill) domain.SkillFileTreeSnapshot {
	t.Helper()
	snapshot, err := loadSkillSnapshot(t.Context(), st.WorkspaceFiles(), sk)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func withGitHubInstaller(t *testing.T, installer githubSkillInstaller) {
	t.Helper()
	original := skillGitHubInstaller
	skillGitHubInstaller = installer
	t.Cleanup(func() { skillGitHubInstaller = original })
}
