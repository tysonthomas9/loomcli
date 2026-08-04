// Package leadcontrol runs an interactive lead agent's conversation under Loom
// control: it supervises the agent backend CLI through harness-wrapper (PTY
// passthrough) or the codex app-server websocket, publishes the runtime's
// endpoint/status metadata on the agent session, resumes the provider thread id
// across restarts, and injects queued agent-inbox and epic-assignment messages
// into the live conversation as turns. Entered by internal/cli/backends' lead
// runtimes; delivery is called by internal/driver's outbox dispatcher, the
// driverapi/prreview web UI handlers, and internal/cli/driver's
// `loom driver deliver-lead-assignment` runtime subcommand.
package leadcontrol
