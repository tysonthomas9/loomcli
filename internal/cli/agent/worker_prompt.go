package agent

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// BuiltinWorkerPrompt is a built-in prompt an autonomous agent role can name in
// its prompt_file.
type BuiltinWorkerPrompt = domain.BuiltinWorkerPrompt

// BuiltinWorkerPrompts returns the built-in worker agent-role prompts.
func BuiltinWorkerPrompts() []BuiltinWorkerPrompt {
	return domain.BuiltinWorkerPrompts()
}

// GenerateWorkerPrompt renders the built-in worker agent-role prompt named by
// id with the given context.
//
// This is the worker counterpart of GenerateTerminalPrompt, and the difference
// between them is the whole point: a terminal prompt renders with
// promptTemplateData (the built-in context, carrying ReadyJSON, TestStep and
// friends), while a worker prompt renders with PromptData — the context every
// other worker prompt already gets, the one that carries TaskID, TaskDetail,
// CheckpointBlock and EpicID. Rendering a worker body through the terminal
// renderer would silently drop all four, so the two renderers stay separate and
// the embedded team-*.md bodies reference PromptData fields only.
func GenerateWorkerPrompt(id string, data PromptData) (string, error) {
	return generateWorkerPromptWith(id, func(promptFieldRefs) PromptData { return data })
}

// generateWorkerPromptWith is GenerateWorkerPrompt with the lazy context build
// the custom-prompt loader uses: build is handed the fields the body actually
// references, so a team prompt that never names {{.TaskDetail}} never pays for
// the issue-backend round trip it would have cost.
func generateWorkerPromptWith(id string, build func(promptFieldRefs) PromptData) (string, error) {
	id = strings.TrimSpace(id)
	if !domain.IsBuiltinWorkerPrompt(id) {
		return "", fmt.Errorf("unknown built-in agent-role prompt %q (known: %s)", id, strings.Join(builtinWorkerPromptIDs(), ", "))
	}

	// loadTemplate gives the team prompts the same per-project override hook
	// (./loom-prompts/<id>.md) the built-in planning/task prompts have.
	content, isOverride, err := loadTemplate(id)
	if err != nil {
		return "", fmt.Errorf("built-in agent-role prompt %q: %w", id, err)
	}

	rendered, err := executePromptTemplate(id, content, build)
	if err == nil {
		return rendered, nil
	}
	if !isOverride {
		return "", err
	}

	// An override that references a field PromptData does not carry executes
	// into an error. Fall back to the shipped body rather than taking the run
	// down over a file the operator can fix later — the same trade renderPrompt
	// makes for the built-in prompts.
	slog.Warn("override prompt execution failed, falling back to the built-in body", "prompt", id, "err", err)
	embedded, embErr := promptFS.ReadFile("prompts/" + id + ".md")
	if embErr != nil {
		return "", fmt.Errorf("built-in agent-role prompt %q not found: %w", id, embErr)
	}
	return executePromptTemplate(id, string(embedded), build)
}

// builtinWorkerPromptIDs lists the registered IDs for error messages.
func builtinWorkerPromptIDs() []string {
	prompts := domain.BuiltinWorkerPrompts()
	ids := make([]string, 0, len(prompts))
	for _, p := range prompts {
		ids = append(ids, p.ID)
	}
	return ids
}
