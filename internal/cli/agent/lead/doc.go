// Package lead runs the lead agent's interactive session.
//
// A lead is the operator-facing agent: it holds a conversation rather than
// executing a single task, so it is invoked interactively against a worktree
// and keeps its harness profile across invocations. profile.go resolves that
// per-agent profile; lead.go performs the run, using the workspace's custom
// terminal prompt when one is configured and the built-in lead prompt
// otherwise.
package lead
