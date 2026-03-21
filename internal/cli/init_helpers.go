package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// checkPrerequisites verifies bd and git are available and we're in a git repo
func checkPrerequisites() bool {
	// Check if we're in a git repository
	result := execCommand(".", "git", "rev-parse", "--is-inside-work-tree")
	if result.Err != nil {
		fmt.Println("✗ Not a git repository")
		fmt.Println("")
		fmt.Println("  Please run 'loom init' from within a git repository.")
		fmt.Println("  You can initialize a new repository with: git init")
		return false
	}
	fmt.Println("✓ git repository detected")

	// Check if we're in the main worktree (not inside a worktree)
	result = execCommand(".", "git", "rev-parse", "--git-common-dir")
	if result.Err == nil {
		gitCommonDir := strings.TrimSpace(result.Stdout)
		result2 := execCommand(".", "git", "rev-parse", "--git-dir")
		if result2.Err == nil {
			gitDir := strings.TrimSpace(result2.Stdout)
			// If git-dir and git-common-dir differ, we're in a worktree
			if gitDir != gitCommonDir && gitDir != ".git" {
				fmt.Println("⚠ Warning: You appear to be inside a worktree")
				fmt.Println("  Consider running 'loom init' from the main repository")
			}
		}
	}

	// Check if bd (beads) CLI is available (skip when fleet-db is active)
	if isFleetDBActive() {
		fmt.Println("✓ fleet-db backend active (bd CLI not required)")
	} else if result = execCommand(".", "bd", "--version"); result.Err != nil {
		fmt.Println("✗ bd (beads CLI) not found")
		fmt.Println("\n  Please install beads CLI from the vendored source:")
		fmt.Println("    make install-bd")
		return false
	} else {
		fmt.Println("✓ bd (beads CLI) found")
	}

	return true
}

// initBeads initializes beads if not already done
func initBeads() bool {
	if isFleetDBActive() {
		fmt.Println("→ Skipping beads init (fleet-db backend active)")
		return true
	}
	if _, err := os.Stat(filepath.Join(GetBeadsDir(), ".beads")); err == nil {
		fmt.Println("✓ beads already initialized, skipping...")
		return true
	}

	if !initYes {
		if !promptYesNo("Initialize beads issue tracker?", true) {
			fmt.Println("→ Skipping beads initialization")
			return true
		}
	}

	fmt.Println("→ Creating .beads/ directory...")
	result := execCommand(GetBeadsDir(), "bd", "init")
	if result.Err != nil {
		fmt.Fprintf(os.Stderr, "✗ Failed to initialize beads: %s\n", result.Stderr)
		return false
	}
	fmt.Println("✓ beads initialized")
	return true
}

// getWorktreesDirForInit returns the worktrees directory to use
func getWorktreesDirForInit() string {
	if initWorktreesDir != "" {
		return initWorktreesDir
	}
	return GetWorktreesDir()
}

// createWorktreesDir creates the worktrees directory if it doesn't exist
func createWorktreesDir(dir string) bool {
	// Check if directory exists
	if info, err := os.Stat(dir); err == nil {
		if info.IsDir() {
			fmt.Printf("✓ Directory '%s' already exists\n", dir)
			return true
		}
		fmt.Fprintf(os.Stderr, "✗ '%s' exists but is not a directory\n", dir)
		return false
	}

	if !initYes {
		if !promptYesNo(fmt.Sprintf("Create worktrees directory '%s'?", dir), true) {
			fmt.Println("→ Skipping directory creation")
			return true
		}
	}

	fmt.Printf("→ Creating directory '%s'...\n", dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "✗ Failed to create directory: %v\n", err)
		return false
	}
	fmt.Printf("✓ Created '%s'\n", dir)
	return true
}

