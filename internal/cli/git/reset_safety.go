package git

import (
	"context"
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
)

type resetPreparation struct {
	branch  string
	capture agentcapture.Result
}

type ResetIgnoredFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// ListResetIgnored reads the paths and sizes a reset would remove.
func ListResetIgnored(ctx context.Context, path string) ([]ResetIgnoredFile, error) {
	entries, err := agentcapture.ListIgnored(ctx, path)
	if err != nil {
		return nil, err
	}
	ignored := make([]ResetIgnoredFile, 0, len(entries))
	for _, entry := range entries {
		ignored = append(ignored, ResetIgnoredFile{Path: entry.Path, Size: entry.Size})
	}
	return ignored, nil
}

func ignoredResetEntries(result agentcapture.Result) []ResetIgnoredFile {
	var ignored []ResetIgnoredFile
	for _, entry := range result.Entries {
		if entry.Class == "listed" {
			ignored = append(ignored, ResetIgnoredFile{Path: entry.Path, Size: entry.Size})
		}
	}
	return ignored
}

func prepareReset(worktreePath, targetBranch string, force bool) (resetPreparation, error) {
	branch, err := cli.GetCurrentBranch(worktreePath)
	if err != nil {
		return resetPreparation{}, fmt.Errorf("getting current branch: %w", err)
	}
	if isProtectedBranch(branch) || branch == targetBranch {
		return resetPreparation{}, loomgit.NewError(loomgit.Protected, fmt.Sprintf("branch %q is protected", branch), nil)
	}
	workspace, lead, err := resetCaptureIdentity(branch)
	if err != nil {
		return resetPreparation{}, err
	}
	lock, running, err := cli.CheckLock(worktreePath)
	if err != nil {
		return resetPreparation{}, fmt.Errorf("checking agent lock: %w", err)
	}
	if running {
		if !force {
			return resetPreparation{}, &LockedError{AgentName: lock.AgentName, PID: lock.PID, Duration: time.Since(lock.StartedAt).Round(time.Second), TaskID: lock.TaskID}
		}
		if err := stopResetAgent(worktreePath, lock.PID); err != nil {
			return resetPreparation{}, err
		}
	}
	result, err := agentcapture.CaptureWorkingArea(context.Background(), worktreePath, workspace, lead)
	if err != nil {
		return resetPreparation{}, loomgit.NewError(loomgit.CaptureIncomplete, "capture failed", err)
	}
	if !result.Complete {
		var missing []string
		for _, entry := range result.Entries {
			if entry.Class == "incomplete" || entry.Class == "secret_suspect" {
				missing = append(missing, entry.Path+" ("+entry.Class+")")
			}
		}
		return resetPreparation{}, loomgit.NewError(loomgit.CaptureIncomplete, strings.Join(missing, ", "), nil)
	}
	return resetPreparation{branch: branch, capture: result}, nil
}

func resetCaptureIdentity(branch string) (workspace, lead string, err error) {
	if workspace, lead, ok := loomgit.InteractiveIdentity(branch); ok {
		return workspace, lead, nil
	}
	return "", "", loomgit.NewError(loomgit.WorkspaceUnsupported, "reset requires a v2 working area", nil)
}

func stopResetAgent(path string, pid int) error {
	if pid <= 1 || pid == syscall.Getpid() {
		return fmt.Errorf("refusing unsafe agent PID %d", pid)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stopping agent: %w", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		lock, running, err := cli.CheckLock(path)
		if err != nil {
			return fmt.Errorf("checking stopped agent: %w", err)
		}
		if !running {
			return nil
		}
		if lock.PID != pid {
			return fmt.Errorf("agent changed while stopping; reset refused")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("agent did not stop; reset refused")
}

func finishReset(path, targetBranch string) error {
	lock, running, err := cli.CheckLock(path)
	if err != nil {
		return fmt.Errorf("rechecking agent lock: %w", err)
	}
	if running {
		return fmt.Errorf("agent %q restarted after capture; reset refused", lock.AgentName)
	}
	if err := GitFetch(path); err != nil {
		return fmt.Errorf("fetching: %w", err)
	}
	if err := GitReset(path, "origin/"+targetBranch); err != nil {
		return fmt.Errorf("resetting: %w", err)
	}
	if _, err := cli.RunGitCommand(path, "clean", "-fdx"); err != nil {
		return fmt.Errorf("cleaning: %w", err)
	}
	return nil
}
