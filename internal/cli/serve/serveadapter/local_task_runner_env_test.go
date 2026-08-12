package serveadapter

import (
	"slices"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/localsettings"
)

func TestLocalTaskRunnerEnvProviderProjectsOnlyModel(t *testing.T) {
	dir := t.TempDir()
	settings := localsettings.Default()
	settings.LocalTaskRunner.OpenCodeModel = "opencode/model"
	if err := localsettings.Save(dir, settings); err != nil {
		t.Fatal(err)
	}
	provider := LocalTaskRunnerEnvProvider(dir)
	if provider == nil {
		t.Fatal("provider is nil")
	}
	got := provider([]string{"PATH=/bin", "GITHUB_TOKEN=filtered-before-composition"})
	if !slices.Equal(got, []string{"LOOM_OPENCODE_MODEL=opencode/model"}) {
		t.Fatalf("environment = %v", got)
	}
	if got := provider([]string{"LOOM_OPENCODE_MODEL=explicit"}); got != nil {
		t.Fatalf("explicit model was overwritten: %v", got)
	}
}
