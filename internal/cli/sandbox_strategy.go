package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// SandboxStrategy executes agent subprocesses inside OpenShell sandboxes.
// Each agent gets its own isolated container with the loom binary uploaded
// and the repo cloned from the remote at the agent's branch.
type SandboxStrategy struct {
	cfg          SandboxConfig
	projectDir   string
	repoURL      string
	openshellBin string // path to openshell binary; defaults to "openshell"
}

// Name returns the strategy identifier.
func (s *SandboxStrategy) Name() string {
	return "sandbox"
}

// openshellCmd returns the path to the openshell binary.
// If openshellBin is set (e.g. for testing), it is returned; otherwise "openshell".
func (s *SandboxStrategy) openshellCmd() string {
	if s.openshellBin != "" {
		return s.openshellBin
	}
	return "openshell"
}

// Spawn creates an OpenShell sandbox and starts the agent inside it.
// It generates a unique sandbox name, uploads the loom binary, clones the repo,
// and runs the agent's bootstrap script inside the sandbox.
func (s *SandboxStrategy) Spawn(ap *AgentProcess, loomArgs []string, env []string, logFile *os.File) (*exec.Cmd, error) {
	// Generate a unique sandbox name: loom-<worktree>-<unix_ms_hex>
	sandboxName := fmt.Sprintf("loom-%s-%x",
		filepath.Base(ap.entry.Worktree),
		time.Now().UnixMilli())
	ap.sandboxName = sandboxName

	// Best-effort cleanup of stale sandbox with same name (from prior crash)
	s.deleteSandbox(sandboxName)

	// Build the openshell sandbox create arguments
	args := s.buildCreateArgs(ap, sandboxName)

	cmd := exec.Command(s.openshellCmd(), args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("openshell sandbox create: %w", err)
	}

	return cmd, nil
}

// Kill sends SIGTERM to the openshell process group and best-effort deletes the sandbox.
func (s *SandboxStrategy) Kill(ap *AgentProcess) {
	ap.mu.Lock()
	pid := ap.pid
	sandboxName := ap.sandboxName
	ap.mu.Unlock()

	// 1. SIGTERM the openshell process group
	if pid > 0 {
		if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
			log.Printf("[sandbox] %s: SIGTERM to process group failed: %v", ap.entry.Worktree, err)
		}
	}

	// 2. Best-effort delete the sandbox container
	if sandboxName != "" {
		s.deleteSandbox(sandboxName)
	}
}

// Cleanup fetches changes pushed by the sandbox agent back to the host worktree
// and deletes the sandbox container.
func (s *SandboxStrategy) Cleanup(ap *AgentProcess) error {
	ap.mu.Lock()
	name := ap.sandboxName
	ap.sandboxName = ""
	ap.mu.Unlock()

	if name == "" {
		return nil
	}

	branch := ap.entry.Worktree

	// 1. Fetch changes the agent pushed from inside the sandbox
	fetchCmd := exec.Command("git", "fetch", "origin", branch)
	fetchCmd.Dir = s.projectDir
	if out, err := fetchCmd.CombinedOutput(); err != nil {
		log.Printf("[sandbox] %s: git fetch failed: %s: %v", ap.entry.Worktree, string(out), err)
		// Don't return — still need to delete sandbox
	}

	// 2. Fast-forward merge the worktree branch
	mergeCmd := exec.Command("git", "-C", ap.worktreePath, "merge",
		"--ff-only", fmt.Sprintf("origin/%s", branch))
	if out, err := mergeCmd.CombinedOutput(); err != nil {
		log.Printf("[sandbox] %s: git merge failed (may need manual resolution): %s: %v",
			ap.entry.Worktree, string(out), err)
	}

	// 3. Delete the sandbox
	s.deleteSandbox(name)

	return nil
}

