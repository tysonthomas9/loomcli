// Package terminal serves the browser terminal: the WebSocket attach point and
// the tab state around it.
//
// HandleAgentTerminalWS upgrades a connection and bridges it to the agent's
// tmux-backed PTY. It authenticates with a short-lived terminal token
// (obtained from HandleGetAgentTerminalToken) rather than a session cookie,
// because a WebSocket handshake from the browser cannot carry custom headers.
//
// The remaining handlers persist what the operator sees: terminal tabs and
// their metadata, which sessions belong to an issue, and the lifecycle
// configuration the client honors. That state is stored server-side so a
// reload — or a different browser — restores the same tabs rather than
// dropping the operator into a bare shell.
package terminal
