package skillmat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type staticSkillStore struct {
	store.SkillStore
	skills []*domain.Skill
	err    error
}

func (s staticSkillStore) List(context.Context, string, store.SkillFilter) ([]*domain.Skill, error) {
	return s.skills, s.err
}

type materializeStore struct {
	store.Store
	skills store.SkillStore
}

func (s materializeStore) Skills() store.SkillStore { return s.skills }

type markerRecordingRoot struct {
	secureRoot
	created []string
	renamed [][2]string
}

func (r *markerRecordingRoot) CreateFile(name string, _ []byte, _ os.FileMode) error {
	r.created = append(r.created, name)
	return nil
}

func (r *markerRecordingRoot) Rename(oldName, newName string) error {
	r.renamed = append(r.renamed, [2]string{oldName, newName})
	return nil
}

func (r *markerRecordingRoot) Remove(string) error { return nil }

func TestMaterializeResolvesRoleSkillAndWritesAgentLayout(t *testing.T) {
	target := t.TempDir()
	st := materializeStore{skills: staticSkillStore{skills: []*domain.Skill{
		{
			Name:        "alpha",
			Scope:       domain.SkillScopeWorkspace,
			Description: "workspace skill",
			Content:     "Workspace body\n",
		},
		{
			Name:        "alpha",
			Scope:       domain.SkillScopeRole,
			RoleName:    "lead",
			Description: "role skill",
			Content:     "Role body\n",
			Files: []domain.SkillFile{{
				Path:       "scripts/run.sh",
				Content:    "#!/bin/sh\necho ok\n",
				Executable: true,
			}},
		},
	}}}

	mustMaterialize(t, st, target, "Materialize")

	skillMDPath := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")
	got, err := os.ReadFile(skillMDPath)
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	want := "---\nname: alpha\ndescription: role skill\n---\nRole body\n"
	if string(got) != want {
		t.Fatalf("SKILL.md = %q, want %q", got, want)
	}

	scriptPath := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "scripts", "run.sh")
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("stat script: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("script mode = %v, want executable", info.Mode().Perm())
	}

	linkPath := filepath.Join(target, filepath.FromSlash(ClaudeSkillsDir), "alpha")
	linkTarget, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("read Claude skill link: %v", err)
	}
	if linkTarget != "../../.agents/skills/alpha" {
		t.Fatalf("Claude skill link = %q, want relative canonical target", linkTarget)
	}

	markerBytes, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(markerPath)))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	var gotMarker marker
	if err := json.Unmarshal(markerBytes, &gotMarker); err != nil {
		t.Fatalf("decode marker: %v", err)
	}
	wantPaths := []string{
		".agents/skills/INDEX.md",
		".agents/skills/alpha/SKILL.md",
		".agents/skills/alpha/scripts/run.sh",
		".agents/skills/loom-skill-catalog/SKILL.md",
		".claude/skills/alpha",
		".claude/skills/loom-skill-catalog",
	}
	if !reflect.DeepEqual(gotMarker.Paths, wantPaths) {
		t.Fatalf("marker paths = %#v, want %#v", gotMarker.Paths, wantPaths)
	}
	if gotMarker.Hash == "" {
		t.Fatal("marker hash is empty")
	}
}

//nolint:funlen // The test verifies the complete synthetic catalog projection in one fixture.
func TestMaterializeWritesLiveSkillCatalog(t *testing.T) {
	target := t.TempDir()
	st := materializeStore{skills: staticSkillStore{skills: []*domain.Skill{
		{Name: "zulu", Scope: domain.SkillScopeWorkspace, Description: "Zulu skill", Content: "zulu\n"},
		{Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "Alpha skill", Content: "alpha\n"},
	}}}

	mustMaterialize(t, st, target, "Materialize")

	index, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(indexPath)))
	if err != nil {
		t.Fatalf("read INDEX.md: %v", err)
	}
	wantIndex := "# Loom skills — live catalog\n" +
		"\n" +
		"Current as of the last turn boundary. Loom rewrites this file when skills\n" +
		"change; it supersedes any skill list captured at session start.\n" +
		"\n" +
		"- **alpha** — Alpha skill → read `.agents/skills/alpha/SKILL.md`\n" +
		"- **loom-skill-catalog** — Read this before listing or choosing skills. Loom adds/removes skills between turns; a session-start skill list may be stale. The live catalog is .agents/skills/INDEX.md. → read `.agents/skills/loom-skill-catalog/SKILL.md`\n" +
		"- **zulu** — Zulu skill → read `.agents/skills/zulu/SKILL.md`\n"
	if string(index) != wantIndex {
		t.Fatalf("INDEX.md = %q, want %q", index, wantIndex)
	}

	catalogPath := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), catalogSkillName, domain.SkillFileNameSKILLMD)
	catalog, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("read catalog SKILL.md: %v", err)
	}
	wantCatalog := "---\n" +
		"name: loom-skill-catalog\n" +
		"description: Read this before listing or choosing skills. Loom adds/removes skills between turns; a session-start skill list may be stale. The live catalog is .agents/skills/INDEX.md.\n" +
		"---\n" +
		"Loom manages the skills in this directory centrally. The set can change\n" +
		"between your turns: skills are added, updated, and removed while your\n" +
		"session is running.\n" +
		"\n" +
		"- The authoritative, always-current catalog: `.agents/skills/INDEX.md`.\n" +
		"- Any skill list captured at session start may be stale; prefer INDEX.md.\n" +
		"- To use a skill, read `.agents/skills/<name>/SKILL.md` and follow it.\n"
	if string(catalog) != wantCatalog {
		t.Fatalf("catalog SKILL.md = %q, want %q", catalog, wantCatalog)
	}
	info, err := os.Stat(catalogPath)
	if err != nil {
		t.Fatalf("stat catalog SKILL.md: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("catalog SKILL.md mode = %o, want 644", info.Mode().Perm())
	}
	linkTarget, err := os.Readlink(filepath.Join(target, filepath.FromSlash(ClaudeSkillsDir), catalogSkillName))
	if err != nil {
		t.Fatalf("read catalog compatibility link: %v", err)
	}
	if linkTarget != "../../.agents/skills/loom-skill-catalog" {
		t.Fatalf("catalog compatibility link = %q", linkTarget)
	}
}

