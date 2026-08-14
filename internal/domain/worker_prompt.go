package domain

import "strings"

// BuiltinPromptPrefix marks a prompt_file value as a reference to a prompt that
// ships inside loom rather than a path on disk. It is the same prefix the
// interactive terminal prompts already use, kept here so both the daemon and
// the CLI can recognize a reference without re-spelling the literal.
const BuiltinPromptPrefix = "builtin:"

// BuiltinWorkerPrompt is a built-in prompt an autonomous (worker) agent role
// can name in its prompt_file.
type BuiltinWorkerPrompt struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// builtinWorkerPrompts is ordered; every ID must match an embedded
// internal/cli/agent/prompts/<ID>.md.
//
// This is deliberately a SIBLING of builtinInteractivePrompts rather than
// hidden entries inside it: the interactive list is served to the
// interactive-prompt picker, and offering a worker prompt there — a body that
// claims a task, works it, and exits — would be wrong for a terminal agent.
// The two lists never overlap, and an ID from one is not resolvable through the
// other.
var builtinWorkerPrompts = []BuiltinWorkerPrompt{
	{ID: "team-architect", Label: "Team: Architect"},
	{ID: "team-web-designer", Label: "Team: Web Designer"},
	{ID: "team-frontend-dev", Label: "Team: Frontend Developer"},
	{ID: "team-backend-dev", Label: "Team: Backend Developer"},
	{ID: "team-content-writer", Label: "Team: Content Writer"},
	{ID: "team-researcher", Label: "Team: Researcher"},
	{ID: "team-agent-dev", Label: "Team: Agent Developer"},
	{ID: "team-eval-engineer", Label: "Team: Eval Engineer"},
	{ID: "team-qa", Label: "Team: QA Engineer"},
	{ID: "team-data-engineer", Label: "Team: Data Engineer"},
}

// BuiltinWorkerPrompts returns the built-in worker agent-role prompts in
// registration order.
func BuiltinWorkerPrompts() []BuiltinWorkerPrompt {
	return append([]BuiltinWorkerPrompt(nil), builtinWorkerPrompts...)
}

// IsBuiltinWorkerPrompt reports whether id is a registered worker prompt.
func IsBuiltinWorkerPrompt(id string) bool {
	for _, prompt := range builtinWorkerPrompts {
		if prompt.ID == id {
			return true
		}
	}
	return false
}

// ParseBuiltinPromptRef splits a prompt_file value into its built-in prompt ID.
// ok is false when the value is an ordinary path, which is what keeps every
// caller's existing filesystem handling intact.
//
// The returned ID is trimmed but NOT validated — a caller that needs to know
// whether the ID resolves asks IsBuiltinWorkerPrompt (or, for terminal agents,
// IsBuiltinInteractivePrompt), so each call site can phrase its own error.
func ParseBuiltinPromptRef(value string) (id string, ok bool) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, BuiltinPromptPrefix) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, BuiltinPromptPrefix)), true
}
