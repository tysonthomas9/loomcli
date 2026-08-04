// Package backends implements the agent backends — the AI CLIs loom drives
// (claude, codex, cursor, gemini, opencode, plus echo/external test doubles) —
// registering each with cli.RegisterBackend at init, running them under the
// harness-wrapper supervisor, and classifying their failures as InvocationError.
// Reached through the cli.Backend registry by internal/cli/agent, automode,
// backendcheck and runtimepreflight. Not internal/backend, the issue backend.
package backends
