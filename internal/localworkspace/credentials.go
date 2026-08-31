package localworkspace

import (
	"context"
	"fmt"
	"strings"
)

// LoomCredentialHelper is the git credential helper loom installs into managed
// https clones. It resolves the token from the environment so no secret is
// written to disk, and it is a no-op for non-`get` operations.
//
//nolint:gosec // G101: a git credential-helper script template that reads an env var, not a hardcoded credential.
const LoomCredentialHelper = `!f() { test "$1" = get || exit 0; echo username=x-access-token; echo "password=${GITHUB_TOKEN:-$GH_TOKEN}"; }; f`

// EnsureCredentialHelper configures repoPath to authenticate https remotes from
// the environment instead of a platform keychain. It is idempotent and a no-op
// for non-https remotes.
//
// Agents run under a daemon whose process has no working directory service, so
// every keychain- or ssh-backed credential path fails before it reads any
// config. An https remote plus a token in the environment is the one path that
// still works, and this is what routes git onto it.
//
// The empty first helper value resets the helpers this repo would otherwise
// inherit from the system and global config (osxkeychain, gh); the second
// supplies the token. Linked worktrees share the clone's .git/config, so every
// agent worktree inherits the setting.
func EnsureCredentialHelper(ctx context.Context, repoPath string) error {
	out, err := runGit(ctx, repoPath, "config", "--local", "--get", "remote.origin.url")
	if err != nil {
		return fmt.Errorf("read remote.origin.url: %w", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "https://") {
		return nil
	}

	// Missing key exits non-zero; that is "not configured", not a failure.
	existing, _ := runGit(ctx, repoPath, "config", "--local", "--get-all", "credential.helper")
	if credentialHelperConfigured(existing) {
		return nil
	}

	if _, err := runGit(ctx, repoPath, "config", "--local", "--replace-all", "credential.helper", ""); err != nil {
		return fmt.Errorf("reset credential.helper: %w", err)
	}
	if _, err := runGit(ctx, repoPath, "config", "--local", "--add", "credential.helper", LoomCredentialHelper); err != nil {
		return fmt.Errorf("set credential.helper: %w", err)
	}
	return nil
}

// credentialHelperConfigured reports whether git config output already lists
// exactly the reset entry followed by loom's helper.
func credentialHelperConfigured(configOutput string) bool {
	lines := strings.Split(strings.TrimRight(configOutput, "\n"), "\n")
	return len(lines) == 2 && lines[0] == "" && lines[1] == LoomCredentialHelper
}
