// Package refname is the sole builder of Loom Git refs, branches, and task-copy paths.
package refname

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/errcode"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
)

const WorkspaceRefPrefix = "refs/loom/ws/"

func component(id string) error {
	if id == "" || id == "." || strings.Contains(id, "..") ||
		strings.ContainsAny(id, "/\\ \t\n\r~^:?*[\x00") || strings.Contains(id, "@{") {
		return fmt.Errorf("invalid ref id %q", id)
	}
	return nil
}

func build(branch bool, ids []string, parts ...string) (string, error) {
	for _, id := range ids {
		if err := component(id); err != nil {
			return "", err
		}
	}
	name := strings.Join(parts, "/")
	if err := gitexec.CheckRefFormat(name, branch); err != nil {
		return "", fmt.Errorf("invalid ref %q: %w", name, err)
	}
	return name, nil
}

func branch(workspace, kind, id string) (string, error) {
	return build(true, []string{workspace, id}, "loom/ws", workspace, kind, id)
}

func hidden(workspace string, ids []string, parts ...string) (string, error) {
	return build(false, append([]string{workspace}, ids...), append([]string{"refs/loom/ws", workspace}, parts...)...)
}

func InteractiveBranch(workspace, lead string) (string, error) {
	return branch(workspace, "interactive", lead)
}

func InteractiveIdentity(name string) (workspace, lead string, ok bool) {
	parts := strings.Split(name, "/")
	if len(parts) != 5 {
		return "", "", false
	}
	want, err := InteractiveBranch(parts[2], parts[4])
	if err != nil || name != want {
		return "", "", false
	}
	return parts[2], parts[4], true
}

func IsInteractiveLeadBranch(name string) bool {
	parts := strings.Split(name, "/")
	if len(parts) != 5 {
		return false
	}
	want, err := InteractiveBranch(parts[2], "lead")
	return err == nil && name == want
}

func ChangeBranch(workspace, change string) (string, error) {
	return branch(workspace, "change", change)
}

func AttemptBase(workspace, attempt string) (string, error) {
	return hidden(workspace, []string{attempt}, "attempt", attempt, "base")
}

func AttemptCapture(workspace, attempt string) (string, error) {
	return hidden(workspace, []string{attempt}, "attempt", attempt, "capture")
}

func RevisionBase(workspace, change, revision string) (string, error) {
	return hidden(workspace, []string{change, revision}, "change", change, revision, "base")
}

func RevisionHead(workspace, change, revision string) (string, error) {
	return hidden(workspace, []string{change, revision}, "change", change, revision, "head")
}

func Publication(workspace, change string) (string, error) {
	return hidden(workspace, []string{change}, "pub", change)
}

func PRHead(workspace, number string) (string, error) {
	return hidden(workspace, []string{number}, "pr", number, "head")
}

func InteractiveBackup(workspace, lead string) (string, error) {
	return hidden(workspace, []string{lead}, "interactive", lead)
}

func WIP(workspace, lead, id string) (string, error) {
	return hidden(workspace, []string{lead, id}, "wip", lead, id)
}

func WorkspacePrefix(workspace string) (string, error) {
	if err := component(workspace); err != nil {
		return "", err
	}
	return "refs/loom/ws/" + workspace + "/", nil
}

// TaskCopyPath is a workspace-scoped relative path keyed only by task-copy ID.
func TaskCopyPath(workspace, taskCopy string) (string, error) {
	name, err := hidden(workspace, []string{taskCopy}, "task-copy", taskCopy)
	if err != nil {
		return "", err
	}
	return filepath.FromSlash(name), nil
}

// CheckNamespace rejects exact ancestor branches that block Loom branches.
func CheckNamespace(repo, workspace string) error {
	if err := component(workspace); err != nil {
		return err
	}
	for _, ref := range []string{"refs/heads/loom", "refs/heads/loom/ws", "refs/heads/loom/ws/" + workspace} {
		exists, err := gitexec.RefExists(repo, ref)
		if err != nil {
			return fmt.Errorf("check namespace %s: %w", ref, err)
		}
		if exists {
			return errcode.New(errcode.RefNamespaceConflict, ref, nil)
		}
	}
	return nil
}
