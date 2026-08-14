package producttruth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateReportsMissingProofDimensions(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "stories.tsv", "Story ID\tArea\nS-1\tWork\n")
	writeTestFile(t, root, "proof_test.go", "func TestProof() {}\n")
	writeTestFile(t, root, "truths.yaml", "version: 1\n"+
		"invariants:\n"+
		"  - id: INV-1\n"+
		"    title: Example\n"+
		"    stories: [S-1]\n"+
		"    source_of_truth: [{system: db, record: row}]\n"+
		"    transition: {from: absent, action: create, to: present}\n"+
		"    ui: {surfaces: [board], expected: card}\n"+
		"    persistence: {query: get, assertions: present}\n"+
		"    failure_retry: {failure: timeout, retry: retry, idempotency: key}\n"+
		"    implementation: {state: enforced}\n"+
		"    proofs:\n"+
		"      - {dimension: contract, kind: go, path: proof_test.go, selector: TestProof}\n")
	got := Validate(root, "truths.yaml", "stories.tsv")
	joined := strings.Join(got.Errors, "\n")
	for _, want := range []string{"missing persistence proof", "missing ui proof", "missing retry proof"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("errors %q do not contain %q", joined, want)
		}
	}
}

func TestValidateRepositoryRegistry(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	got := Validate(root, "docs/qa/product-invariants.yaml", "docs/qa/feature-user-stories.tsv")
	if len(got.Errors) != 0 {
		t.Fatalf("registry errors:\n%s", Format(got))
	}
}

func TestFormatReportsFailClosedCapabilityGap(t *testing.T) {
	result := Result{Registry: Registry{Invariants: []Invariant{{
		ID: "INV-GAP",
		Implementation: Implementation{
			State: "fail_closed",
			Gap:   "external executor is not configured",
		},
	}}}}
	formatted := Format(result)
	if !strings.Contains(formatted, "state=fail_closed") || !strings.Contains(formatted, "GAP: external executor is not configured") {
		t.Fatalf("Format() did not surface implementation gap:\n%s", formatted)
	}
}

func writeTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
