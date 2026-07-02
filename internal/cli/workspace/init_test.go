package workspace

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input    string
		expected []string
	}{
		{"falcon,nova", []string{"falcon", "nova"}},
		{"alpha, beta, gamma", []string{"alpha", "beta", "gamma"}},
		{"single", []string{"single"}},
		{"", nil},
		{", ,", nil},
		{"  a  ,  b  ", []string{"a", "b"}},
	}

	for _, tc := range tests {
		got := parseNames(tc.input)
		if !slicesEqual(got, tc.expected) {
			t.Errorf("parseNames(%q) = %v, want %v", tc.input, got, tc.expected)
		}
	}
}

func TestFilterExisting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		names    []string
		existing []string
		expected []string
	}{
		{[]string{"a", "b", "c"}, []string{"b"}, []string{"a", "c"}},
		{[]string{"a", "b"}, []string{"a", "b"}, nil},
		{[]string{"a", "b"}, nil, []string{"a", "b"}},
		{[]string{"x", "y"}, []string{"a", "b"}, []string{"x", "y"}},
	}

	for _, tc := range tests {
		got := filterExisting(tc.names, tc.existing)
		if !slicesEqual(got, tc.expected) {
			t.Errorf("filterExisting(%v, %v) = %v, want %v", tc.names, tc.existing, got, tc.expected)
		}
	}
}

func TestGetFirstName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		names    []string
		expected string
	}{
		{[]string{"a", "b"}, "a"},
		{[]string{"falcon"}, "falcon"},
		{nil, "falcon"},
		{[]string{}, "falcon"},
	}

	for _, tc := range tests {
		got := getFirstName(tc.names)
		if got != tc.expected {
			t.Errorf("getFirstName(%v) = %q, want %q", tc.names, got, tc.expected)
		}
	}
}

func TestListExistingWorktrees(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	// Create some "worktrees" (directories with .git)
	for _, name := range []string{"falcon", "nova"} {
		wtPath := filepath.Join(tmpDir, name)
		os.MkdirAll(wtPath, 0755)
		os.WriteFile(filepath.Join(wtPath, ".git"), []byte{}, 0644)
	}

	// Create a non-worktree directory (no .git)
	os.MkdirAll(filepath.Join(tmpDir, "not-a-worktree"), 0755)

	// Create a file (should be ignored)
	os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte{}, 0644)

	got := listExistingWorktrees(tmpDir)

	// Should only find falcon and nova
	if len(got) != 2 {
		t.Errorf("listExistingWorktrees() found %d worktrees, want 2", len(got))
	}

	hasName := func(names []string, name string) bool {
		for _, n := range names {
			if n == name {
				return true
			}
		}
		return false
	}

	if !hasName(got, "falcon") {
		t.Error("listExistingWorktrees() missing 'falcon'")
	}
	if !hasName(got, "nova") {
		t.Error("listExistingWorktrees() missing 'nova'")
	}
}

func TestListExistingWorktrees_EmptyDir(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	got := listExistingWorktrees(tmpDir)
	if len(got) != 0 {
		t.Errorf("listExistingWorktrees() on empty dir = %v, want empty", got)
	}
}

func TestListExistingWorktrees_NonexistentDir(t *testing.T) {
	t.Parallel()
	got := listExistingWorktrees("/nonexistent/path/12345")
	if got != nil {
		t.Errorf("listExistingWorktrees() on nonexistent = %v, want nil", got)
	}
}

func TestCheckPrerequisites_NotGitRepo(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"rev-parse", "--is-inside-work-tree"}, Err: errors.New("not a git repo")},
	})
	mock.InstallOn(deps)

	result := checkPrerequisites(deps)
	if result {
		t.Error("checkPrerequisites() should return false when not in git repo")
	}
}

