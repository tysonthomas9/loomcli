package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// TestResolveRoleConfigStaticAcceptsBuiltinPrompt is the daemon half of the
// builtin: gap: an agent role whose prompt_file names a prompt that ships with
// loom used to die here, because resolution stat-ed the reference as a path.
//
// The value must also survive UNCHANGED: spawn forwards prompt_file verbatim to
// `loom agent --prompt`, so rewriting it into projectDir/builtin:team-architect
// would hand the worker a file that cannot exist.
func TestResolveRoleConfigStaticAcceptsBuiltinPrompt(t *testing.T) {
	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			"app-architect": {
				Kind:       "worker",
				PromptFile: "builtin:team-architect",
				TaskFilter: "any",
			},
		},
	}

	rc, err := ResolveRoleConfigStatic("app-architect", cfg, t.TempDir())
	if err != nil {
		t.Fatalf("ResolveRoleConfigStatic error = %v, want the built-in prompt reference accepted", err)
	}
	if rc.PromptFile != "builtin:team-architect" {
		t.Errorf("PromptFile = %q, want the reference forwarded unchanged", rc.PromptFile)
	}
}

func TestResolveRoleConfigStaticRejectsUnknownBuiltinPrompt(t *testing.T) {
	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			"app-architect": {
				Kind:       "worker",
				PromptFile: "builtin:team-nope",
			},
		},
	}

	_, err := ResolveRoleConfigStatic("app-architect", cfg, t.TempDir())
	if err == nil {
		t.Fatal("ResolveRoleConfigStatic error = nil, want an unknown built-in prompt error")
	}
	if !strings.Contains(err.Error(), "team-nope") {
		t.Errorf("error = %q, want it to name the unknown prompt", err)
	}
	if strings.Contains(err.Error(), "not found:") {
		t.Errorf("error = %q, want a registry error rather than a filesystem error", err)
	}
}

// TestResolveRoleConfigStaticRejectsInteractiveBuiltinPrompt keeps the two
// registries apart on the daemon side: a terminal prompt has no worker workflow
// and must not be spawnable as one.
func TestResolveRoleConfigStaticRejectsInteractiveBuiltinPrompt(t *testing.T) {
	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			"helper": {
				Kind:       "worker",
				PromptFile: "builtin:pr-review",
			},
		},
	}

	if _, err := ResolveRoleConfigStatic("helper", cfg, t.TempDir()); err == nil {
		t.Fatal("ResolveRoleConfigStatic error = nil, want an interactive prompt ID rejected for a worker agent role")
	}
}

// TestBuildAgentExecCmdForwardsBuiltinPromptVerbatim closes the loop between
// the daemon and the CLI: spawn needs no change for built-in prompts precisely
// because it forwards prompt_file untouched, and this pins that.
func TestBuildAgentExecCmdForwardsBuiltinPromptVerbatim(t *testing.T) {
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "app-architect-1", Role: "app-architect"},
		RoleConfig:   cfgpkg.RoleConfig{PromptFile: "builtin:team-architect", TaskFilter: "any"},
		WorktreePath: t.TempDir(),
	}

	cmd, err := buildAgentExecCmd(ap, "", "")
	if err != nil {
		t.Fatalf("buildAgentExecCmd error = %v", err)
	}

	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--prompt builtin:team-architect") {
		t.Errorf("spawn args = %q, want the built-in prompt reference forwarded verbatim", args)
	}
}

// TestResolveRoleConfigStaticStillResolvesPromptFiles guards the unchanged
// path: a real relative prompt file is still made absolute against projectDir,
// and a missing one is still an error.
func TestResolveRoleConfigStaticStillResolvesPromptFiles(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "critic.md"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			"critic":  {Kind: "worker", PromptFile: "critic.md"},
			"missing": {Kind: "worker", PromptFile: "nope.md"},
		},
	}

	rc, err := ResolveRoleConfigStatic("critic", cfg, projectDir)
	if err != nil {
		t.Fatalf("ResolveRoleConfigStatic error = %v", err)
	}
	if want := filepath.Join(projectDir, "critic.md"); rc.PromptFile != want {
		t.Errorf("PromptFile = %q, want %q", rc.PromptFile, want)
	}

	if _, err := ResolveRoleConfigStatic("missing", cfg, projectDir); err == nil {
		t.Fatal("ResolveRoleConfigStatic error = nil for a missing prompt file, want an error")
	}
}