func TestMaterializePreservesAngleBracketsInDescription(t *testing.T) {
	t.Parallel()

	target := t.TempDir()
	description := "Use React's <ViewTransition> component"
	st := materializeStore{skills: staticSkillStore{skills: []*domain.Skill{{
		Name: "view-transitions", Scope: domain.SkillScopeWorkspace,
		Description: description, Content: "body\n",
	}}}}

	mustMaterialize(t, st, target, "Materialize")

	skillDocument, err := os.ReadFile(filepath.Join(
		target, filepath.FromSlash(AgentsSkillsDir), "view-transitions", domain.SkillFileNameSKILLMD,
	))
	if err != nil {
		t.Fatalf("read materialized SKILL.md: %v", err)
	}
	if !strings.Contains(string(skillDocument), "description: "+description+"\n") {
		t.Fatalf("materialized SKILL.md did not preserve description: %q", skillDocument)
	}

	index, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(indexPath)))
	if err != nil {
		t.Fatalf("read INDEX.md: %v", err)
	}
	if !strings.Contains(string(index), "**view-transitions** — "+description+" → read") {
		t.Fatalf("INDEX.md did not preserve description: %q", index)
	}
}

func TestMaterializeCatalogAnnotatesShadowedSkill(t *testing.T) {
	target := t.TempDir()
	st := materializeStore{skills: staticSkillStore{skills: []*domain.Skill{
		{Name: "review", Scope: domain.SkillScopeWorkspace, Description: "workspace review", Content: "workspace\n"},
		{Name: "review", Scope: domain.SkillScopeRole, RoleName: "lead", Description: "lead review", Content: "lead\n"},
	}}}

	mustMaterialize(t, st, target, "Materialize")
	index, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(indexPath)))
	if err != nil {
		t.Fatalf("read INDEX.md: %v", err)
	}
	wantLine := "- **review** — lead review (overrides the workspace skill of the same name) → read `.agents/skills/review/SKILL.md`\n"
	if !strings.Contains(string(index), wantLine) {
		t.Fatalf("INDEX.md = %q, want shadow annotation %q", index, wantLine)
	}
	if strings.Contains(string(index), "workspace review") {
		t.Fatalf("INDEX.md includes shadowed workspace description: %q", index)
	}
}

func TestMaterializeRewritesCatalogAfterSkillRemoval(t *testing.T) {
	target := t.TempDir()
	alpha := &domain.Skill{Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "Alpha skill", Content: "alpha\n"}
	beta := &domain.Skill{Name: "beta", Scope: domain.SkillScopeWorkspace, Description: "Beta skill", Content: "beta\n"}
	skills := &staticSkillStore{skills: []*domain.Skill{alpha, beta}}
	st := materializeStore{skills: skills}
	mustMaterialize(t, st, target, "initial Materialize")

	skills.skills = []*domain.Skill{alpha}
	mustMaterialize(t, st, target, "Materialize after removal")
	index, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(indexPath)))
	if err != nil {
		t.Fatalf("read rewritten INDEX.md: %v", err)
	}
	if strings.Contains(string(index), "**beta**") || !strings.Contains(string(index), "**alpha**") {
		t.Fatalf("rewritten INDEX.md = %q, want alpha without beta", index)
	}
	for _, removed := range []string{
		filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "beta"),
		filepath.Join(target, filepath.FromSlash(ClaudeSkillsDir), "beta"),
	} {
		if _, err := os.Lstat(removed); !os.IsNotExist(err) {
			t.Fatalf("removed skill path %q still exists or failed unexpectedly: %v", removed, err)
		}
	}
}

func TestMaterializeZeroSkillsWritesCatalogOnly(t *testing.T) {
	target := t.TempDir()
	if err := materialize(t.Context(), materializeStore{skills: staticSkillStore{}}, "WS", "lead", target); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	index, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(indexPath)))
	if err != nil {
		t.Fatalf("read INDEX.md: %v", err)
	}
	wantIndex := skillIndexPreamble +
		"- **loom-skill-catalog** — " + catalogSkillDescription + " → read `.agents/skills/loom-skill-catalog/SKILL.md`\n"
	if string(index) != wantIndex {
		t.Fatalf("INDEX.md = %q, want catalog-only index %q", index, wantIndex)
	}
	if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), catalogSkillName, domain.SkillFileNameSKILLMD)); err != nil {
		t.Fatalf("stat catalog SKILL.md: %v", err)
	}
	if _, err := os.Readlink(filepath.Join(target, filepath.FromSlash(ClaudeSkillsDir), catalogSkillName)); err != nil {
		t.Fatalf("read catalog compatibility link: %v", err)
	}

	markerBytes, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(markerPath)))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	var gotMarker marker
	if err := json.Unmarshal(markerBytes, &gotMarker); err != nil {
		t.Fatalf("decode marker: %v", err)
	}
	wantPaths := []string{
		indexPath,
		path.Join(AgentsSkillsDir, catalogSkillName, domain.SkillFileNameSKILLMD),
		path.Join(ClaudeSkillsDir, catalogSkillName),
	}
	if !reflect.DeepEqual(gotMarker.Paths, wantPaths) {
		t.Fatalf("marker paths = %#v, want %#v", gotMarker.Paths, wantPaths)
	}
	for _, managedPath := range wantPaths {
		if !validManagedPath(managedPath) {
			t.Fatalf("synthetic marker path %q is not valid", managedPath)
		}
	}
}

func TestMaterializeRejectsUnmanagedSkillIndex(t *testing.T) {
	target := t.TempDir()
	indexPath := filepath.Join(target, filepath.FromSlash(indexPath))
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		t.Fatalf("create skill directory: %v", err)
	}
	if err := os.WriteFile(indexPath, []byte("user-managed index\n"), 0o644); err != nil {
		t.Fatalf("write unmanaged INDEX.md: %v", err)
	}

	err := materialize(t.Context(), materializeStore{skills: staticSkillStore{}}, "WS", "lead", target)
	if err == nil || !strings.Contains(err.Error(), "INDEX.md") || !strings.Contains(err.Error(), "unrecorded") {
		t.Fatalf("Materialize error = %v, want unmanaged INDEX.md collision", err)
	}
	got, readErr := os.ReadFile(indexPath)
	if readErr != nil || string(got) != "user-managed index\n" {
		t.Fatalf("unmanaged INDEX.md changed: content=%q err=%v", got, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(target, filepath.FromSlash(markerPath))); !os.IsNotExist(statErr) {
		t.Fatalf("marker exists after collision: %v", statErr)
	}
}

