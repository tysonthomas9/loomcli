package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(worktreesDir, "falcon"), "-b", "falcon"}, Stdout: "Created"},
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(worktreesDir, "nova"), "-b", "nova"}, Stdout: "Created"},
	})
	mock.Install()

	names := createWorktrees(worktreesDir)
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
	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(worktreesDir, "nova"), "-b", "nova"}, Stdout: "Created"},
	})
	mock.Install()

	names := createWorktrees(worktreesDir)
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

	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(worktreesDir, "alpha"), "-b", "alpha"}, Stdout: "Created"},
		{Name: "git", Args: []string{"worktree", "add", filepath.Join(worktreesDir, "beta"), "-b", "beta"}, Stdout: "Created"},
	})
	mock.Install()

	names := createWorktrees(worktreesDir)
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
	mock := NewCommandMock(t, []CommandStub{})
	mock.Install()

	names := createWorktrees(worktreesDir)
	if len(names) != 2 {
		t.Fatalf("createWorktrees returned %d names, want 2 existing", len(names))
	}
}

// --- showSummary tests ---

func TestShowSummary_MultipleNames(t *testing.T) {
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

func TestInitBeads_Failure(t *testing.T) {
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	os.Chdir(tmpDir)

	origYes := initYes
	initYes = true
	defer func() { initYes = origYes }()

	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"init"}, Stderr: "failed to init", Err: errors.New("exit 1")},
	})
	mock.Install()

	result := initBeads()
	if result {
		t.Error("initBeads() should return false when bd init fails")
	}
}

func TestCreateSingleWorktree_RetryFails(t *testing.T) {
	tmpDir := t.TempDir()
	wtPath := filepath.Join(tmpDir, "falcon")

	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"worktree", "add", wtPath, "-b", "falcon"}, Stderr: "branch already exists", Err: errors.New("exit 1")},
		{Name: "git", Args: []string{"worktree", "add", wtPath, "falcon"}, Stderr: "worktree locked", Err: errors.New("exit 1")},
	})
	mock.Install()

	result := createSingleWorktree(tmpDir, "falcon")
	if result {
		t.Error("createSingleWorktree() should return false when retry also fails")
	}
}

func TestCheckPrerequisites_InsideWorktree(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{
		{Name: "git", Args: []string{"rev-parse", "--is-inside-work-tree"}, Stdout: "true"},
		{Name: "git", Args: []string{"rev-parse", "--git-common-dir"}, Stdout: "/repo/.git"},
		{Name: "git", Args: []string{"rev-parse", "--git-dir"}, Stdout: "/repo/.git/worktrees/falcon"},
		{Name: "bd", Args: []string{"--version"}, Stdout: "beads v1.0.0"},
	})
	mock.Install()

	// Capture stderr to verify warning is printed
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	result := checkPrerequisites()

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
