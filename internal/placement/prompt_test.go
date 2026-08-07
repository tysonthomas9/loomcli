package placement

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestLeadPromptTextResolvesInlineThenFile(t *testing.T) {
	if got, err := LeadPromptText(nil); err != nil || got != "" {
		t.Fatalf("LeadPromptText(nil) = %q, %v", got, err)
	}
	if got, err := LeadPromptText(&domain.Role{}); err != nil || got != "" {
		t.Fatalf("LeadPromptText(empty role) = %q, %v", got, err)
	}

	promptFile := filepath.Join(t.TempDir(), "role.md")
	if err := os.WriteFile(promptFile, []byte("file prompt"), 0o600); err != nil {
		t.Fatalf("write prompt file: %v", err)
	}

	// Inline prompt wins even when a prompt file is also configured.
	got, err := LeadPromptText(&domain.Role{Prompt: "inline prompt", PromptFile: promptFile})
	if err != nil || got != "inline prompt" {
		t.Fatalf("LeadPromptText(inline+file) = %q, %v", got, err)
	}

	got, err = LeadPromptText(&domain.Role{PromptFile: promptFile})
	if err != nil || got != "file prompt" {
		t.Fatalf("LeadPromptText(file) = %q, %v", got, err)
	}

	if _, err := LeadPromptText(&domain.Role{PromptFile: filepath.Join(t.TempDir(), "missing.md")}); err == nil {
		t.Fatal("LeadPromptText(missing file) = nil error, want read error")
	}
}
