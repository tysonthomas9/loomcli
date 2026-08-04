// Package terminal owns the web UI's PTY layer: shell sessions keyed on
// (workspace, session) that outlive any single WebSocket (PTYManager /
// MultiPTYManager behind the PTYSource interface), the Redis-backed
// tab-metadata and UI-state TerminalService, and the session launch-command
// resolver — including ValidBackends, the accepted agent CLI backend names
// (claude, codex, ...) the terminal dropdown validates against. Consumed by
// internal/webui/handlers/terminal and wired in internal/webui/app;
// agent_tmux.go is the residual attach path for CLI auto-mode tmux sessions.
package terminal