func TestMaterializeMatchingHashIsNoOp(t *testing.T) {
	target := t.TempDir()
	skills := &staticSkillStore{skills: []*domain.Skill{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "database body\n",
	}}}
	st := materializeStore{skills: skills}
	mustMaterialize(t, st, target, "first Materialize")
	skillMD := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")
	if err := os.WriteFile(skillMD, []byte("agent-authored working copy\n"), 0o644); err != nil {
		t.Fatalf("edit materialized copy: %v", err)
	}

	mustMaterialize(t, st, target, "second Materialize")
	got, err := os.ReadFile(skillMD)
	if err != nil {
		t.Fatalf("read working copy: %v", err)
	}
	if string(got) != "agent-authored working copy\n" {
		t.Fatalf("matching hash rewrote SKILL.md: %q", got)
	}
}

// A stored skill fleet-db should have refused at write time must not be able to
// stop materialization for the whole workspace: every Materialize caller treats
// a non-StoreUnavailable error as fatal, so one bad record would take skills
// down for every agent until it was deleted.
func TestMaterializeSkipsUnprojectableSkillsWithoutFailing(t *testing.T) {
	tests := []struct {
		name string
		bad  *domain.Skill
	}{
		{
			name: "name reserved for the catalog pointer",
			bad: &domain.Skill{
				Name: catalogSkillName, Scope: domain.SkillScopeWorkspace,
				Description: "smuggled past write-time validation", Content: "body\n",
			},
		},
		{
			name: "bundled path escapes the skill directory",
			bad: &domain.Skill{
				Name: "beta", Scope: domain.SkillScopeWorkspace, Description: "beta", Content: "body\n",
				Files: []domain.SkillFile{{Path: "../escape.md", Content: "nope\n"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := t.TempDir()
			good := &domain.Skill{
				Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
			}
			st := materializeStore{skills: staticSkillStore{skills: []*domain.Skill{tt.bad, good}}}
			mustMaterialize(t, st, target, "Materialize with one unprojectable skill")

			if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")); err != nil {
				t.Fatalf("healthy skill was not materialized: %v", err)
			}
			index, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(indexPath)))
			if err != nil {
				t.Fatalf("read catalog index: %v", err)
			}
			if !strings.Contains(string(index), "**alpha**") {
				t.Fatalf("catalog index omits the healthy skill:\n%s", index)
			}
			// The index must not advertise a skill that was never written.
			if strings.Contains(string(index), "**beta**") {
				t.Fatalf("catalog index advertises a skipped skill:\n%s", index)
			}
			if _, err := os.Stat(filepath.Join(target, "escape.md")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("bundled path escaped the target: stat err = %v", err)
			}
		})
	}
}

func TestMaterializeMatchingHashReconcilesProjectionDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, target string)
	}{
		{
			name: "missing managed file",
			mutate: func(t *testing.T, target string) {
				t.Helper()
				if err := os.Remove(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")); err != nil {
					t.Fatalf("remove managed file: %v", err)
				}
			},
		},
		{
			name: "executable mode drift",
			mutate: func(t *testing.T, target string) {
				t.Helper()
				if err := os.Chmod(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "run.sh"), 0o644); err != nil {
					t.Fatalf("remove executable mode: %v", err)
				}
			},
		},
		{
			name: "managed file replaced by symlink",
			mutate: func(t *testing.T, target string) {
				t.Helper()
				managed := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")
				if err := os.Remove(managed); err != nil {
					t.Fatalf("remove managed file: %v", err)
				}
				outside := filepath.Join(target, "outside")
				if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
					t.Fatalf("write outside file: %v", err)
				}
				if err := os.Symlink(outside, managed); err != nil {
					t.Fatalf("replace managed file with symlink: %v", err)
				}
			},
		},
		{
			name: "managed compatibility link retargeted",
			mutate: func(t *testing.T, target string) {
				t.Helper()
				link := filepath.Join(target, filepath.FromSlash(ClaudeSkillsDir), "alpha")
				if err := os.Remove(link); err != nil {
					t.Fatalf("remove managed link: %v", err)
				}
				if err := os.Symlink("../../wrong", link); err != nil {
					t.Fatalf("retarget managed link: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := t.TempDir()
			st := materializeStore{skills: staticSkillStore{skills: []*domain.Skill{{
				Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
				Files: []domain.SkillFile{{Path: "run.sh", Content: "#!/bin/sh\n", Executable: true}},
			}}}}
			mustMaterialize(t, st, target, "first Materialize")
			tt.mutate(t, target)

			mustMaterialize(t, st, target, "second Materialize")
			skillMD := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")
			info, err := os.Lstat(skillMD)
			if err != nil {
				t.Fatalf("lstat reconciled SKILL.md: %v", err)
			}
			if !info.Mode().IsRegular() {
				t.Fatalf("SKILL.md mode = %v, want regular file", info.Mode())
			}
			scriptInfo, err := os.Stat(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "run.sh"))
			if err != nil {
				t.Fatalf("stat reconciled executable: %v", err)
			}
			if scriptInfo.Mode().Perm() != 0o755 {
				t.Fatalf("run.sh mode = %o, want 755", scriptInfo.Mode().Perm())
			}
			linkTarget, err := os.Readlink(filepath.Join(target, filepath.FromSlash(ClaudeSkillsDir), "alpha"))
			if err != nil {
				t.Fatalf("read reconciled compatibility link: %v", err)
			}
			if linkTarget != "../../.agents/skills/alpha" {
				t.Fatalf("compatibility link target = %q", linkTarget)
			}
		})
	}
}