// createWorktrees handles the interactive worktree creation
func createWorktrees(worktreesDir string) []string {
	// Check for existing worktrees
	existingWorktrees := listExistingWorktrees(worktreesDir)
	if len(existingWorktrees) > 0 {
		fmt.Println("Existing worktrees:")
		for _, name := range existingWorktrees {
			fmt.Printf("  - %s\n", name)
		}
		fmt.Println("")

		if !initYes {
			if !promptYesNo("Create additional worktrees?", true) {
				fmt.Println("→ Skipping worktree creation")
				return existingWorktrees
			}
		}
	}

	// Determine names to create
	var namesToCreate []string
	if initYes {
		// Non-interactive mode
		if initNames != "" {
			namesToCreate = parseNames(initNames)
		} else {
			namesToCreate = defaultAgentNames
		}
		// Filter out existing worktrees
		namesToCreate = filterExisting(namesToCreate, existingWorktrees)
	} else {
		// Interactive mode
		namesToCreate = promptForWorktreeNames(existingWorktrees)
	}

	if len(namesToCreate) == 0 {
		fmt.Println("→ No new worktrees to create")
		return existingWorktrees
	}

	// Create each worktree
	createdNames := []string{}
	for _, name := range namesToCreate {
		if createSingleWorktree(worktreesDir, name) {
			createdNames = append(createdNames, name)
		}
	}

	return append(existingWorktrees, createdNames...)
}

// listExistingWorktrees returns names of directories in the worktrees dir
func listExistingWorktrees(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			// Check if it's a git worktree
			gitFile := filepath.Join(dir, entry.Name(), ".git")
			if _, err := os.Stat(gitFile); err == nil {
				names = append(names, entry.Name())
			}
		}
	}
	return names
}

// validateNewWorktreeName checks that a worktree name is safe for creation.
// This is stricter than validateWorktreeName (which only blocks path traversal)
// because new worktree names must also avoid git flag injection and invalid characters.
func validateNewWorktreeName(name string) error {
	if name == "" {
		return fmt.Errorf("worktree name cannot be empty")
	}
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("invalid worktree name %q: must not start with '-'", name)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("invalid worktree name %q: must not be '.' or '..'", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("invalid worktree name %q: must not contain '..'", name)
	}
	if strings.ContainsAny(name, "/ \\:*?\"<>|") {
		return fmt.Errorf("invalid worktree name %q: contains invalid characters", name)
	}
	return nil
}

// parseNames splits comma-separated names
func parseNames(names string) []string {
	parts := strings.Split(names, ",")
	var result []string
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name != "" {
			result = append(result, name)
		}
	}
	return result
}

// filterExisting removes names that already exist
func filterExisting(names, existing []string) []string {
	existingSet := make(map[string]bool)
	for _, name := range existing {
		existingSet[name] = true
	}

	var result []string
	for _, name := range names {
		if !existingSet[name] {
			result = append(result, name)
		}
	}
	return result
}

// promptForWorktreeNames interactively asks for worktree names
func promptForWorktreeNames(existing []string) []string {
	count := promptInt("How many agent worktrees?", 2)
	if count <= 0 {
		return nil
	}

	existingSet := make(map[string]bool)
	for _, name := range existing {
		existingSet[name] = true
	}

	fmt.Println("")
	fmt.Println("Suggested names: " + strings.Join(suggestedAgentNames, ", "))
	fmt.Println("")

	var names []string
	defaultIdx := 0
	for i := 0; i < count; i++ {
		// Find next unused default name
		defaultName := ""
		for defaultIdx < len(suggestedAgentNames) {
			candidate := suggestedAgentNames[defaultIdx]
			if !existingSet[candidate] {
				defaultName = candidate
				break
			}
			defaultIdx++
		}
		if defaultName == "" {
			defaultName = fmt.Sprintf("agent%d", i+1)
		}

		for {
			name := promptString(fmt.Sprintf("Name for worktree %d", i+1), defaultName)
			name = strings.TrimSpace(name)

			if err := validateNewWorktreeName(name); err != nil {
				fmt.Printf("  %v, try again\n", err)
				continue
			}

			if existingSet[name] {
				fmt.Printf("  '%s' already exists, choose a different name\n", name)
				continue
			}

			names = append(names, name)
			existingSet[name] = true
			defaultIdx++
			break
		}
	}

	return names
}