// buildCreateArgs constructs the openshell sandbox create CLI arguments.
func (s *SandboxStrategy) buildCreateArgs(ap *AgentProcess, name string) []string {
	args := []string{"sandbox", "create", "--name", name}

	// Upload the loom binary (the ONLY upload — --upload accepts one value).
	// The --upload destination is a directory: the file keeps its original name.
	// So we need the source file to be named "loom" at the destination /sandbox/bin/.
	if loomBin, err := os.Executable(); err == nil {
		// If the binary isn't named "loom", copy it to a temp file named "loom"
		// so it lands at /sandbox/bin/loom inside the container.
		uploadPath := loomBin
		if filepath.Base(loomBin) != "loom" {
			tmpLoom := filepath.Join(os.TempDir(), "loom")
			if data, err := os.ReadFile(loomBin); err == nil {
				if err := os.WriteFile(tmpLoom, data, 0755); err == nil {
					uploadPath = tmpLoom
				}
			}
		}
		args = append(args, "--upload", uploadPath+":/sandbox/bin")
	}

	// Container image (--from)
	if s.cfg.From != "" {
		args = append(args, "--from", s.cfg.From)
	}

	// Providers for credential injection (repeatable flag)
	for _, p := range s.cfg.Providers {
		args = append(args, "--provider", p)
	}

	// Policy YAML file — only pass custom policies for now.
	// The "open" policy is skipped because the default sandbox policy already
	// allows network access, and passing --policy can cause provisioning issues
	// with some OpenShell versions.
	if s.cfg.Network != "" && s.cfg.Network != "open" {
		args = append(args, "--policy", s.cfg.Network)
	}

	// Disable PTY for non-interactive daemon mode
	args = append(args, "--no-tty")

	// Trailing command: bootstrap script that clones and runs loom
	args = append(args, "--")
	args = append(args, "sh", "-c", s.buildSandboxCommand(ap))

	return args
}

// buildSandboxCommand constructs the shell bootstrap script that runs inside the sandbox.
// It clones the repo at the agent's branch, makes loom executable, runs the agent,
// syncs beads state, and pushes changes back.
func (s *SandboxStrategy) buildSandboxCommand(ap *AgentProcess) string {
	branch := ap.entry.Worktree

	// Determine backend for sandbox execution
	backend := s.cfg.Backend
	if backend == "" {
		backend = ap.entry.Backend
	}

	var sb strings.Builder
	sb.WriteString("set -e\n")
	sb.WriteString("chmod +x /sandbox/bin/loom\n")
	// The sandbox proxy intercepts HTTPS but its CA cert is not in the container's
	// trust store. Disable git SSL verification for sandbox network operations.
	sb.WriteString("export GIT_SSL_NO_VERIFY=1\n")
	sb.WriteString(fmt.Sprintf("git clone --branch %s --single-branch %s /sandbox/repo\n",
		shellQuote(branch), shellQuote(s.repoURL)))
	sb.WriteString("cd /sandbox/repo\n")
	sb.WriteString("git config user.name \"loom-sandbox\"\n")
	sb.WriteString("git config user.email \"loom-sandbox@local\"\n")

	// Build the loom command with optional backend
	loomCmd := fmt.Sprintf("/sandbox/bin/loom task %s --auto --daemon-mode",
		shellQuote("worktrees/"+branch))
	if backend != "" {
		loomCmd += fmt.Sprintf(" --backend %s", shellQuote(backend))
	}
	sb.WriteString(loomCmd + "\n")

	sb.WriteString("bd sync\n")
	sb.WriteString("git add -A\n")
	sb.WriteString(fmt.Sprintf("git diff --cached --quiet || git commit -m %s\n",
		shellQuote(fmt.Sprintf("sandbox agent work [%s]", branch))))
	sb.WriteString(fmt.Sprintf("git push origin %s\n", shellQuote(branch)))

	return sb.String()
}

// ensurePolicyFile returns the path to the sandbox policy YAML file.
// If network is "open", it generates the default permissive policy file.
// Otherwise it returns the configured policy path as-is.
func (s *SandboxStrategy) ensurePolicyFile() string {
	network := s.cfg.Network
	if network == "" {
		network = "open"
	}

	if network != "open" {
		// Custom policy file path — use as-is
		return network
	}

	// Generate the permissive "open" policy file
	path := filepath.Join(s.projectDir, ".loom", "sandbox-policy-open.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		log.Printf("[sandbox] failed to create policy directory: %v", err)
		return ""
	}
	if err := os.WriteFile(path, []byte(openPolicyYAML), 0600); err != nil {
		log.Printf("[sandbox] failed to write policy file: %v", err)
		return ""
	}
	return path
}

// deleteSandbox runs "openshell sandbox delete <name>" with a 30-second timeout.
// Errors are logged but not returned (best-effort cleanup).
func (s *SandboxStrategy) deleteSandbox(name string) {
	if name == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.openshellCmd(), "sandbox", "delete", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[sandbox] delete %s: %s: %v", name, string(out), err)
	}
}

// shellQuote is defined in automode_tmux.go — it wraps a string in single
// quotes with proper escaping for shell use. Reused here for sandbox commands.