func TestMaterializeRestoresManagedFileReplacedByEmptyDirectory(t *testing.T) {
	target := t.TempDir()
	st := materializeStore{skills: staticSkillStore{skills: []*domain.Skill{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
	}}}}
	mustMaterialize(t, st, target, "initial Materialize")
	skillMD := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")
	if err := os.Remove(skillMD); err != nil {
		t.Fatalf("remove managed SKILL.md: %v", err)
	}
	if err := os.Mkdir(skillMD, 0o755); err != nil {
		t.Fatalf("replace managed SKILL.md with directory: %v", err)
	}

	mustMaterialize(t, st, target, "reconcile Materialize")
	body, err := os.ReadFile(skillMD)
	if err != nil {
		t.Fatalf("read restored SKILL.md: %v", err)
	}
	if got, want := string(body), "---\nname: alpha\ndescription: alpha\n---\nbody\n"; got != want {
		t.Fatalf("restored SKILL.md = %q, want %q", got, want)
	}
}

func TestMaterializeKeepsPersistingSkillsReadableWhileUpdatingAndDeleting(t *testing.T) {
	target := t.TempDir()
	version := func(body, fileBody string) *domain.Skill {
		files := make([]domain.SkillFile, 8)
		for i := range files {
			files[i] = domain.SkillFile{
				Path:    fmt.Sprintf("references/file-%02d.md", i),
				Content: strings.Repeat(fileBody, 128),
			}
		}
		return &domain.Skill{
			Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: body, Files: files,
		}
	}
	alphaA := version("version A\n", "A")
	alphaB := version("version B\n", "B")
	beta := &domain.Skill{
		Name: "beta", Scope: domain.SkillScopeWorkspace, Description: "beta", Content: "persistent beta\n",
		Files: []domain.SkillFile{{Path: "references/one.md", Content: "one\n"}, {Path: "references/two.md", Content: "two\n"}},
	}
	deleted := &domain.Skill{Name: "deleted", Scope: domain.SkillScopeWorkspace, Description: "deleted", Content: "remove me\n"}
	skills := &staticSkillStore{skills: []*domain.Skill{alphaA, beta, deleted}}
	st := materializeStore{skills: skills}
	mustMaterialize(t, st, target, "initial Materialize")

	alphaPath := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")
	betaPath := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "beta", "SKILL.md")
	wantAlpha := map[string]bool{
		"---\nname: alpha\ndescription: alpha\n---\nversion A\n": true,
		"---\nname: alpha\ndescription: alpha\n---\nversion B\n": true,
	}
	wantBeta := "---\nname: beta\ndescription: beta\n---\npersistent beta\n"

	stop := make(chan struct{})
	readerDone := make(chan struct{})
	readerErr := make(chan error, 1)
	var reads atomic.Int64
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, check := range []struct {
				name    string
				path    string
				content func(string) bool
			}{
				{name: "alpha", path: alphaPath, content: func(got string) bool { return wantAlpha[got] }},
				{name: "beta", path: betaPath, content: func(got string) bool { return got == wantBeta }},
			} {
				body, err := os.ReadFile(check.path)
				if err != nil {
					readerErr <- fmt.Errorf("read persistent %s SKILL.md: %w", check.name, err)
					return
				}
				if !check.content(string(body)) {
					readerErr <- fmt.Errorf("persistent %s SKILL.md contained partial or unknown content %q", check.name, body)
					return
				}
				info, err := os.Stat(check.path)
				if err != nil {
					readerErr <- fmt.Errorf("stat persistent %s SKILL.md: %w", check.name, err)
					return
				}
				if !info.Mode().IsRegular() {
					readerErr <- fmt.Errorf("persistent %s SKILL.md mode = %v, want regular file", check.name, info.Mode())
					return
				}
				reads.Add(1)
			}
		}
	}()

	for i := 0; i < 64; i++ {
		alpha := alphaA
		if i%2 == 0 {
			alpha = alphaB
		}
		skills.skills = []*domain.Skill{alpha, beta}
		if err := materialize(t.Context(), st, "WS", "lead", target); err != nil {
			close(stop)
			<-readerDone
			t.Fatalf("Materialize pass %d: %v", i+1, err)
		}
		select {
		case err := <-readerErr:
			close(stop)
			<-readerDone
			t.Fatal(err)
		default:
		}
	}
	close(stop)
	<-readerDone
	select {
	case err := <-readerErr:
		t.Fatal(err)
	default:
	}
	if got := reads.Load(); got < 100 {
		t.Fatalf("live reader completed %d reads, want at least 100", got)
	}
	deletedDir := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "deleted")
	if _, err := os.Stat(deletedDir); !os.IsNotExist(err) {
		t.Fatalf("deleted skill directory still exists or failed unexpectedly: %v", err)
	}
}

func TestMaterializeAtomicallyUpdatesContentAndExecutableMode(t *testing.T) {
	target := t.TempDir()
	skill := &domain.Skill{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
		Files: []domain.SkillFile{{Path: "scripts/run.sh", Content: "old\n"}},
	}
	skills := &staticSkillStore{skills: []*domain.Skill{skill}}
	st := materializeStore{skills: skills}
	mustMaterialize(t, st, target, "initial Materialize")

	skill.Files = []domain.SkillFile{{Path: "scripts/run.sh", Content: "#!/bin/sh\necho new\n", Executable: true}}
	mustMaterialize(t, st, target, "updated Materialize")

	script := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "scripts", "run.sh")
	body, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read updated script: %v", err)
	}
	if got, want := string(body), "#!/bin/sh\necho new\n"; got != want {
		t.Fatalf("updated script = %q, want %q", got, want)
	}
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("stat updated script: %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o755); got != want {
		t.Fatalf("updated script mode = %o, want %o", got, want)
	}
}

