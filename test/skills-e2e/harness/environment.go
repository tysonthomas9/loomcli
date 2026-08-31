package harness

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

type Environment struct {
	loomBin    string
	fixtureDir string
	sources    map[string]string
}

type Skill struct {
	WorkspaceKey     string      `json:"workspace_key"`
	Name             string      `json:"name"`
	Scope            string      `json:"scope"`
	Description      string      `json:"description"`
	FileTreeRevision string      `json:"file_tree_revision"`
	CreatedBy        string      `json:"created_by"`
	UpdatedBy        string      `json:"updated_by"`
	Source           string      `json:"source"`
	Content          string      `json:"content"`
	Files            []SkillFile `json:"files"`
}

type SkillFile struct {
	Path       string `json:"path"`
	MediaType  string `json:"media_type"`
	Executable bool   `json:"executable"`
	SizeBytes  int64  `json:"size_bytes"`
}

type expectedSkill struct {
	SourceFixture string `json:"source_fixture"`
	Skill         Skill  `json:"skill"`
}

type Materialization struct {
	env  *Environment
	root string
}

func Open(t *testing.T) *Environment {
	t.Helper()
	loomBin := requireEnv(t, "SKILLS_E2E_LOOM_BIN")
	loomBin, err := filepath.Abs(loomBin)
	if err != nil {
		t.Fatalf("resolve SKILLS_E2E_LOOM_BIN: %v", err)
	}
	if info, err := os.Stat(loomBin); err != nil || info.IsDir() {
		t.Fatalf("SKILLS_E2E_LOOM_BIN is not an executable file: %s", loomBin)
	}

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate skills E2E harness source")
	}
	fixtureDir := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "testdata"))

	return &Environment{
		loomBin:    loomBin,
		fixtureDir: fixtureDir,
		sources:    make(map[string]string),
	}
}

func (e *Environment) ImportSkill(t *testing.T, fixture string) Skill {
	t.Helper()
	source := e.stageFixture(t, fixture)
	e.run(t, "", "skill", "import", source)

	name := strings.Split(filepath.ToSlash(fixture), "/")[0]
	skill := e.ShowSkill(t, name)
	e.sources[fixture] = source
	return skill
}

func (e *Environment) ShowSkill(t *testing.T, name string) Skill {
	t.Helper()
	output := e.run(t, "", "skill", "show", name, "--json")
	var skill Skill
	if err := json.Unmarshal(output, &skill); err != nil {
		t.Fatalf("decode public Skill %q: %v\n%s", name, err, output)
	}
	return skill
}

func (e *Environment) RequireSkill(t *testing.T, actual Skill, manifestPath string) {
	t.Helper()
	manifestBytes, err := os.ReadFile(filepath.Join(e.fixtureDir, filepath.FromSlash(manifestPath)))
	if err != nil {
		t.Fatalf("read expected Skill manifest: %v", err)
	}
	var expected expectedSkill
	if err := json.Unmarshal(manifestBytes, &expected); err != nil {
		t.Fatalf("decode expected Skill manifest: %v", err)
	}
	source, ok := e.sources[expected.SourceFixture]
	if !ok {
		t.Fatalf("expected source fixture %q was not imported", expected.SourceFixture)
	}
	expected.Skill.Source = "import:" + source

	if !reflect.DeepEqual(actual, expected.Skill) {
		t.Fatalf("public Skill differs from literal manifest\n%s", jsonDifference(expected.Skill, actual))
	}
}

func (e *Environment) MaterializeSkills(t *testing.T) Materialization {
	t.Helper()
	root := t.TempDir()
	e.run(t, root, "skill", "materialize")
	return Materialization{env: e, root: root}
}

func (m Materialization) RequireExactTree(t *testing.T, fixture, skillName string) {
	t.Helper()
	expectedRoot, ok := m.env.sources[fixture]
	if !ok {
		t.Fatalf("fixture %q was not imported", fixture)
	}
	actualRoot := filepath.Join(m.root, ".agents", "skills", skillName)

	expectedFiles := collectFiles(t, expectedRoot)
	actualFiles := collectFiles(t, actualRoot)
	if !reflect.DeepEqual(mapKeys(expectedFiles), mapKeys(actualFiles)) {
		t.Fatalf("materialized paths differ\nexpected: %v\nactual:   %v", mapKeys(expectedFiles), mapKeys(actualFiles))
	}

	for path, expected := range expectedFiles {
		actual := actualFiles[path]
		if !bytes.Equal(actual.data, expected.data) {
			t.Fatalf("materialized bytes differ for %s\nexpected sha256: %x\nactual sha256:   %x",
				path, sha256.Sum256(expected.data), sha256.Sum256(actual.data))
		}
		if actual.mode != expected.mode {
			t.Fatalf("materialized mode differs for %s: expected %04o, actual %04o", path, expected.mode, actual.mode)
		}
	}
}

func (e *Environment) run(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	command := exec.Command(e.loomBin, args...)
	command.Env = os.Environ()
	command.Dir = dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err != nil {
		t.Fatalf("loom %s failed: %v\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), err, stdout.Bytes(), stderr.Bytes())
	}
	return stdout.Bytes()
}

func (e *Environment) stageFixture(t *testing.T, fixture string) string {
	t.Helper()
	sourceRoot := filepath.Join(e.fixtureDir, filepath.FromSlash(fixture))
	targetRoot := filepath.Join(t.TempDir(), "skill")
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		t.Fatalf("create staged fixture: %v", err)
	}

	err := filepath.WalkDir(sourceRoot, func(sourcePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceRoot, sourcePath)
		if err != nil || relative == "." {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(targetRoot, relative), 0o755)
		}

		data, err := os.ReadFile(sourcePath)
		if err != nil {
			return err
		}
		mode := fs.FileMode(0o644)
		targetRelative := relative
		switch {
		case strings.HasSuffix(relative, ".executable"):
			targetRelative = strings.TrimSuffix(relative, ".executable")
			mode = 0o755
		case strings.HasSuffix(relative, ".empty"):
			targetRelative = strings.TrimSuffix(relative, ".empty")
			data = nil
		case strings.HasSuffix(relative, ".hex"):
			targetRelative = strings.TrimSuffix(relative, ".hex")
			data, err = hex.DecodeString(strings.TrimSpace(string(data)))
			if err != nil {
				return fmt.Errorf("decode %s: %w", relative, err)
			}
		}

		targetPath := filepath.Join(targetRoot, targetRelative)
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(targetPath, data, mode); err != nil {
			return err
		}
		return os.Chmod(targetPath, mode)
	})
	if err != nil {
		t.Fatalf("stage fixture %q: %v", fixture, err)
	}

	canonical, err := filepath.EvalSymlinks(targetRoot)
	if err != nil {
		t.Fatalf("canonicalize staged fixture: %v", err)
	}
	return canonical
}

type fileState struct {
	data []byte
	mode fs.FileMode
}

func collectFiles(t *testing.T, root string) map[string]fileState {
	t.Helper()
	files := make(map[string]fileState)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = fileState{data: data, mode: info.Mode().Perm()}
		return nil
	})
	if err != nil {
		t.Fatalf("read tree %s: %v", root, err)
	}
	return files
}

func mapKeys(values map[string]fileState) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func jsonDifference(expected, actual Skill) string {
	expectedJSON, _ := json.MarshalIndent(expected, "", "  ")
	actualJSON, _ := json.MarshalIndent(actual, "", "  ")
	return fmt.Sprintf("expected:\n%s\nactual:\n%s", expectedJSON, actualJSON)
}

func requireEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for the real-service Skill E2E suite", name)
	}
	return value
}