func TestCheckPrerequisites_DoesNotRequireExternalIssueCLI(t *testing.T) {
	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"rev-parse", "--is-inside-work-tree"}, Stdout: "true"},
		{Name: "git", Args: []string{"rev-parse", "--git-common-dir"}, Stdout: ".git"},
		{Name: "git", Args: []string{"rev-parse", "--git-dir"}, Stdout: ".git"},
	})
	mock.InstallOn(deps)

	result := checkPrerequisites(deps)
	if !result {
		t.Error("checkPrerequisites() should return true without an external issue CLI")
	}
}

func TestCheckPrerequisites_Success(t *testing.T) {
	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"rev-parse", "--is-inside-work-tree"}, Stdout: "true"},
		{Name: "git", Args: []string{"rev-parse", "--git-common-dir"}, Stdout: ".git"},
		{Name: "git", Args: []string{"rev-parse", "--git-dir"}, Stdout: ".git"},
	})
	mock.InstallOn(deps)

	result := checkPrerequisites(deps)
	if !result {
		t.Error("checkPrerequisites() should return true when all prerequisites met")
	}
}

func TestInitIssueStorage_AlreadyInitialized(t *testing.T) {
	// Save and restore working directory
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	os.Chdir(tmpDir)

	deps, _, _, _, _ := NewTestDeps(t)

	result := initIssueStorage(deps)
	if !result {
		t.Error("initIssueStorage() should return true in fleet-db mode")
	}
}

func TestInitIssueStorage_Initialize(t *testing.T) {
	ResetWorkspaceRuntimeDirCache()
	defer ResetWorkspaceRuntimeDirCache()

	// Save and restore working directory
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	os.Chdir(tmpDir)

	// Set non-interactive mode
	origYes := initYes
	initYes = true
	defer func() { initYes = origYes }()

	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, nil)
	mock.InstallOn(deps)

	result := initIssueStorage(deps)
	if !result {
		t.Error("initIssueStorage() should return true without local task database init")
	}
}

func TestCreateWorktreesDir_AlreadyExists(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	result := createWorktreesDir(tmpDir)
	if !result {
		t.Error("createWorktreesDir() should return true for existing directory")
	}
}

func TestCreateWorktreesDir_NotADirectory(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "testfile")
	os.WriteFile(filePath, []byte{}, 0644)

	result := createWorktreesDir(filePath)
	if result {
		t.Error("createWorktreesDir() should return false when path is a file")
	}
}

func TestCreateWorktreesDir_Create(t *testing.T) {
	tmpDir := t.TempDir()
	newDir := filepath.Join(tmpDir, "new-worktrees")

	// Set non-interactive mode
	origYes := initYes
	initYes = true
	defer func() { initYes = origYes }()

	result := createWorktreesDir(newDir)
	if !result {
		t.Error("createWorktreesDir() should return true after creating directory")
	}

	// Verify directory was created
	info, err := os.Stat(newDir)
	if err != nil {
		t.Errorf("Directory was not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("Created path is not a directory")
	}
}

func TestCreateSingleWorktree_Success(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(tmpDir, "falcon"), "-b", "falcon"}, Stdout: "Created worktree"},
	})
	mock.InstallOn(deps)

	result := createSingleWorktree(deps, tmpDir, "falcon")
	if !result {
		t.Error("createSingleWorktree() should return true on success")
	}
}

func TestCreateSingleWorktree_BranchExists(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	wtPath := filepath.Join(tmpDir, "falcon")

	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{
		// First attempt fails with branch exists
		{Name: "git", Args: []string{"worktree", "add", wtPath, "-b", "falcon"}, Stderr: "branch already exists", Err: errors.New("exit 1")},
		// Second attempt without -b
		{Name: "git", Args: []string{"worktree", "add", wtPath, "falcon"}, Stdout: "Created worktree"},
	})
	mock.InstallOn(deps)

	result := createSingleWorktree(deps, tmpDir, "falcon")
	if !result {
		t.Error("createSingleWorktree() should retry without -b when branch exists")
	}
}