func TestMaterializeTransitionsCaseFoldCollidingManagedPath(t *testing.T) {
	target := t.TempDir()
	skill := &domain.Skill{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
		Files: []domain.SkillFile{{Path: "Docs/a.md", Content: "old\n"}},
	}
	skills := &staticSkillStore{skills: []*domain.Skill{skill}}
	st := materializeStore{skills: skills}
	mustMaterialize(t, st, target, "initial Materialize")

	skill.Files = []domain.SkillFile{{Path: "docs/A.md", Content: "new\n"}}
	mustMaterialize(t, st, target, "transition Materialize")

	newPath := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "docs", "A.md")
	if body, err := os.ReadFile(newPath); err != nil || string(body) != "new\n" {
		t.Fatalf("new case-folded path = %q, err=%v", body, err)
	}
	var relativeFiles []string
	skillRoot := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha")
	err := filepath.WalkDir(skillRoot, func(name string, item os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if item.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(skillRoot, name)
		if err != nil {
			return err
		}
		relativeFiles = append(relativeFiles, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk transitioned skill: %v", err)
	}
	sort.Strings(relativeFiles)
	if want := []string{"SKILL.md", "docs/A.md"}; !reflect.DeepEqual(relativeFiles, want) {
		t.Fatalf("materialized files = %#v, want %#v", relativeFiles, want)
	}
}

func TestMaterializeSweepsCrashOrphanedProjectionTemporary(t *testing.T) {
	target := t.TempDir()
	st := materializeStore{skills: staticSkillStore{skills: []*domain.Skill{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
	}}}}
	mustMaterialize(t, st, target, "initial Materialize")
	orphanName := projectionTempPrefix + "crash-orphan"
	orphanPath := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", orphanName)
	if err := os.WriteFile(orphanPath, []byte("incomplete\n"), 0o600); err != nil {
		t.Fatalf("plant crash orphan: %v", err)
	}

	mustMaterialize(t, st, target, "reconcile after crash orphan")
	if _, err := os.Lstat(orphanPath); !os.IsNotExist(err) {
		t.Fatalf("crash-orphaned temporary still exists or failed unexpectedly: %v", err)
	}
	markerBytes, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(markerPath)))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	var gotMarker marker
	if err := json.Unmarshal(markerBytes, &gotMarker); err != nil {
		t.Fatalf("decode marker: %v", err)
	}
	for _, recorded := range gotMarker.Paths {
		if strings.Contains(recorded, projectionTempPrefix) {
			t.Fatalf("marker recorded projection temporary %q", recorded)
		}
	}
	if validManagedPath(path.Join(AgentsSkillsDir, "alpha", orphanName)) {
		t.Fatalf("projection temporary %q is valid as a managed marker path", orphanName)
	}
}

func TestMaterializeRecoversExactPartialProjectionWithoutMarker(t *testing.T) {
	target := t.TempDir()
	skill := &domain.Skill{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
		Files: []domain.SkillFile{{Path: "run.sh", Content: "#!/bin/sh\n", Executable: true}},
	}
	entries := desiredEntries(domain.ResolveSkillChainDetail([]*domain.Skill{skill}, "lead"))
	partial := entries[0]
	partialPath := filepath.Join(target, filepath.FromSlash(partial.Path))
	if err := os.MkdirAll(filepath.Dir(partialPath), 0o755); err != nil {
		t.Fatalf("create partial projection parent: %v", err)
	}
	if err := os.WriteFile(partialPath, partial.Content, partial.Mode); err != nil {
		t.Fatalf("write exact partial projection: %v", err)
	}
	if err := os.Chmod(partialPath, partial.Mode); err != nil {
		t.Fatalf("set exact partial projection mode: %v", err)
	}

	st := materializeStore{skills: staticSkillStore{skills: []*domain.Skill{skill}}}
	mustMaterialize(t, st, target, "Materialize after partial write")
	if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(markerPath))); err != nil {
		t.Fatalf("stat recovered marker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "run.sh")); err != nil {
		t.Fatalf("stat remaining projection: %v", err)
	}
}

func TestWriteMarkerAtomicallyRenamesCompletedTemporaryFile(t *testing.T) {
	root := &markerRecordingRoot{}
	if err := writeMarkerAtomically(root, []byte("complete marker\n")); err != nil {
		t.Fatalf("writeMarkerAtomically: %v", err)
	}
	if len(root.created) != 1 || root.created[0] == markerPath || !strings.HasPrefix(root.created[0], markerPath+".tmp-") {
		t.Fatalf("created paths = %#v, want one marker temporary", root.created)
	}
	if len(root.renamed) != 1 || root.renamed[0] != [2]string{root.created[0], markerPath} {
		t.Fatalf("renames = %#v, want temporary -> marker", root.renamed)
	}
}

func TestMaterializeRemovalPreservesUnrecordedFiles(t *testing.T) {
	target := t.TempDir()
	skills := &staticSkillStore{skills: []*domain.Skill{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
		Files: []domain.SkillFile{{Path: "references/managed.md", Content: "managed\n"}},
	}}}
	st := materializeStore{skills: skills}
	mustMaterialize(t, st, target, "first Materialize")
	unrecorded := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "notes.user")
	if err := os.WriteFile(unrecorded, []byte("keep me\n"), 0o644); err != nil {
		t.Fatalf("write unrecorded file: %v", err)
	}

	skills.skills = nil
	mustMaterialize(t, st, target, "remove Materialize")
	got, err := os.ReadFile(unrecorded)
	if err != nil {
		t.Fatalf("unrecorded file was removed: %v", err)
	}
	if string(got) != "keep me\n" {
		t.Fatalf("unrecorded file = %q", got)
	}
	for _, removed := range []string{
		filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md"),
		filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "references", "managed.md"),
		filepath.Join(target, filepath.FromSlash(ClaudeSkillsDir), "alpha"),
	} {
		if _, err := os.Lstat(removed); !os.IsNotExist(err) {
			t.Fatalf("recorded path %q still exists or failed unexpectedly: %v", removed, err)
		}
	}
	markerBytes, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(markerPath)))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	var gotMarker marker
	if err := json.Unmarshal(markerBytes, &gotMarker); err != nil {
		t.Fatalf("decode marker: %v", err)
	}
	wantPaths := []string{
		indexPath,
		path.Join(AgentsSkillsDir, catalogSkillName, domain.SkillFileNameSKILLMD),
		path.Join(ClaudeSkillsDir, catalogSkillName),
	}
	if !reflect.DeepEqual(gotMarker.Paths, wantPaths) {
		t.Fatalf("marker paths after removal = %#v, want %#v", gotMarker.Paths, wantPaths)
	}
}

