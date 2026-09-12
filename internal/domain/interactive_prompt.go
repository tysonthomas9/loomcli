package domain

// BuiltinInteractivePrompt is a selectable built-in terminal-agent prompt.
type BuiltinInteractivePrompt struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Hidden bool   `json:"-"`
}

// ordered; ID must match an embedded internal/cli/agent/prompts/<ID>.md
var builtinInteractivePrompts = []BuiltinInteractivePrompt{
	{ID: "lead", Label: "Lead"},
	{ID: "pr-review", Label: "PR Review"},
	{ID: "pr-review-checkout", Label: "PR Review (checkout)", Hidden: true},
	{ID: "lead-profile", Label: "Lead (profile)", Hidden: true},
	// Suppresses the argv persona entirely: the role instructions are expected
	// to reach the model as ambient profile context instead. Backed by a 0-byte
	// prompts/none.md, which must exist because loadTemplate panics on a
	// missing embedded template - see BuiltinPromptNone.
	{ID: "none", Label: "None (profile only)", Hidden: true},
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