func TestCreateSingleWorktree_AlreadyExists(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	wtPath := filepath.Join(tmpDir, "falcon")

	// Both git worktree add attempts fail with "already exists" / "already a worktree"
	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"worktree", "add", wtPath, "-b", "falcon"}, Stderr: "fatal: 'falcon' already exists", Err: errors.New("exit 1")},
		{Name: "git", Args: []string{"worktree", "add", wtPath, "falcon"}, Stderr: "fatal: '/path' is already a worktree", Err: errors.New("exit 1")},
	})
	mock.InstallOn(deps)

	result := createSingleWorktree(deps, tmpDir, "falcon")
	if result {
		t.Error("createSingleWorktree() should return false when worktree already exists")
	}
}

func TestCreateSingleWorktree_AlreadyCheckedOut(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	wtPath := filepath.Join(tmpDir, "falcon")

	// Both git worktree add attempts fail with "already checked out" (alternate git message)
	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"worktree", "add", wtPath, "-b", "falcon"}, Stderr: "fatal: 'falcon' already exists", Err: errors.New("exit 1")},
		{Name: "git", Args: []string{"worktree", "add", wtPath, "falcon"}, Stderr: "fatal: 'falcon' is already checked out at '/other/worktree'", Err: errors.New("exit 1")},
	})
	mock.InstallOn(deps)

	result := createSingleWorktree(deps, tmpDir, "falcon")
	if result {
		t.Error("createSingleWorktree() should return false when branch is already checked out")
	}
}

func TestGetWorktreesDirForInit(t *testing.T) {
	// Test with flag
	origDir := initWorktreesDir
	initWorktreesDir = "custom-dir"
	defer func() { initWorktreesDir = origDir }()

	got := getWorktreesDirForInit()
	if got != "custom-dir" {
		t.Errorf("getWorktreesDirForInit() = %q, want 'custom-dir'", got)
	}

	// Test without flag (uses GetWorktreesDir)
	initWorktreesDir = ""
	got = getWorktreesDirForInit()
	if got != GetWorktreesDir() {
		t.Errorf("getWorktreesDirForInit() = %q, want %q", got, GetWorktreesDir())
	}
}

func TestDefaultAgentNames(t *testing.T) {
	t.Parallel()
	// Verify defaults are set correctly
	if len(defaultAgentNames) != 2 {
		t.Errorf("defaultAgentNames has %d items, want 2", len(defaultAgentNames))
	}
	if defaultAgentNames[0] != "falcon" {
		t.Errorf("defaultAgentNames[0] = %q, want 'falcon'", defaultAgentNames[0])
	}
	if defaultAgentNames[1] != "nova" {
		t.Errorf("defaultAgentNames[1] = %q, want 'nova'", defaultAgentNames[1])
	}
}

func TestSuggestedAgentNames(t *testing.T) {
	t.Parallel()
	// Verify suggested names list is reasonable
	if len(suggestedAgentNames) < 5 {
		t.Errorf("suggestedAgentNames has %d items, want at least 5", len(suggestedAgentNames))
	}
	// Should start with defaults
	if suggestedAgentNames[0] != "falcon" {
		t.Errorf("suggestedAgentNames[0] = %q, want 'falcon'", suggestedAgentNames[0])
	}
	if suggestedAgentNames[1] != "nova" {
		t.Errorf("suggestedAgentNames[1] = %q, want 'nova'", suggestedAgentNames[1])
	}
}

// --- promptYesNo tests ---

func TestPromptYesNo_DefaultYes(t *testing.T) {
	MockStdin(t, "\n")
	result := promptYesNo("Continue?", true)
	if !result {
		t.Error("promptYesNo with empty input and defaultYes=true should return true")
	}
}

func TestPromptYesNo_DefaultNo(t *testing.T) {
	MockStdin(t, "\n")
	result := promptYesNo("Continue?", false)
	if result {
		t.Error("promptYesNo with empty input and defaultYes=false should return false")
	}
}