func TestMaterializeRoleDeletionUnshadowsWorkspaceSkill(t *testing.T) {
	target := t.TempDir()
	workspaceSkill := &domain.Skill{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "workspace", Content: "workspace body\n",
	}
	skills := &staticSkillStore{skills: []*domain.Skill{
		workspaceSkill,
		{Name: "alpha", Scope: domain.SkillScopeRole, RoleName: "lead", Description: "role", Content: "role body\n"},
	}}
	st := materializeStore{skills: skills}
	mustMaterialize(t, st, target, "shadowed Materialize")

	skills.skills = []*domain.Skill{workspaceSkill}
	mustMaterialize(t, st, target, "unshadowed Materialize")
	got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md"))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	if !strings.Contains(string(got), "description: workspace\n") || !strings.HasSuffix(string(got), "workspace body\n") {
		t.Fatalf("unshadowed SKILL.md = %q", got)
	}
}

// Paths that would collide when written must never both be materialized. The
// skill carrying them is dropped rather than the projection failing, for the
// same reason as any other unprojectable record: one malformed skill must not
// stop every agent in the workspace.
func TestMaterializeRefusesToWriteInSkillPathCollisions(t *testing.T) {
	tests := []struct {
		name  string
		files []domain.SkillFile
		// paths that must not exist under the skill directory afterwards
		unwritten []string
	}{
		{
			name:      "reserved body name under different case",
			files:     []domain.SkillFile{{Path: "skill.md", Content: "collision"}},
			unwritten: []string{"skill.md"},
		},
		{
			name: "unicode normalization",
			files: []domain.SkillFile{
				{Path: "references/caf\u00e9.md", Content: "NFC"},
				{Path: "references/cafe\u0301.md", Content: "NFD"},
			},
			unwritten: []string{"references/caf\u00e9.md", "references/cafe\u0301.md"},
		},
		{
			name: "file versus directory",
			files: []domain.SkillFile{
				{Path: "a/b", Content: "file"},
				{Path: "a/b/c", Content: "child"},
			},
			unwritten: []string{"a/b", "a/b/c"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := t.TempDir()
			healthy := &domain.Skill{
				Name: "beta", Scope: domain.SkillScopeWorkspace, Description: "beta", Content: "beta body\n",
			}
			st := materializeStore{skills: staticSkillStore{skills: []*domain.Skill{{
				Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body", Files: tt.files,
			}, healthy}}}
			mustMaterialize(t, st, target, "Materialize with a colliding skill")

			skillDir := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha")
			if _, statErr := os.Lstat(skillDir); !os.IsNotExist(statErr) {
				t.Fatalf("colliding skill was materialized: stat err = %v", statErr)
			}
			for _, unwritten := range tt.unwritten {
				if _, statErr := os.Lstat(filepath.Join(skillDir, filepath.FromSlash(unwritten))); !os.IsNotExist(statErr) {
					t.Fatalf("colliding path %q was written: stat err = %v", unwritten, statErr)
				}
			}
			// The rest of the workspace still materializes.
			if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "beta", "SKILL.md")); err != nil {
				t.Fatalf("healthy skill was not materialized: %v", err)
			}
			index, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(indexPath)))
			if err != nil {
				t.Fatalf("read catalog index: %v", err)
			}
			if strings.Contains(string(index), "**alpha**") {
				t.Fatalf("catalog index advertises the skipped skill:\n%s", index)
			}
		})
	}
}

func TestMaterializeRejectsExistingCaseFoldCollision(t *testing.T) {
	target := t.TempDir()
	skillDir := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("create user skill dir: %v", err)
	}
	existing := filepath.Join(skillDir, "README.md")
	if err := os.WriteFile(existing, []byte("user file\n"), 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}
	st := materializeStore{skills: staticSkillStore{skills: []*domain.Skill{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body",
		Files: []domain.SkillFile{{Path: "readme.md", Content: "managed"}},
	}}}}

	err := materialize(t.Context(), st, "WS", "lead", target)
	if err == nil {
		t.Fatal("Materialize error = nil, want collision")
	}
	for _, want := range []string{"readme.md", "README.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Materialize error = %q, want path %q", err, want)
		}
	}
	got, readErr := os.ReadFile(existing)
	if readErr != nil || string(got) != "user file\n" {
		t.Fatalf("existing file changed: content=%q err=%v", got, readErr)
	}
}

func TestMaterializeRefusesSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	outsideHooks := filepath.Join(base, "outside", ".git", "hooks")
	if err := os.MkdirAll(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha"), 0o755); err != nil {
		t.Fatalf("create planted skill dir: %v", err)
	}
	if err := os.MkdirAll(outsideHooks, 0o755); err != nil {
		t.Fatalf("create outside hooks: %v", err)
	}
	link := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "link")
	if err := os.Symlink("../../../../outside/.git/hooks", link); err != nil {
		t.Fatalf("plant escape symlink: %v", err)
	}
	st := materializeStore{skills: staticSkillStore{skills: []*domain.Skill{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body",
		Files: []domain.SkillFile{{Path: "link/pre-commit", Content: "#!/bin/sh\n", Executable: true}},
	}}}}

	err := materialize(t.Context(), st, "WS", "lead", target)
	if err == nil {
		t.Fatal("Materialize error = nil, want planted symlink refusal")
	}
	for _, want := range []string{"link/pre-commit", ".agents/skills/alpha/link"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Materialize error = %q, want both colliding paths including %q", err, want)
		}
	}
	if _, statErr := os.Lstat(filepath.Join(outsideHooks, "pre-commit")); !os.IsNotExist(statErr) {
		t.Fatalf("escape path was written outside target: %v", statErr)
	}
}

func TestMaterializeRelativeClaudeLinkSurvivesTargetMove(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "before")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("create target: %v", err)
	}
	st := materializeStore{skills: staticSkillStore{skills: []*domain.Skill{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
	}}}}
	mustMaterialize(t, st, target, "Materialize")
	moved := filepath.Join(base, "after")
	if err := os.Rename(target, moved); err != nil {
		t.Fatalf("move target: %v", err)
	}
	throughLink := filepath.Join(moved, filepath.FromSlash(ClaudeSkillsDir), "alpha", "SKILL.md")
	got, err := os.ReadFile(throughLink)
	if err != nil {
		t.Fatalf("read through moved relative link: %v", err)
	}
	if !strings.HasSuffix(string(got), "body\n") {
		t.Fatalf("SKILL.md through moved link = %q", got)
	}
}

