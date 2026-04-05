package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

// PromptData is the template context for custom prompt files.
type PromptData struct {
	AgentName    string
	WorktreeName string
	Role         string
	TaskID       string
}

// LoadPromptTemplate reads a prompt template file and executes it with the given data.
// Returns the rendered prompt string.
func LoadPromptTemplate(path string, data PromptData) (string, error) {
	content, err := os.ReadFile(path) // #nosec G304 — path from LoadPromptTemplate callers, not user input
	if err != nil {
		return "", fmt.Errorf("reading prompt template %s: %w", path, err)
	}

	tmpl, err := template.New(filepath.Base(path)).Parse(string(content))
	if err != nil {
		return "", fmt.Errorf("parsing prompt template %s: %w", path, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing prompt template %s: %w", path, err)
	}

	return buf.String(), nil
}
