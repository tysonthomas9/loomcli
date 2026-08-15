package agentprompt

import (
	"strings"
	"testing"
)

func TestTemplateSourceReturnsRawBuiltinTemplate(t *testing.T) {
	for _, name := range []string{"planning", "task"} {
		t.Run(name, func(t *testing.T) {
			source, err := TemplateSource(name)
			if err != nil {
				t.Fatalf("TemplateSource: %v", err)
			}
			if !strings.Contains(source, "{{ .AgentName }}") {
				t.Fatalf("%s source does not contain its template fields", name)
			}
		})
	}
}

func TestTemplateSourceReturnsErrorsInsteadOfPanicking(t *testing.T) {
	for _, name := range []string{"", "missing", "../planning", "planning.md"} {
		t.Run(name, func(t *testing.T) {
			if _, err := TemplateSource(name); err == nil {
				t.Fatalf("TemplateSource(%q) error = nil", name)
			}
		})
	}
}
