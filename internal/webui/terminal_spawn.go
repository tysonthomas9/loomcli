package webui

import (
	"fmt"
)

// Spawn creates a tmux session in the given workspace without attaching a PTY
// connection. It is idempotent: if the session already exists, it returns
// (false, nil). The returned bool indicates whether a new session was created.
//
// wsID must be non-empty.
func (m *TerminalManager) Spawn(wsID, name, command string, cols, rows uint16) (bool, error) {
	if wsID == "" {
		return false, fmt.Errorf("wsID must not be empty")
	}
	if !validSessionName.MatchString(name) {
		return false, fmt.Errorf("invalid session name %q: must match [a-zA-Z0-9_-]+", name)
	}
	if cols == 0 {
		cols = m.defaultCols
	}
	if rows == 0 {
		rows = m.defaultRows
	}

	internalName := m.tmuxName(wsID, name)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tmuxHasSession(internalName) {
		return false, nil
	}

	if err := m.tmuxNewSession(internalName, command, cols, rows); err != nil {
		return false, fmt.Errorf("tmux new-session: %w", err)
	}
	return true, nil
}

// tmuxNewSessionArgv creates a new detached tmux session that execs argv directly
// instead of passing the command through a shell. Use this when any argument may
// contain user-supplied text, to avoid shell-injection risk.
// If workDir is non-empty it is passed to tmux via -c, so the child process
// starts in that directory instead of the parent's cwd.
func (m *TerminalManager) tmuxNewSessionArgv(name string, argv []string, cols, rows uint16, workDir string) error {
	if len(argv) == 0 {
		return fmt.Errorf("tmuxNewSessionArgv: argv must not be empty")
	}
	args := []string{
		"new-session", "-d",
		"-s", name,
		"-x", fmt.Sprintf("%d", cols),
		"-y", fmt.Sprintf("%d", rows),
	}
	if workDir != "" {
		args = append(args, "-c", workDir)
	}
	args = append(args, argv...)
	if err := m.tmuxCmd(args...).Run(); err != nil {
		return err
	}
	m.applySessionOptions(name)
	return nil
}

// SpawnInDir is like Spawn but starts the tmux session in the given working
// directory via tmux -c. If workDir is empty, the session inherits the loom
// service's cwd (same as Spawn). Idempotent: if a session with this name
// already exists, returns (false, nil).
//
// Used by the terminal spawn handler to land new "+ Tab" sessions in the
// active workspace's path rather than the loom service's cwd. wsID must be
// non-empty.
func (m *TerminalManager) SpawnInDir(wsID, name, command string, cols, rows uint16, workDir string) (bool, error) {
	if wsID == "" {
		return false, fmt.Errorf("wsID must not be empty")
	}
	if !validSessionName.MatchString(name) {
		return false, fmt.Errorf("invalid session name %q: must match [a-zA-Z0-9_-]+", name)
	}
	if command == "" {
		return false, fmt.Errorf("command must not be empty")
	}
	if cols == 0 {
		cols = m.defaultCols
	}
	if rows == 0 {
		rows = m.defaultRows
	}

	internalName := m.tmuxName(wsID, name)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tmuxHasSession(internalName) {
		return false, nil
	}

	// Pass as single-element argv so tmux execs the command directly with the
	// cwd set via -c. For single-binary commands (claude, /bin/bash, etc.) this
	// is behaviorally identical to Spawn but with workDir support.
	if err := m.tmuxNewSessionArgv(internalName, []string{command}, cols, rows, workDir); err != nil {
		return false, fmt.Errorf("tmux new-session: %w", err)
	}
	return true, nil
}

// SpawnArgv creates a tmux session in the given workspace whose command is
// execv'd directly from argv, bypassing shell interpretation. Use this for
// commands containing user-supplied text (e.g. an initial agent prompt) to
// avoid shell-injection risks. If workDir is non-empty, the session starts in
// that directory (via tmux -c); otherwise it inherits the loom service's cwd.
// Idempotent: if the session already exists it returns (false, nil). wsID
// must be non-empty.
func (m *TerminalManager) SpawnArgv(wsID, name string, argv []string, cols, rows uint16, workDir string) (bool, error) {
	if wsID == "" {
		return false, fmt.Errorf("wsID must not be empty")
	}
	if !validSessionName.MatchString(name) {
		return false, fmt.Errorf("invalid session name %q: must match [a-zA-Z0-9_-]+", name)
	}
	if len(argv) == 0 {
		return false, fmt.Errorf("argv must not be empty")
	}
	if cols == 0 {
		cols = m.defaultCols
	}
	if rows == 0 {
		rows = m.defaultRows
	}

	internalName := m.tmuxName(wsID, name)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.tmuxHasSession(internalName) {
		return false, nil
	}

	if err := m.tmuxNewSessionArgv(internalName, argv, cols, rows, workDir); err != nil {
		return false, fmt.Errorf("tmux new-session: %w", err)
	}
	return true, nil
}