// createSingleWorktree creates one worktree
func createSingleWorktree(worktreesDir, name string) bool {
	if err := validateNewWorktreeName(name); err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ Skipping worktree: %v\n", err)
		return false
	}
	worktreePath := filepath.Join(worktreesDir, name)

	fmt.Printf("→ Creating worktree %s on branch %s...\n", name, name)

	// Let git worktree add fail naturally if the path or branch already exists,
	// avoiding a TOCTOU race between an os.Stat check and the git command.
	result := execCommand(".", "git", "worktree", "add", worktreePath, "-b", name)
	if result.Err != nil {
		stderr := result.Stderr
		if strings.Contains(stderr, "already exists") ||
			strings.Contains(stderr, "already a worktree") {
			// Branch or path already exists — try without -b (use existing branch)
			result = execCommand(".", "git", "worktree", "add", worktreePath, name)
			if result.Err != nil {
				if strings.Contains(result.Stderr, "already exists") ||
					strings.Contains(result.Stderr, "already a worktree") ||
					strings.Contains(result.Stderr, "already checked out") {
					fmt.Printf("→ Worktree '%s' already exists, skipping\n", name)
					return false
				}
				fmt.Fprintf(os.Stderr, "  ✗ Failed to create worktree: %s\n", strings.TrimSpace(result.Stderr))
				return false
			}
		} else {
			fmt.Fprintf(os.Stderr, "  ✗ Failed to create worktree: %s\n", strings.TrimSpace(stderr))
			return false
		}
	}

	fmt.Printf("  ✓ Created %s\n", worktreePath)

	// Auto-install Claude Code hooks (non-fatal on failure)
	if err := InstallClaudeHooks(worktreePath); err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠ Failed to install hooks: %v\n", err)
	} else {
		fmt.Printf("  ✓ Installed Claude Code hooks\n")
	}

	return true
}

// showSummary displays the final setup summary and next steps
func showSummary(worktreesDir string, names []string) {
	fmt.Println("Setup complete! 🎉")
	fmt.Println("")
	fmt.Println("Directory structure:")
	fmt.Println("  .beads/           Issue database")
	fmt.Printf("  %s/\n", worktreesDir)
	for _, name := range names {
		fmt.Printf("    %s/         Agent workspace (branch: %s)\n", name, name)
	}
	fmt.Println("")
	fmt.Println("Next steps:")
	fmt.Println("  1. Create tasks:     bd create --title=\"My first task\" --type=task")
	fmt.Println("  2. Run planner:      loom plan " + getFirstName(names))
	fmt.Println("  3. Review plans:     loom lead")
	fmt.Println("  4. Implement:        loom task " + getFirstName(names))
	fmt.Println("  5. Monitor:          loom monitor")
	fmt.Println("")
}

func getFirstName(names []string) string {
	if len(names) > 0 {
		return names[0]
	}
	return "falcon"
}

// Interactive prompt helpers

func promptYesNo(prompt string, defaultYes bool) bool { //nolint:unparam // defaultYes is always true in production but tested with false
	reader := bufio.NewReader(os.Stdin)
	hint := "[y/N]"
	if defaultYes {
		hint = "[Y/n]"
	}
	fmt.Printf("%s %s ", prompt, hint)

	input, err := reader.ReadString('\n')
	if err != nil {
		return defaultYes
	}

	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return defaultYes
	}
	return input == "y" || input == "yes"
}

func promptString(prompt, defaultVal string) string {
	reader := bufio.NewReader(os.Stdin)
	if defaultVal != "" {
		fmt.Printf("%s [%s]: ", prompt, defaultVal)
	} else {
		fmt.Printf("%s: ", prompt)
	}

	input, err := reader.ReadString('\n')
	if err != nil {
		return defaultVal
	}

	input = strings.TrimSpace(input)
	if input == "" {
		return defaultVal
	}
	return input
}

func promptInt(prompt string, defaultVal int) int {
	input := promptString(prompt, strconv.Itoa(defaultVal))
	val, err := strconv.Atoi(input)
	if err != nil {
		return defaultVal
	}
	return val
}