func TestPromptYesNo_ExplicitYes(t *testing.T) {
	for _, input := range []string{"y\n", "yes\n", "Y\n", "YES\n", "Yes\n"} {
		t.Run(input, func(t *testing.T) {
			MockStdin(t, input)
			result := promptYesNo("Continue?", false)
			if !result {
				t.Errorf("promptYesNo(%q) should return true", strings.TrimSpace(input))
			}
		})
	}
}

func TestPromptYesNo_ExplicitNo(t *testing.T) {
	for _, input := range []string{"n\n", "no\n", "N\n", "NO\n"} {
		t.Run(input, func(t *testing.T) {
			MockStdin(t, input)
			result := promptYesNo("Continue?", true)
			if result {
				t.Errorf("promptYesNo(%q) should return false", strings.TrimSpace(input))
			}
		})
	}
}

func TestPromptYesNo_EOF(t *testing.T) {
	// Pipe with no data simulates EOF
	MockStdin(t, "")
	result := promptYesNo("Continue?", true)
	if !result {
		t.Error("promptYesNo on EOF should return default (true)")
	}
}

// --- promptString tests ---

func TestPromptString_Default(t *testing.T) {
	MockStdin(t, "\n")
	result := promptString("Name", "falcon")
	if result != "falcon" {
		t.Errorf("promptString with empty input = %q, want 'falcon'", result)
	}
}

func TestPromptString_Custom(t *testing.T) {
	MockStdin(t, "nova\n")
	result := promptString("Name", "falcon")
	if result != "nova" {
		t.Errorf("promptString = %q, want 'nova'", result)
	}
}

func TestPromptString_Whitespace(t *testing.T) {
	MockStdin(t, "  spark  \n")
	result := promptString("Name", "falcon")
	if result != "spark" {
		t.Errorf("promptString = %q, want 'spark'", result)
	}
}

func TestPromptString_EOF(t *testing.T) {
	MockStdin(t, "")
	result := promptString("Name", "default")
	if result != "default" {
		t.Errorf("promptString on EOF = %q, want 'default'", result)
	}
}

// --- promptInt tests ---

func TestPromptInt_Default(t *testing.T) {
	MockStdin(t, "\n")
	result := promptInt("Count", 5)
	if result != 5 {
		t.Errorf("promptInt with empty input = %d, want 5", result)
	}
}

func TestPromptInt_ValidInt(t *testing.T) {
	MockStdin(t, "3\n")
	result := promptInt("Count", 5)
	if result != 3 {
		t.Errorf("promptInt = %d, want 3", result)
	}
}

func TestPromptInt_InvalidInt(t *testing.T) {
	MockStdin(t, "abc\n")
	result := promptInt("Count", 5)
	if result != 5 {
		t.Errorf("promptInt with invalid input = %d, want 5 (default)", result)
	}
}

// --- createWorktrees tests ---

func TestCreateWorktrees_NonInteractive_NoExisting(t *testing.T) {
	tmpDir := t.TempDir()
	worktreesDir := filepath.Join(tmpDir, "worktrees")
	os.MkdirAll(worktreesDir, 0755)

	origYes := initYes
	origNames := initNames
	initYes = true
	initNames = ""
	defer func() { initYes = origYes; initNames = origNames }()

	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(worktreesDir, "falcon"), "-b", "falcon"}, Stdout: "Created"},
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(worktreesDir, "nova"), "-b", "nova"}, Stdout: "Created"},
	})
	mock.InstallOn(deps)

	names := createWorktrees(deps, worktreesDir)
	if len(names) != 2 {
		t.Fatalf("createWorktrees returned %d names, want 2", len(names))
	}
	if names[0] != "falcon" || names[1] != "nova" {
		t.Errorf("createWorktrees = %v, want [falcon nova]", names)
	}
}

