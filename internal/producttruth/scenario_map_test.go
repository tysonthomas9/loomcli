package producttruth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateScenarioMapRepository(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	got := ValidateScenarioMap(root, "tests/aft/coverage/scenario-map.yaml")
	if len(got.Errors) != 0 {
		t.Fatalf("scenario map errors:\n%s", FormatScenarioMap(got))
	}
}

func TestValidateScenarioMapResolvesKnownFailureSelectorSource(t *testing.T) {
	root := t.TempDir()
	writeScenarioMapTestFile(t, root, "stories.tsv", "Story ID\tArea\nS-1\tWork\n")
	writeScenarioMapTestFile(t, root, "graph/flow.graph.yaml", "kind: graph\nversion: 1\nimports: [states.yaml]\n")
	writeScenarioMapTestFile(t, root, "graph/states.yaml", "kind: graph-states\nversion: 1\nstates:\n  done:\n    intent: expected failure\n")
	writeScenarioMapTestFile(t, root, "map.yaml", validScenarioMapFixture)

	got := ValidateScenarioMap(root, "map.yaml")
	if len(got.Errors) != 0 {
		t.Fatalf("scenario map errors:\n%s", FormatScenarioMap(got))
	}
}

func TestValidateScenarioMapRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	writeScenarioMapTestFile(t, root, "map.yaml", "kind: aft-scenario-map\nversion: 1\nunexpected: true\n")

	got := ValidateScenarioMap(root, "map.yaml")
	if len(got.Errors) != 1 || !strings.Contains(got.Errors[0], "field unexpected not found") {
		t.Fatalf("errors = %#v, want strict unknown-field error", got.Errors)
	}
}

func TestValidateScenarioMapReportsBrokenReferencesAndInvariants(t *testing.T) {
	root := t.TempDir()
	writeScenarioMapTestFile(t, root, "stories.tsv", "Story ID\tArea\nS-1\tWork\n")
	writeScenarioMapTestFile(t, root, "map.yaml", `kind: aft-scenario-map
version: 1
scope:
  executions: 2
  productExecutions: 1
  surfaceContractExecutions: 0
  observedBaseline: {passed: 0, failed: 0}
storyAudit:
  registry: stories.tsv
  unannotated:
    - {story: S-404, disposition: missing, gap: GAP-404}
families:
  - id: duplicate
    status: covered
    stories: [S-404]
    scenarios: [missing.test.yaml]
    missing:
      - id: GAP-1
        status: missing
        priority: P9
        code: missing.ts
        scenario: missing behavior
  - id: duplicate
    status: unknown
`)

	got := ValidateScenarioMap(root, "map.yaml")
	joined := strings.Join(got.Errors, "\n")
	for _, want := range []string{
		"productExecutions + surfaceContractExecutions must equal executions",
		"observed passed + failed must equal executions",
		"covered family must not declare a missing or implementation gap",
		"duplicate family id",
		"unknown status \"unknown\"",
		"unknown story S-404",
		"unknown gap GAP-404",
		"priority must be P0, P1, P2, or P3",
		"missing.test.yaml",
		"missing.ts",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors do not contain %q:\n%s", want, joined)
		}
	}
}

func TestValidateScenarioMapRejectsUnrelatedSelectorSource(t *testing.T) {
	root := t.TempDir()
	writeScenarioMapTestFile(t, root, "stories.tsv", "Story ID\tArea\nS-1\tWork\n")
	writeScenarioMapTestFile(t, root, "graph/flow.graph.yaml", "kind: graph\nversion: 1\nimports: [states.yaml]\n")
	writeScenarioMapTestFile(t, root, "graph/states.yaml", "kind: graph-states\nversion: 1\n")
	writeScenarioMapTestFile(t, root, "other.yaml", "intent: expected failure\n")
	fixture := strings.Replace(validScenarioMapFixture, "graph/states.yaml", "other.yaml", 1)
	writeScenarioMapTestFile(t, root, "map.yaml", fixture)

	got := ValidateScenarioMap(root, "map.yaml")
	joined := strings.Join(got.Errors, "\n")
	if !strings.Contains(joined, "selectorSource is not the source or one of its graph imports") {
		t.Fatalf("errors = %#v, want unrelated selector source error", got.Errors)
	}
}

func writeScenarioMapTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

const validScenarioMapFixture = `kind: aft-scenario-map
version: 1
scope:
  executions: 1
  productExecutions: 1
  surfaceContractExecutions: 0
  observedBaseline: {passed: 1, failed: 0}
storyAudit:
  registry: stories.tsv
  unannotated:
    - {story: S-1, disposition: missing, gap: GAP-1}
families:
  - id: example
    status: partial
    stories: [S-1]
    scenarios: [graph/flow.graph.yaml]
    missing:
      - id: GAP-1
        status: missing
        priority: P1
        scenario: missing behavior
knownFailures:
  - source: graph/flow.graph.yaml
    selectorSource: graph/states.yaml
    scenario: expected failure
`
