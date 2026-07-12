package domain

// BuiltinInteractivePrompt is a selectable built-in terminal-agent prompt.
type BuiltinInteractivePrompt struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// ordered; ID must match an embedded internal/cli/agent/prompts/<ID>.md
var builtinInteractivePrompts = []BuiltinInteractivePrompt{
	{ID: "lead", Label: "Lead"},
	{ID: "pr-review", Label: "PR Review"},
}

// BuiltinInteractivePrompts returns the built-in interactive terminal prompts.
func BuiltinInteractivePrompts() []BuiltinInteractivePrompt {
	return append([]BuiltinInteractivePrompt(nil), builtinInteractivePrompts...)
}

// IsBuiltinInteractivePrompt reports whether id is registered.
func IsBuiltinInteractivePrompt(id string) bool {
	for _, prompt := range builtinInteractivePrompts {
		if prompt.ID == id {
			return true
		}
	}
	return false
}
