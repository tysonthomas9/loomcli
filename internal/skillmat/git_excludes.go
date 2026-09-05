package skillmat

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//nolint:funlen // Exclude-file reconciliation keeps its read/merge/write steps inline.
func ensureGitExcludes(ctx context.Context, targetDir string) error {
	inside := gitCommandContext(ctx, targetDir, "rev-parse", "--is-inside-work-tree")
	out, err := inside.Output()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
	if strings.TrimSpace(string(out)) != "true" {
		return nil
	}
	resolve := gitCommandContext(ctx, targetDir, "rev-parse", "--git-path", "info/exclude")
	out, err = resolve.Output()
	if err != nil {
		return fmt.Errorf("resolve git info/exclude: %w", err)
	}
	excludePath := strings.TrimSpace(string(out))
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(targetDir, excludePath)
	}
	excludePath = filepath.Clean(excludePath)
	excludeRootPath := filepath.Dir(filepath.Dir(excludePath))
	excludeName, err := filepath.Rel(excludeRootPath, excludePath)
	if err != nil {
		return fmt.Errorf("resolve git exclude relative path: %w", err)
	}
	excludeName = filepath.ToSlash(excludeName)
	excludeRoot, err := openSecureRoot(excludeRootPath)
	if err != nil {
		return fmt.Errorf("open git metadata root %q: %w", excludeRootPath, err)
	}
	defer excludeRoot.Close()
	b, _, err := excludeRoot.ReadFile(excludeName, 0)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	wanted := []string{
		AgentsSkillsDir, AgentsSkillsDir + "/",
		ClaudeSkillsDir, ClaudeSkillsDir + "/",
		projectionStateDir + "/",
	}
	present := make(map[string]bool, len(wanted))
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		present[line] = true
	}
	var addition strings.Builder
	if len(b) > 0 && b[len(b)-1] != '\n' {
		addition.WriteByte('\n')
	}
	for _, line := range wanted {
		if !present[line] {
			addition.WriteString(line)
			addition.WriteByte('\n')
		}
	}
	if addition.Len() == 0 {
		return nil
	}
	return excludeRoot.AppendFile(excludeName, []byte(addition.String()), 0o644)
}

func gitCommandContext(ctx context.Context, targetDir string, args ...string) *exec.Cmd {
	commandArgs := append([]string{"-C", targetDir}, args...)
	cmd := exec.CommandContext(ctx, "git", commandArgs...) //nolint:gosec,norawexec // fixed git inspection command
	cmd.Env = sanitizedGitEnv(os.Environ())
	return cmd
}

func sanitizedGitEnv(env []string) []string {
	clean := make([]string, 0, len(env))
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			continue
		}
		clean = append(clean, item)
	}
	return clean
}
