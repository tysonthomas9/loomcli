package cli

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// loomSessionRegex matches loom agent tmux sessions: loom-<wsPrefix>-<role>-<agent>-<PID>
// wsPrefix is 1-8 lowercase hex chars or "default"; role is "plan" or "task"; PID is numeric.
// Agent names may contain hyphens, so the PID is captured as the final numeric segment.
var loomSessionRegex = regexp.MustCompile(`^loom-([a-f0-9]{1,8}|default)-(plan|task)-(.+)-(\d+)$`)

// loomTmuxSession represents a parsed loom agent tmux session.
type loomTmuxSession struct {
	Name    string    // full session name, e.g. "loom-aaaabbbb-plan-falcon-12345"
	Role    string    // "plan" or "task"
	Agent   string    // agent name, e.g. "falcon"
	PID     int       // parent loom process PID (extracted from session name)
	Created time.Time // session creation time (from tmux #{session_created})
}

// listLoomTmuxSessions lists all tmux sessions matching the loom naming convention.
// It is a package-level variable for testability.
var listLoomTmuxSessions = defaultListLoomTmuxSessions

func defaultListLoomTmuxSessions() ([]loomTmuxSession, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}\t#{session_created}").CombinedOutput()
	if err != nil {
		msg := strings.ToLower(string(out))
		if strings.Contains(msg, "no server running") || strings.Contains(msg, "failed to connect") {
			return nil, nil // no tmux server = no sessions = no orphans
		}
		return nil, fmt.Errorf("tmux list-sessions: %w", err)
	}

	var sessions []loomTmuxSession
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		name := parts[0]
		matches := loomSessionRegex.FindStringSubmatch(name)
		if matches == nil {
			continue // not a loom agent session
		}
		pid, _ := strconv.Atoi(matches[4])
		s := loomTmuxSession{
			Name:  name,
			Role:  matches[2],
			Agent: matches[3],
			PID:   pid,
		}
		if len(parts) == 2 {
			if ts, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64); err == nil {
				s.Created = time.Unix(ts, 0)
			}
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// killTmuxSession kills a tmux session by name. Package-level variable for testability.
var killTmuxSession = func(name string) error {
	return exec.Command("tmux", "kill-session", "-t", name).Run()
}

func checkOrphanedTmuxSessions() CheckResult {
	sessions, err := listLoomTmuxSessions()
	if err != nil {
		return CheckResult{
			Name:    "orphaned_tmux_sessions",
			Status:  StatusWarn,
			Summary: "could not list tmux sessions",
			Detail:  err.Error(),
		}
	}
	if len(sessions) == 0 {
		return CheckResult{} // skip — no loom tmux sessions exist
	}

	var orphaned []loomTmuxSession
	for _, s := range sessions {
		if !lockfile.IsProcessRunning(s.PID) {
			orphaned = append(orphaned, s)
		}
	}

	if len(orphaned) == 0 {
		return CheckResult{
			Name:    "orphaned_tmux_sessions",
			Status:  StatusPass,
			Summary: "no orphaned tmux sessions",
		}
	}

	var details []string
	for _, s := range orphaned {
		age := time.Since(s.Created).Truncate(time.Second)
		details = append(details, fmt.Sprintf("%s (role=%s agent=%s pid=%d age=%s)", s.Name, s.Role, s.Agent, s.PID, age))
	}

	if doctorFix {
		return fixOrphanedTmuxSessions(orphaned, details)
	}

	return CheckResult{
		Name:    "orphaned_tmux_sessions",
		Status:  StatusWarn,
		Summary: fmt.Sprintf("%d orphaned tmux session(s) found", len(orphaned)),
		Detail:  strings.Join(details, "\n") + "\nRun: loom doctor --fix",
	}
}

func fixOrphanedTmuxSessions(orphaned []loomTmuxSession, details []string) CheckResult {
	fixed := 0
	var failures []string
	for _, s := range orphaned {
		if err := killTmuxSession(s.Name); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", s.Name, err))
		} else {
			fixed++
		}
	}
	if len(failures) > 0 {
		return CheckResult{
			Name:    "orphaned_tmux_sessions",
			Status:  StatusWarn,
			Summary: fmt.Sprintf("fixed %d orphaned session(s), %d failed", fixed, len(failures)),
			Detail:  strings.Join(append(details, failures...), "\n"),
		}
	}
	return CheckResult{
		Name:    "orphaned_tmux_sessions",
		Status:  StatusPass,
		Summary: fmt.Sprintf("fixed %d orphaned tmux session(s)", fixed),
		Detail:  strings.Join(details, "\n"),
	}
}