func TestCreateWorktrees_NonInteractive_WithExisting(t *testing.T) {
	tmpDir := t.TempDir()
	worktreesDir := filepath.Join(tmpDir, "worktrees")
	// Create an existing worktree "falcon"
	falconDir := filepath.Join(worktreesDir, "falcon")
	os.MkdirAll(falconDir, 0755)
	os.WriteFile(filepath.Join(falconDir, ".git"), []byte{}, 0644)

	origYes := initYes
	origNames := initNames
	initYes = true
	initNames = ""
	defer func() { initYes = origYes; initNames = origNames }()

	// Only "nova" should be created (falcon already exists and is filtered)
	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(worktreesDir, "nova"), "-b", "nova"}, Stdout: "Created"},
	})
	mock.InstallOn(deps)

	names := createWorktrees(deps, worktreesDir)
	if len(names) != 2 {
		t.Fatalf("createWorktrees returned %d names, want 2 (1 existing + 1 created)", len(names))
	}
	if names[0] != "falcon" || names[1] != "nova" {
		t.Errorf("createWorktrees = %v, want [falcon nova]", names)
	}
}

func TestCreateWorktrees_NonInteractive_CustomNames(t *testing.T) {
	tmpDir := t.TempDir()
	worktreesDir := filepath.Join(tmpDir, "worktrees")
	os.MkdirAll(worktreesDir, 0755)

	origYes := initYes
	origNames := initNames
	initYes = true
	initNames = "alpha,beta"
	defer func() { initYes = origYes; initNames = origNames }()

	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(worktreesDir, "alpha"), "-b", "alpha"}, Stdout: "Created"},
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(worktreesDir, "beta"), "-b", "beta"}, Stdout: "Created"},
	})
	mock.InstallOn(deps)

	names := createWorktrees(deps, worktreesDir)
	if len(names) != 2 {
		t.Fatalf("createWorktrees returned %d names, want 2", len(names))
	}
	if names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("createWorktrees = %v, want [alpha beta]", names)
	}
}

func TestCreateWorktrees_NonInteractive_NoneToCreate(t *testing.T) {
	tmpDir := t.TempDir()
	worktreesDir := filepath.Join(tmpDir, "worktrees")
	// Create both default worktrees
	for _, name := range []string{"falcon", "nova"} {
		d := filepath.Join(worktreesDir, name)
		os.MkdirAll(d, 0755)
		os.WriteFile(filepath.Join(d, ".git"), []byte{}, 0644)
	}

	origYes := initYes
	origNames := initNames
	initYes = true
	initNames = ""
	defer func() { initYes = origYes; initNames = origNames }()

	// No commands should be called since all names already exist
	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{})
	mock.InstallOn(deps)

	names := createWorktrees(deps, worktreesDir)
	if len(names) != 2 {
		t.Fatalf("createWorktrees returned %d names, want 2 existing", len(names))
	}
}

func TestCreateSingleWorktree_PathTraversal(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	deps, _, _, _, _ := NewTestDeps(t)

	traversalNames := []string{"..", "../../etc", "../secret"}
	for _, name := range traversalNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result := createSingleWorktree(deps, tmpDir, name)
			if result {
				t.Errorf("createSingleWorktree(%q, %q) should return false for path traversal", tmpDir, name)
			}
		})
	}
}

