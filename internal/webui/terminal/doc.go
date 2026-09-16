// Package terminal provides the web UI's terminal sessions.
//
// Two managers live here, for two different kinds of session. PTYManager owns
// the PTY-backed shells behind the main web terminal, keyed by (workspace,
// session) rather than by the WebSocket attached to them — see pty_manager.go
// for that lifetime model and its detach, grace, and kill rules.
// AgentTmuxManager is the narrower path: it attaches the UI to the long-lived
// tmux sessions CLI auto-mode creates for agents, and cleans them up when a
// workspace is deleted.
//
// The split exists because ownership differs. A web terminal's shell is
// created by, and belongs to, the server; an auto-mode agent's tmux session
// already exists and is merely observed.
package terminal
