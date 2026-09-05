package cireport_test

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

func TestLoomReleaseCannotBypassPairedCompatibility(t *testing.T) {
	registry.MarkEvidence(t, 80)
	release := readReleaseWorkflow(t)
	compatibility, ok := release.Jobs["skills-compatibility"]
	if !ok {
		t.Fatal("release workflow has no skills-compatibility job")
	}
	if compatibility.Uses != "./.github/workflows/skills-compatibility.yml" {
		t.Fatalf("release compatibility workflow = %q", compatibility.Uses)
	}
	if compatibility.With["loom_ref"] != "${{ github.sha }}" || compatibility.With["fleetdb_ref"] != "main" {
		t.Fatalf("release compatibility pair = %#v", compatibility.With)
	}
	releaseJob, ok := release.Jobs["goreleaser"]
	if !ok {
		t.Fatal("release workflow has no goreleaser job")
	}
	if releaseJob.Needs != "skills-compatibility" {
		t.Fatalf("goreleaser needs = %q, want skills-compatibility", releaseJob.Needs)
	}
}

type releaseWorkflow struct {
	Jobs map[string]releaseJob `yaml:"jobs"`
}

type releaseJob struct {
	Uses  string         `yaml:"uses"`
	Needs string         `yaml:"needs"`
	With  map[string]any `yaml:"with"`
}

func readReleaseWorkflow(t *testing.T) releaseWorkflow {
	t.Helper()
	path := filepath.Join("..", "..", ".github", "workflows", "release.yml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var workflow releaseWorkflow
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow
}