func TestValidateNewWorktreeName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		wantErr bool
		errMsg  string // substring expected in error message
	}{
		// Valid names
		{"falcon", false, ""},
		{"nova", false, ""},
		{"my-agent", false, ""},
		{"agent_1", false, ""},
		{"Agent2", false, ""},

		// Invalid — empty
		{"", true, "cannot be empty"},

		// Invalid — starts with '-'
		{"-flag", true, "must not start with '-'"},
		{"--orphan", true, "must not start with '-'"},
		{"-", true, "must not start with '-'"},

		// Invalid — path traversal dots
		{"..", true, "must not be '.' or '..'"},
		{".", true, "must not be '.' or '..'"},

		// Invalid — contains '..' (invalid git branch name)
		{"a..b", true, "must not contain '..'"},
		{"foo..bar", true, "must not contain '..'"},

		// Invalid — contains '/' (also catches ../evil)
		{"../evil", true, "must not contain '..'"},
		{"foo/bar", true, "invalid characters"},

		// Invalid — special characters
		{".hidden", true, "must not start or end with '.'"},
		{"a b", true, "invalid characters"},
		{"a:b", true, "invalid characters"},
		{"a*b", true, "invalid characters"},
		{"a?b", true, "invalid characters"},
		{"a<b", true, "invalid characters"},
		{"a>b", true, "invalid characters"},
		{"a|b", true, "invalid characters"},
		{"a\\b", true, "invalid characters"},
		{"a\"b", true, "invalid characters"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateNewWorktreeName(tc.name)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateNewWorktreeName(%q) error = %v, wantErr %v", tc.name, err, tc.wantErr)
			}
			if err != nil && tc.errMsg != "" && !strings.Contains(err.Error(), tc.errMsg) {
				t.Errorf("validateNewWorktreeName(%q) error = %v, want error containing %q", tc.name, err, tc.errMsg)
			}
		})
	}
}

func TestCreateSingleWorktree_InvalidName(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	// No command mock stubs — if git were called, the mock would panic
	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{})
	mock.InstallOn(deps)

	invalidNames := []string{"--orphan", "-flag", ".", "a b", "a/b"}
	for _, name := range invalidNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result := createSingleWorktree(deps, tmpDir, name)
			if result {
				t.Errorf("createSingleWorktree(%q, %q) should return false for invalid name", tmpDir, name)
			}
		})
	}
}

func TestCreateWorktrees_SkipsInvalidNames(t *testing.T) {
	tmpDir := t.TempDir()
	worktreesDir := filepath.Join(tmpDir, "worktrees")
	os.MkdirAll(worktreesDir, 0755)

	origYes := initYes
	origNames := initNames
	initYes = true
	initNames = "valid,--evil,ok"
	defer func() { initYes = origYes; initNames = origNames }()

	// Only "valid" and "ok" should trigger git commands; "--evil" is skipped
	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(worktreesDir, "valid"), "-b", "valid"}, Stdout: "Created"},
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(worktreesDir, "ok"), "-b", "ok"}, Stdout: "Created"},
	})
	mock.InstallOn(deps)

	names := createWorktrees(deps, worktreesDir)
	// Should have 2 created names (valid, ok) — "--evil" skipped
	if len(names) != 2 {
		t.Fatalf("createWorktrees returned %d names, want 2", len(names))
	}
	if names[0] != "valid" || names[1] != "ok" {
		t.Errorf("createWorktrees = %v, want [valid ok]", names)
	}
}

// --- showSummary tests ---

func TestShowSummary_MultipleNames(t *testing.T) {
	// Not parallel: captures os.Stdout which is a global.
	// Capture stdout
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	showSummary("worktrees", []string{"falcon", "nova"})

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "Setup complete!") {
		t.Error("showSummary should print 'Setup complete!'")
	}
	if !strings.Contains(output, "falcon") {
		t.Error("showSummary should include 'falcon'")
	}
	if !strings.Contains(output, "nova") {
		t.Error("showSummary should include 'nova'")
	}
	if !strings.Contains(output, "loom plan falcon") {
		t.Error("showSummary should show 'loom plan falcon' in next steps")
	}
}

func TestShowSummary_EmptyNames(t *testing.T) {
	// Not parallel: captures os.Stdout which is a global.
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	showSummary("worktrees", nil)

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// With no names, getFirstName returns "falcon"
	if !strings.Contains(output, "loom plan falcon") {
		t.Error("showSummary with nil names should fallback to 'falcon' in next steps")
	}
}

