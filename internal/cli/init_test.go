package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParseNames(t *testing.T) {
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
	tmpDir := t.TempDir()
	got := listExistingWorktrees(tmpDir)
	if len(got) != 0 {
		t.Errorf("listExistingWorktrees() on empty dir = %v, want empty", got)
	}
}

func TestListExistingWorktrees_NonexistentDir(t *testing.T) {
	got := listExistingWorktrees("/nonexistent/path/12345")
	if got != nil {
		t.Errorf("listExistingWorktrees() on nonexistent = %v, want nil", got)
	}
}

func TestCheckPrerequisites_NotGitRepo(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"rev-parse", "--is-inside-work-tree"}, Err: errors.New("not a git repo")},
	})
	mock.Install()

	result := checkPrerequisites()
	if result {
		t.Error("checkPrerequisites() should return false when not in git repo")
	}
}

func TestCheckPrerequisites_NoBd(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"rev-parse", "--is-inside-work-tree"}, Stdout: "true"},
		{Name: "git", Args: []string{"rev-parse", "--git-common-dir"}, Stdout: ".git"},
		{Name: "git", Args: []string{"rev-parse", "--git-dir"}, Stdout: ".git"},
		{Name: "bd", Args: []string{"--version"}, Err: errors.New("bd not found")},
	})
	mock.Install()

	result := checkPrerequisites()
	if result {
		t.Error("checkPrerequisites() should return false when bd not installed")
	}
}

func TestCheckPrerequisites_Success(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"rev-parse", "--is-inside-work-tree"}, Stdout: "true"},
		{Name: "git", Args: []string{"rev-parse", "--git-common-dir"}, Stdout: ".git"},
		{Name: "git", Args: []string{"rev-parse", "--git-dir"}, Stdout: ".git"},
		{Name: "bd", Args: []string{"--version"}, Stdout: "beads v1.0.0"},
	})
	mock.Install()

	result := checkPrerequisites()
	if !result {
		t.Error("checkPrerequisites() should return true when all prerequisites met")
	}
}

func TestInitBeads_AlreadyInitialized(t *testing.T) {
	// Save and restore working directory
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	os.Chdir(tmpDir)

	// Create .beads directory
	os.MkdirAll(".beads", 0755)

	// No mock needed - should skip without running any commands
	result := initBeads()
	if !result {
		t.Error("initBeads() should return true when already initialized")
	}
}

func TestInitBeads_Initialize(t *testing.T) {
	// Save and restore working directory
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	os.Chdir(tmpDir)

	// Set non-interactive mode
	origYes := initYes
	initYes = true
	defer func() { initYes = origYes }()

	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"init"}, Stdout: "Initialized beads"},
	})
	mock.Install()

	result := initBeads()
	if !result {
		t.Error("initBeads() should return true after successful init")
	}
}

func TestCreateWorktreesDir_AlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	result := createWorktreesDir(tmpDir)
	if !result {
		t.Error("createWorktreesDir() should return true for existing directory")
	}
}

func TestCreateWorktreesDir_NotADirectory(t *testing.T) {
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
	tmpDir := t.TempDir()

	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(tmpDir, "falcon"), "-b", "falcon"}, Stdout: "Created worktree"},
	})
	mock.Install()

	result := createSingleWorktree(tmpDir, "falcon")
	if !result {
		t.Error("createSingleWorktree() should return true on success")
	}
}

func TestCreateSingleWorktree_BranchExists(t *testing.T) {
	tmpDir := t.TempDir()
	wtPath := filepath.Join(tmpDir, "falcon")

	mock := NewCommandMock(t, []CommandStub{
		// First attempt fails with branch exists
		{Name: "git", Args: []string{"worktree", "add", wtPath, "-b", "falcon"}, Stderr: "branch already exists", Err: errors.New("exit 1")},
		// Second attempt without -b
		{Name: "git", Args: []string{"worktree", "add", wtPath, "falcon"}, Stdout: "Created worktree"},
	})
	mock.Install()

	result := createSingleWorktree(tmpDir, "falcon")
	if !result {
		t.Error("createSingleWorktree() should retry without -b when branch exists")
	}
}

func TestCreateSingleWorktree_AlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	wtPath := filepath.Join(tmpDir, "falcon")
	os.MkdirAll(wtPath, 0755)

	// No mock needed - should skip without running git
	result := createSingleWorktree(tmpDir, "falcon")
	if result {
		t.Error("createSingleWorktree() should return false when path exists")
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