func TestMaterializeStoreOutageLeavesProjectionUntouched(t *testing.T) {
	target := t.TempDir()
	available := materializeStore{skills: staticSkillStore{skills: []*domain.Skill{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "old body\n",
	}}}}
	if err := materialize(t.Context(), available, "WS", "lead", target); err != nil {
		t.Fatalf("initial Materialize: %v", err)
	}
	skillMD := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")
	before, err := os.ReadFile(skillMD)
	if err != nil {
		t.Fatalf("read initial projection: %v", err)
	}
	outage := &url.Error{Op: "GET", URL: "http://fleet-db/skills", Err: syscall.ECONNREFUSED}
	unavailable := materializeStore{skills: staticSkillStore{err: outage}}
	err = materialize(t.Context(), unavailable, "WS", "lead", target)
	if !IsStoreUnavailable(err) || !errors.Is(err, outage) {
		t.Fatalf("Materialize error = %v, want store-unavailable wrapper", err)
	}
	after, readErr := os.ReadFile(skillMD)
	if readErr != nil {
		t.Fatalf("read projection after outage: %v", readErr)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("projection changed during outage:\nbefore=%q\nafter=%q", before, after)
	}
}

func TestMaterializeDoesNotDegradeOnNonOutageStoreError(t *testing.T) {
	target := t.TempDir()
	denied := fmt.Errorf("skill list forbidden: %w", domain.ErrConflict)
	st := materializeStore{skills: staticSkillStore{err: denied}}
	err := materialize(t.Context(), st, "WS", "lead", target)
	if err == nil || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Materialize error = %v, want store error", err)
	}
	if IsStoreUnavailable(err) {
		t.Fatalf("authorization error classified as outage: %v", err)
	}
}

func TestMaterializeDoesNotClassifyCancellationAsStoreOutage(t *testing.T) {
	target := t.TempDir()
	st := materializeStore{skills: staticSkillStore{err: context.Canceled}}
	err := materialize(t.Context(), st, "WS", "lead", target)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Materialize error = %v, want context.Canceled", err)
	}
	if IsStoreUnavailable(err) {
		t.Fatalf("context cancellation classified as store outage: %v", err)
	}
}

func TestMaterializeRejectsOversizedAndPartialMarkersWithoutCleanup(t *testing.T) {
	tests := []struct {
		name       string
		markerBody []byte
		want       string
	}{
		{name: "oversized", markerBody: bytes.Repeat([]byte("x"), (1<<20)+1), want: "maximum size"},
		{name: "partial JSON", markerBody: []byte(`{"version":1`), want: "decode skill marker"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := t.TempDir()
			markerPath := filepath.Join(target, filepath.FromSlash(markerPath))
			if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
				t.Fatalf("create marker parent: %v", err)
			}
			if err := os.WriteFile(markerPath, tt.markerBody, 0o644); err != nil {
				t.Fatalf("write marker: %v", err)
			}
			sentinel := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "keep.user")
			if err := os.WriteFile(sentinel, []byte("keep\n"), 0o644); err != nil {
				t.Fatalf("write sentinel: %v", err)
			}

			err := materialize(t.Context(), materializeStore{skills: staticSkillStore{}}, "WS", "lead", target)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Materialize error = %v, want %q", err, tt.want)
			}
			if got, readErr := os.ReadFile(sentinel); readErr != nil || string(got) != "keep\n" {
				t.Fatalf("sentinel changed after marker rejection: content=%q err=%v", got, readErr)
			}
			gotMarker, readErr := os.ReadFile(markerPath)
			if readErr != nil || !bytes.Equal(gotMarker, tt.markerBody) {
				t.Fatalf("marker changed after rejection: size=%d err=%v", len(gotMarker), readErr)
			}
		})
	}
}

func TestValidManagedPathRejectsNonSlashSeparators(t *testing.T) {
	for _, unsafe := range []string{
		`.agents/skills/x\..\..\..\README.md`,
		`.claude/skills/x\..\outside`,
	} {
		if validManagedPath(unsafe) {
			t.Fatalf("validManagedPath(%q) = true, want false", unsafe)
		}
	}
}

func TestMaterializeEnsuresGitExcludeViaGitPath(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "loom-test@example.invalid")
	runGit(t, repo, "config", "user.name", "Loom Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-qm", "fixture")
	linked := filepath.Join(t.TempDir(), "linked")
	runGit(t, repo, "worktree", "add", "-q", "-b", "skillmat-test", linked)
	t.Cleanup(func() {
		cmd := exec.Command("git", "-C", repo, "worktree", "remove", "--force", linked) //nolint:gosec //nolint:norawexec // test fixture cleanup
		_ = cmd.Run()
	})

	st := materializeStore{skills: staticSkillStore{}}
	for i := 0; i < 2; i++ {
		if err := materialize(t.Context(), st, "WS", "lead", linked); err != nil {
			t.Fatalf("Materialize pass %d: %v", i+1, err)
		}
	}
	excludePath := strings.TrimSpace(runGit(t, linked, "rev-parse", "--git-path", "info/exclude"))
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(linked, excludePath)
	}
	b, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("read resolved exclude %q: %v", excludePath, err)
	}
	for _, line := range []string{AgentsSkillsDir + "/", ClaudeSkillsDir + "/"} {
		if count := strings.Count(string(b), line+"\n"); count != 1 {
			t.Fatalf("exclude entry %q count = %d, want 1 in:\n%s", line, count, b)
		}
	}
	if status := strings.TrimSpace(runGit(t, linked, "status", "--porcelain")); status != "" {
		t.Fatalf("materialized paths are visible to git status: %s", status)
	}
}

