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
	"time"
)

type Environment struct {
	t          *testing.T
	loomBin    string
	fixtureDir string
	sources    map[string]string
	nextEnv    map[string]string
	lastStderr string
}

// DelayNextTreeProjection makes the next public Loom command run an E2E-tagged
// Fleet binary that fails inline tree projection once and delays real
// background projection. Production Fleet builds do not contain these controls.
func (e *Environment) DelayNextTreeProjection(delay time.Duration) {
	e.t.Helper()
	if delay <= 0 {
		e.t.Fatalf("tree projection delay must be positive: %v", delay)
	}
	e.nextEnv = map[string]string{
		"FLEET_E2E_WORKSPACE_FILE_INLINE_FAILURES":  "1",
		"FLEET_E2E_WORKSPACE_FILE_BACKGROUND_DELAY": delay.String(),
	}
}

func (e *Environment) RequireLastCommandActivated(name string) {
	e.t.Helper()
	if !strings.Contains(e.lastStderr, name) {
		e.t.Fatalf("last Loom command did not activate %q\nstderr:\n%s", name, e.lastStderr)
	}
}

// SkillFixture is a checked-in source tree staged exactly as a user would
// present it to `loom skill import`.
type SkillFixture struct {
	root string
}

// FileFixture is a checked-in file passed to a public Loom command.
type FileFixture struct {
	path string
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

// Command is one already-started public Loom command.
type Command struct {
	t      *testing.T
	args   []string
	cmd    *exec.Cmd
	stdout bytes.Buffer
	stderr bytes.Buffer
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
		t:          t,
		loomBin:    loomBin,
		fixtureDir: fixtureDir,
		sources:    make(map[string]string),
	}
}

// SkillFixture stages a readable fixture recipe as a real Skill directory.
// Staging is test setup; no product operation occurs until SkillImport runs.
func (e *Environment) SkillFixture(fixture string) SkillFixture {
	e.t.Helper()
	source := e.stageFixture(fixture)
	e.sources[fixture] = source
	return SkillFixture{root: source}
}

// FileFixture returns the path to a checked-in file for a public Loom command.
func (e *Environment) FileFixture(fixture string) FileFixture {
	e.t.Helper()
	path := filepath.Join(e.fixtureDir, filepath.FromSlash(fixture))
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		e.t.Fatalf("file fixture is not a regular file: %s", path)
	}
	return FileFixture{path: path}
}

// SkillImport invokes `loom skill import` and nothing else.
func (e *Environment) SkillImport(fixture SkillFixture) {
	e.t.Helper()
	e.run("", "skill", "import", fixture.root)
}