// --- Additional coverage tests ---

func TestInitIssueStorage_Failure(t *testing.T) {
	ResetWorkspaceRuntimeDirCache()
	defer ResetWorkspaceRuntimeDirCache()

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	os.Chdir(tmpDir)

	origYes := initYes
	initYes = true
	defer func() { initYes = origYes }()

	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, nil)
	mock.InstallOn(deps)

	result := initIssueStorage(deps)
	if !result {
		t.Error("initIssueStorage() should return true without invoking an external issue CLI")
	}
}

func TestCreateSingleWorktree_RetryFails(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	wtPath := filepath.Join(tmpDir, "falcon")

	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"worktree", "add", wtPath, "-b", "falcon"}, Stderr: "branch already exists", Err: errors.New("exit 1")},
		{Name: "git", Args: []string{"worktree", "add", wtPath, "falcon"}, Stderr: "worktree locked", Err: errors.New("exit 1")},
	})
	mock.InstallOn(deps)

	result := createSingleWorktree(deps, tmpDir, "falcon")
	if result {
		t.Error("createSingleWorktree() should return false when retry also fails")
	}
}

func TestCheckPrerequisites_InsideWorktree(t *testing.T) {
	// Not parallel: captures os.Stdout which is a global.
	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"rev-parse", "--is-inside-work-tree"}, Stdout: "true"},
		{Name: "git", Args: []string{"rev-parse", "--git-common-dir"}, Stdout: "/repo/.git"},
		{Name: "git", Args: []string{"rev-parse", "--git-dir"}, Stdout: "/repo/.git/worktrees/falcon"},
	})
	mock.InstallOn(deps)

	// Capture stderr to verify warning is printed
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	result := checkPrerequisites(deps)

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !result {
		t.Error("checkPrerequisites() should still return true when inside worktree")
	}
	if !strings.Contains(output, "Warning") {
		t.Error("checkPrerequisites() should warn when inside a worktree")
	}
}

func TestShowWorkspaceSummary(t *testing.T) {
	// Not parallel: captures os.Stdout which is a global.
	ws := WorkspaceConfig{
		Path: "/home/user/myworkspace",
		Repos: []RepoConfig{
			{Name: "backend", Path: "/home/user/myworkspace/backend"},
			{Name: "frontend", Path: "/home/user/myworkspace/frontend"},
		},
	}

	// Capture stdout
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	showWorkspaceSummary(ws)

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify key elements are printed
	if !strings.Contains(output, "Workspace ready!") {
		t.Error("showWorkspaceSummary should print 'Workspace ready!'")
	}
	if !strings.Contains(output, "/home/user/myworkspace") {
		t.Error("showWorkspaceSummary should include workspace path")
	}
	if !strings.Contains(output, ".loom/") {
		t.Error("showWorkspaceSummary should mention .loom/ runtime state")
	}
	if !strings.Contains(output, "loom agent backend") {
		t.Error("showWorkspaceSummary should suggest 'loom agent' with first repo name")
	}
	if !strings.Contains(output, "loom monitor") {
		t.Error("showWorkspaceSummary should suggest 'loom monitor'")
	}
}

func TestShowWorkspaceSummary_NoRepos(t *testing.T) {
	// Not parallel: captures os.Stdout which is a global.
	ws := WorkspaceConfig{
		Path:  "/home/user/emptyws",
		Repos: []RepoConfig{},
	}

	// Capture stdout
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	showWorkspaceSummary(ws)

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify basic elements are printed even with no repos
	if !strings.Contains(output, "Workspace ready!") {
		t.Error("showWorkspaceSummary should print 'Workspace ready!'")
	}
	if !strings.Contains(output, "/home/user/emptyws") {
		t.Error("showWorkspaceSummary should include workspace path")
	}
	// With no repos, the "loom agent" suggestion line should not include a repo name
	// (the function only prints that line if len(ws.Repos) > 0)
}