func TestMaterializeGitExcludeIgnoresPoisonedGitEnvironment(t *testing.T) {
	target := t.TempDir()
	attacker := t.TempDir()
	runGit(t, target, "init", "-q")
	runGit(t, attacker, "init", "-q")
	targetExclude := filepath.Join(target, ".git", "info", "exclude")
	attackerExclude := filepath.Join(attacker, ".git", "info", "exclude")
	attackerBefore, err := os.ReadFile(attackerExclude)
	if err != nil {
		t.Fatalf("read attacker exclude before materialization: %v", err)
	}
	t.Setenv("GIT_DIR", filepath.Join(attacker, ".git"))
	t.Setenv("GIT_WORK_TREE", attacker)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(attacker, ".git", "index"))

	if err := materialize(t.Context(), materializeStore{skills: staticSkillStore{}}, "WS", "lead", target); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	targetBody, err := os.ReadFile(targetExclude)
	if err != nil {
		t.Fatalf("read target exclude: %v", err)
	}
	for _, line := range []string{AgentsSkillsDir + "/", ClaudeSkillsDir + "/"} {
		if !strings.Contains(string(targetBody), line+"\n") {
			t.Fatalf("target exclude missing %q:\n%s", line, targetBody)
		}
	}
	attackerAfter, err := os.ReadFile(attackerExclude)
	if err != nil {
		t.Fatalf("read attacker exclude after materialization: %v", err)
	}
	if !bytes.Equal(attackerAfter, attackerBefore) {
		t.Fatalf("poisoned Git environment redirected exclude write:\nbefore=%q\nafter=%q", attackerBefore, attackerAfter)
	}
}

func TestMaterializeGitExcludeRefusesSymlinkedInfoParent(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	gitInfo := filepath.Join(repo, ".git", "info")
	if err := os.Rename(gitInfo, filepath.Join(repo, ".git", "info.real")); err != nil {
		t.Fatalf("move real git info directory: %v", err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, gitInfo); err != nil {
		t.Fatalf("plant git info symlink: %v", err)
	}

	err := materialize(t.Context(), materializeStore{skills: staticSkillStore{}}, "WS", "lead", repo)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Materialize error = %v, want symlink refusal", err)
	}
	if _, statErr := os.Lstat(filepath.Join(outside, "exclude")); !os.IsNotExist(statErr) {
		t.Fatalf("symlinked git info parent was traversed: %v", statErr)
	}
}

func TestMaterializeReconcilesManagedFileDirectoryTransitions(t *testing.T) {
	tests := []struct {
		name   string
		before []domain.SkillFile
		after  []domain.SkillFile
		want   string
	}{
		{
			name:   "file becomes directory",
			before: []domain.SkillFile{{Path: "node", Content: "old"}},
			after:  []domain.SkillFile{{Path: "node/child", Content: "new"}},
			want:   "node/child",
		},
		{
			name:   "directory becomes file",
			before: []domain.SkillFile{{Path: "node/child", Content: "old"}},
			after:  []domain.SkillFile{{Path: "node", Content: "new"}},
			want:   "node",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := t.TempDir()
			skill := &domain.Skill{Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body", Files: tt.before}
			skills := &staticSkillStore{skills: []*domain.Skill{skill}}
			st := materializeStore{skills: skills}
			mustMaterialize(t, st, target, "initial Materialize")
			skill.Files = tt.after
			mustMaterialize(t, st, target, "transition Materialize")
			got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", filepath.FromSlash(tt.want)))
			if err != nil || string(got) != "new" {
				t.Fatalf("transition path %q = %q, err=%v", tt.want, got, err)
			}
		})
	}
}

func TestMaterializeRefusesDirectoryToFileTransitionWithUnrecordedChild(t *testing.T) {
	target := t.TempDir()
	skill := &domain.Skill{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body",
		Files: []domain.SkillFile{{Path: "node/managed", Content: "old"}},
	}
	skills := &staticSkillStore{skills: []*domain.Skill{skill}}
	st := materializeStore{skills: skills}
	mustMaterialize(t, st, target, "initial Materialize")
	unrecorded := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "node", "user")
	if err := os.WriteFile(unrecorded, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write unrecorded child: %v", err)
	}
	skill.Files = []domain.SkillFile{{Path: "node", Content: "new"}}

	err := materialize(t.Context(), st, "WS", "lead", target)
	if err == nil || !strings.Contains(err.Error(), "node") || !strings.Contains(err.Error(), "user") {
		t.Fatalf("Materialize error = %v, want node/user collision", err)
	}
	if got, readErr := os.ReadFile(unrecorded); readErr != nil || string(got) != "keep" {
		t.Fatalf("unrecorded child changed: content=%q err=%v", got, readErr)
	}
}

func TestMaterializeRefusesManagedFileToNonemptyDirectoryBeforeCleanup(t *testing.T) {
	target := t.TempDir()
	skill := &domain.Skill{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body",
		Files: []domain.SkillFile{{Path: "node", Content: "old"}, {Path: "zzz", Content: "must remain"}},
	}
	skills := &staticSkillStore{skills: []*domain.Skill{skill}}
	st := materializeStore{skills: skills}
	mustMaterialize(t, st, target, "initial Materialize")
	node := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "node")
	if err := os.Remove(node); err != nil {
		t.Fatalf("remove managed node file: %v", err)
	}
	if err := os.Mkdir(node, 0o755); err != nil {
		t.Fatalf("replace managed node file with directory: %v", err)
	}
	unrecorded := filepath.Join(node, "user")
	if err := os.WriteFile(unrecorded, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write unrecorded child: %v", err)
	}
	skill.Files = []domain.SkillFile{{Path: "node/child", Content: "new"}, {Path: "zzz", Content: "updated"}}

	err := materialize(t.Context(), st, "WS", "lead", target)
	if err == nil || !strings.Contains(err.Error(), "node") {
		t.Fatalf("Materialize error = %v, want managed file-to-directory drift", err)
	}
	if got, readErr := os.ReadFile(unrecorded); readErr != nil || string(got) != "keep" {
		t.Fatalf("unrecorded child changed: content=%q err=%v", got, readErr)
	}
	zzz := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "zzz")
	if got, readErr := os.ReadFile(zzz); readErr != nil || string(got) != "must remain" {
		t.Fatalf("lexically later managed file changed before collision refusal: content=%q err=%v", got, readErr)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:gosec //nolint:norawexec // test fixture setup
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// mustMaterialize runs a materialization the test requires to succeed. label
// names the step, so a test with several materializations still says which one
// failed.
func mustMaterialize(t *testing.T, st store.Store, target, label string) {
	t.Helper()
	if err := materialize(t.Context(), st, "WS", "lead", target); err != nil {
		t.Fatalf("%s: %v", label, err)
	}
}