// SkillImportFails invokes one public import and requires the command to fail.
func (e *Environment) SkillImportFails(fixture SkillFixture) {
	e.t.Helper()
	args := []string{"skill", "import", fixture.root}
	// The executable and arguments are controlled by this black-box harness.
	//nolint:gosec // Running the real Loom CLI is the purpose of this method.
	command := exec.Command(e.loomBin, args...)
	command.Env = append(os.Environ(), e.consumeNextCommandEnv()...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	e.lastStderr = stderr.String()
	if err == nil {
		e.t.Fatalf("loom %s unexpectedly succeeded\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), stdout.Bytes(), stderr.Bytes())
	}
}

// StartSkillImport starts `loom skill import` immediately and returns a handle
// whose Wait method observes that one public command. Starting two handles
// before waiting creates a deterministic client-side concurrency window.
func (e *Environment) StartSkillImport(fixture SkillFixture) *Command {
	e.t.Helper()
	args := []string{"skill", "import", fixture.root}
	// The executable and arguments are controlled by this black-box harness.
	//nolint:gosec // Starting the real Loom CLI is the purpose of this method.
	cmd := exec.Command(e.loomBin, args...)
	command := &Command{t: e.t, args: args, cmd: cmd}
	cmd.Env = os.Environ()
	cmd.Stdout = &command.stdout
	cmd.Stderr = &command.stderr
	if err := cmd.Start(); err != nil {
		e.t.Fatalf("start loom %s: %v", strings.Join(args, " "), err)
	}
	return command
}

// Wait requires the public Loom command to finish successfully.
func (c *Command) Wait() {
	c.t.Helper()
	if err := c.cmd.Wait(); err != nil {
		c.t.Fatalf("loom %s failed: %v\nstdout:\n%s\nstderr:\n%s",
			strings.Join(c.args, " "), err, c.stdout.Bytes(), c.stderr.Bytes())
	}
}

// SkillShow invokes `loom skill show --json` and decodes its public response.
func (e *Environment) SkillShow(name string) Skill {
	e.t.Helper()
	output := e.run("", "skill", "show", name, "--json")
	var skill Skill
	if err := json.Unmarshal(output, &skill); err != nil {
		e.t.Fatalf("decode public Skill %q: %v\n%s", name, err, output)
	}
	return skill
}

// SkillList invokes `loom skill list --json` and decodes its public response.
func (e *Environment) SkillList() []Skill {
	e.t.Helper()
	output := e.run("", "skill", "list", "--json")
	var skills []Skill
	if err := json.Unmarshal(output, &skills); err != nil {
		e.t.Fatalf("decode public Skill list: %v\n%s", err, output)
	}
	return skills
}

// SkillUpdateContent invokes `loom skill update --content` and nothing else.
func (e *Environment) SkillUpdateContent(name string, content FileFixture) {
	e.t.Helper()
	e.run("", "skill", "update", name, "--content", content.path)
}

// SkillDelete invokes `loom skill delete` and nothing else.
func (e *Environment) SkillDelete(name string) {
	e.t.Helper()
	e.run("", "skill", "delete", name)
}

func (e *Environment) RequireSkill(actual Skill, manifestPath string) {
	e.t.Helper()
	manifestBytes, err := os.ReadFile(filepath.Join(e.fixtureDir, filepath.FromSlash(manifestPath)))
	if err != nil {
		e.t.Fatalf("read expected Skill manifest: %v", err)
	}
	var expected expectedSkill
	if err := json.Unmarshal(manifestBytes, &expected); err != nil {
		e.t.Fatalf("decode expected Skill manifest: %v", err)
	}
	source, ok := e.sources[expected.SourceFixture]
	if !ok {
		e.t.Fatalf("expected source fixture %q was not staged", expected.SourceFixture)
	}
	expected.Skill.Source = "import:" + source

	if !reflect.DeepEqual(actual, expected.Skill) {
		e.t.Fatalf("public Skill differs from literal manifest\n%s", jsonDifference(expected.Skill, actual))
	}
}

// SkillMaterialize invokes `loom skill materialize` in a fresh worktree.
func (e *Environment) SkillMaterialize() Materialization {
	e.t.Helper()
	root := e.t.TempDir()
	e.run(root, "skill", "materialize")
	return Materialization{env: e, root: root}
}

// SkillMaterializeFails invokes one public materialization into a fresh
// worktree and requires the command to reject the operation.
func (e *Environment) SkillMaterializeFails() Materialization {
	e.t.Helper()
	root := e.t.TempDir()
	args := []string{"skill", "materialize"}
	// The executable and arguments are controlled by this black-box harness.
	//nolint:gosec // Running the real Loom CLI is the purpose of this method.
	command := exec.Command(e.loomBin, args...)
	command.Env = append(os.Environ(), e.consumeNextCommandEnv()...)
	command.Dir = root
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	e.lastStderr = stderr.String()
	if err == nil {
		e.t.Fatalf("loom %s unexpectedly succeeded\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), stdout.Bytes(), stderr.Bytes())
	}
	return Materialization{env: e, root: root}
}

// SkillMaterializeInto invokes `loom skill materialize` again in an existing
// worktree so scenarios can observe reconciliation and pruning.
func (e *Environment) SkillMaterializeInto(target Materialization) {
	e.t.Helper()
	e.run(target.root, "skill", "materialize")
}

func (m Materialization) RequireExactTree(fixture SkillFixture, skillName string) {
	m.env.t.Helper()
	expectedRoot := fixture.root
	actualRoot := filepath.Join(m.root, ".agents", "skills", skillName)

	expectedFiles := collectFiles(m.env.t, expectedRoot)
	actualFiles := collectFiles(m.env.t, actualRoot)
	if !reflect.DeepEqual(mapKeys(expectedFiles), mapKeys(actualFiles)) {
		m.env.t.Fatalf("materialized paths differ\nexpected: %v\nactual:   %v", mapKeys(expectedFiles), mapKeys(actualFiles))
	}

	for path, expected := range expectedFiles {
		actual := actualFiles[path]
		if !bytes.Equal(actual.data, expected.data) {
			m.env.t.Fatalf("materialized bytes differ for %s\nexpected sha256: %x\nactual sha256:   %x",
				path, sha256.Sum256(expected.data), sha256.Sum256(actual.data))
		}
		if actual.mode != expected.mode {
			m.env.t.Fatalf("materialized mode differs for %s: expected %04o, actual %04o", path, expected.mode, actual.mode)
		}
	}
}

func (m Materialization) RequireSkillAbsent(skillName string) {
	m.env.t.Helper()
	path := filepath.Join(m.root, ".agents", "skills", skillName)
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		m.env.t.Fatalf("materialized skill %q still exists after deletion: %v", skillName, err)
	}
}

func (e *Environment) RequireListedSkill(expected Skill, listed []Skill) {
	e.t.Helper()
	for _, actual := range listed {
		if actual.Name == expected.Name && actual.Scope == expected.Scope {
			if actual.FileTreeRevision != expected.FileTreeRevision {
				e.t.Fatalf("listed Skill %q revision = %q, want selected revision %q",
					expected.Name, actual.FileTreeRevision, expected.FileTreeRevision)
			}
			return
		}
	}
	e.t.Fatalf("selected Skill %q was absent from public Skill list", expected.Name)
}

func (e *Environment) run(dir string, args ...string) []byte {
	e.t.Helper()
	// The executable is resolved from the test-controlled SKILLS_E2E_LOOM_BIN;
	// scenarios pass explicit public Loom CLI arguments rather than shell text.
	//nolint:gosec // This is the purpose of the black-box E2E harness.
	command := exec.Command(e.loomBin, args...)
	command.Env = append(os.Environ(), e.consumeNextCommandEnv()...)
	command.Dir = dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	e.lastStderr = stderr.String()
	if err != nil {
		e.t.Fatalf("loom %s failed: %v\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), err, stdout.Bytes(), stderr.Bytes())
	}
	return stdout.Bytes()
}

func (e *Environment) consumeNextCommandEnv() []string {
	keys := make([]string, 0, len(e.nextEnv))
	for key := range e.nextEnv {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+e.nextEnv[key])
	}
	e.nextEnv = nil
	return values
}

func (e *Environment) stageFixture(fixture string) string {
	e.t.Helper()
	sourceRoot := filepath.Join(e.fixtureDir, filepath.FromSlash(fixture))
	targetRoot := filepath.Join(e.t.TempDir(), "skill")
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		e.t.Fatalf("create staged fixture: %v", err)
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

		return stageFixtureFile(sourcePath, targetRoot, relative)
	})
	if err != nil {
		e.t.Fatalf("stage fixture %q: %v", fixture, err)
	}

	canonical, err := filepath.EvalSymlinks(targetRoot)
	if err != nil {
		e.t.Fatalf("canonicalize staged fixture: %v", err)
	}
	return canonical
}

func stageFixtureFile(sourcePath, targetRoot, relative string) error {
	// sourcePath is supplied by WalkDir beneath the checked-in fixture root.
	//nolint:gosec // Reading that test-controlled path is intentional.
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
		// path is supplied by WalkDir beneath the test-owned materialization.
		//nolint:gosec // Reading that test-controlled path is intentional.
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
