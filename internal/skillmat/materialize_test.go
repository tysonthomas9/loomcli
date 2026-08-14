package skillmat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
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

	if err := Materialize(t.Context(), st, "WS", "lead", target); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

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

	markerBytes, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(MarkerPath)))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	var gotMarker marker
	if err := json.Unmarshal(markerBytes, &gotMarker); err != nil {
		t.Fatalf("decode marker: %v", err)
	}
	wantPaths := []string{
		".agents/skills/alpha/SKILL.md",
		".agents/skills/alpha/scripts/run.sh",
		".claude/skills/alpha",
	}
	if !reflect.DeepEqual(gotMarker.Paths, wantPaths) {
		t.Fatalf("marker paths = %#v, want %#v", gotMarker.Paths, wantPaths)
	}
	if gotMarker.Hash == "" {
		t.Fatal("marker hash is empty")
	}
}

func TestMaterializeMatchingHashIsNoOp(t *testing.T) {
	target := t.TempDir()
	skills := &staticSkillStore{skills: []*domain.Skill{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "database body\n",
	}}}
	st := materializeStore{skills: skills}
	if err := Materialize(t.Context(), st, "WS", "lead", target); err != nil {
		t.Fatalf("first Materialize: %v", err)
	}
	skillMD := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")
	if err := os.WriteFile(skillMD, []byte("agent-authored working copy\n"), 0o644); err != nil {
		t.Fatalf("edit materialized copy: %v", err)
	}

	if err := Materialize(t.Context(), st, "WS", "lead", target); err != nil {
		t.Fatalf("second Materialize: %v", err)
	}
	got, err := os.ReadFile(skillMD)
	if err != nil {
		t.Fatalf("read working copy: %v", err)
	}
	if string(got) != "agent-authored working copy\n" {
		t.Fatalf("matching hash rewrote SKILL.md: %q", got)
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
			if err := Materialize(t.Context(), st, "WS", "lead", target); err != nil {
				t.Fatalf("first Materialize: %v", err)
			}
			tt.mutate(t, target)

			if err := Materialize(t.Context(), st, "WS", "lead", target); err != nil {
				t.Fatalf("second Materialize: %v", err)
			}
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

func TestMaterializeRecoversExactPartialProjectionWithoutMarker(t *testing.T) {
	target := t.TempDir()
	skill := &domain.Skill{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
		Files: []domain.SkillFile{{Path: "run.sh", Content: "#!/bin/sh\n", Executable: true}},
	}
	entries, err := desiredEntries(domain.ResolveSkillChainDetail([]*domain.Skill{skill}, "lead"))
	if err != nil {
		t.Fatalf("desiredEntries: %v", err)
	}
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
	if err := Materialize(t.Context(), st, "WS", "lead", target); err != nil {
		t.Fatalf("Materialize after partial write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(MarkerPath))); err != nil {
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
	if len(root.created) != 1 || root.created[0] == MarkerPath || !strings.HasPrefix(root.created[0], MarkerPath+".tmp-") {
		t.Fatalf("created paths = %#v, want one marker temporary", root.created)
	}
	if len(root.renamed) != 1 || root.renamed[0] != [2]string{root.created[0], MarkerPath} {
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
	if err := Materialize(t.Context(), st, "WS", "lead", target); err != nil {
		t.Fatalf("first Materialize: %v", err)
	}
	unrecorded := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "notes.user")
	if err := os.WriteFile(unrecorded, []byte("keep me\n"), 0o644); err != nil {
		t.Fatalf("write unrecorded file: %v", err)
	}

	skills.skills = nil
	if err := Materialize(t.Context(), st, "WS", "lead", target); err != nil {
		t.Fatalf("remove Materialize: %v", err)
	}
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
	markerBytes, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(MarkerPath)))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	var gotMarker marker
	if err := json.Unmarshal(markerBytes, &gotMarker); err != nil {
		t.Fatalf("decode marker: %v", err)
	}
	if len(gotMarker.Paths) != 0 {
		t.Fatalf("marker paths after removal = %#v, want empty", gotMarker.Paths)
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
	if err := Materialize(t.Context(), st, "WS", "lead", target); err != nil {
		t.Fatalf("shadowed Materialize: %v", err)
	}

	skills.skills = []*domain.Skill{workspaceSkill}
	if err := Materialize(t.Context(), st, "WS", "lead", target); err != nil {
		t.Fatalf("unshadowed Materialize: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md"))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	if !strings.Contains(string(got), "description: workspace\n") || !strings.HasSuffix(string(got), "workspace body\n") {
		t.Fatalf("unshadowed SKILL.md = %q", got)
	}
}

func TestMaterializeRejectsInSkillPathCollisions(t *testing.T) {
	tests := []struct {
		name  string
		files []domain.SkillFile
		wantA string
		wantB string
	}{
		{
			name:  "reserved body name under different case",
			files: []domain.SkillFile{{Path: "skill.md", Content: "collision"}},
			wantA: "SKILL.md",
			wantB: "skill.md",
		},
		{
			name: "unicode normalization",
			files: []domain.SkillFile{
				{Path: "references/caf\u00e9.md", Content: "NFC"},
				{Path: "references/cafe\u0301.md", Content: "NFD"},
			},
			wantA: "references/caf\u00e9.md",
			wantB: "references/cafe\u0301.md",
		},
		{
			name: "file versus directory",
			files: []domain.SkillFile{
				{Path: "a/b", Content: "file"},
				{Path: "a/b/c", Content: "child"},
			},
			wantA: "a/b",
			wantB: "a/b/c",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := t.TempDir()
			st := materializeStore{skills: staticSkillStore{skills: []*domain.Skill{{
				Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body", Files: tt.files,
			}}}}
			err := Materialize(t.Context(), st, "WS", "lead", target)
			if err == nil {
				t.Fatal("Materialize error = nil, want collision")
			}
			if !strings.Contains(err.Error(), tt.wantA) || !strings.Contains(err.Error(), tt.wantB) {
				t.Fatalf("Materialize error = %q, want both %q and %q", err, tt.wantA, tt.wantB)
			}
			if _, statErr := os.Lstat(filepath.Join(target, filepath.FromSlash(MarkerPath))); !os.IsNotExist(statErr) {
				t.Fatalf("marker exists after collision: %v", statErr)
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

	err := Materialize(t.Context(), st, "WS", "lead", target)
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

	err := Materialize(t.Context(), st, "WS", "lead", target)
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
	if err := Materialize(t.Context(), st, "WS", "lead", target); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
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
	if err := Materialize(t.Context(), available, "WS", "lead", target); err != nil {
		t.Fatalf("initial Materialize: %v", err)
	}
	skillMD := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")
	before, err := os.ReadFile(skillMD)
	if err != nil {
		t.Fatalf("read initial projection: %v", err)
	}
	outage := &url.Error{Op: "GET", URL: "http://fleet-db/skills", Err: syscall.ECONNREFUSED}
	unavailable := materializeStore{skills: staticSkillStore{err: outage}}
	err = Materialize(t.Context(), unavailable, "WS", "lead", target)
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
	err := Materialize(t.Context(), st, "WS", "lead", target)
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
	err := Materialize(t.Context(), st, "WS", "lead", target)
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
			markerPath := filepath.Join(target, filepath.FromSlash(MarkerPath))
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

			err := Materialize(t.Context(), materializeStore{skills: staticSkillStore{}}, "WS", "lead", target)
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
		cmd := exec.Command("git", "-C", repo, "worktree", "remove", "--force", linked) //nolint:gosec,norawexec // test fixture cleanup
		_ = cmd.Run()
	})

	st := materializeStore{skills: staticSkillStore{}}
	for i := 0; i < 2; i++ {
		if err := Materialize(t.Context(), st, "WS", "lead", linked); err != nil {
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

	if err := Materialize(t.Context(), materializeStore{skills: staticSkillStore{}}, "WS", "lead", target); err != nil {
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

	err := Materialize(t.Context(), materializeStore{skills: staticSkillStore{}}, "WS", "lead", repo)
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
			if err := Materialize(t.Context(), st, "WS", "lead", target); err != nil {
				t.Fatalf("initial Materialize: %v", err)
			}
			skill.Files = tt.after
			if err := Materialize(t.Context(), st, "WS", "lead", target); err != nil {
				t.Fatalf("transition Materialize: %v", err)
			}
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
	if err := Materialize(t.Context(), st, "WS", "lead", target); err != nil {
		t.Fatalf("initial Materialize: %v", err)
	}
	unrecorded := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "node", "user")
	if err := os.WriteFile(unrecorded, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write unrecorded child: %v", err)
	}
	skill.Files = []domain.SkillFile{{Path: "node", Content: "new"}}

	err := Materialize(t.Context(), st, "WS", "lead", target)
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
	if err := Materialize(t.Context(), st, "WS", "lead", target); err != nil {
		t.Fatalf("initial Materialize: %v", err)
	}
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

	err := Materialize(t.Context(), st, "WS", "lead", target)
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
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:gosec,norawexec // test fixture setup
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
