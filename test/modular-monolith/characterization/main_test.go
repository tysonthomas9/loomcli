package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestCheckedInManifestIsValid(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	if _, err := loadManifest(root + "/" + defaultManifestPath); err != nil {
		t.Fatalf("load checked-in manifest: %v", err)
	}
}

func TestCheckedInPhase6ParityManifestIsValid(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	if _, err := loadManifest(root + "/test/modular-monolith/phase6-parity-matrix.yaml"); err != nil {
		t.Fatalf("load checked-in Phase 6 manifest: %v", err)
	}
}

func TestDecodeManifestRejectsUnknownField(t *testing.T) {
	data := validManifestYAML() + "unknown: true\n"
	if _, err := decodeManifest([]byte(data)); err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("decodeManifest error = %v, want unknown-field rejection", err)
	}
}

func TestValidateManifestRequiresAuthoritativeRowsInOrder(t *testing.T) {
	m, err := decodeManifest([]byte(validManifestYAML()))
	if err != nil {
		t.Fatalf("decode valid manifest: %v", err)
	}
	m.Rows[0], m.Rows[1] = m.Rows[1], m.Rows[0]
	if err := validateManifest(m); err == nil || !strings.Contains(err.Error(), "rows[0].id") {
		t.Fatalf("validateManifest error = %v, want authoritative-order rejection", err)
	}
}

func TestCompareDiscoveredTestsRejectsSelectionDrift(t *testing.T) {
	err := compareDiscoveredTests([]string{"TestOne", "TestTwo"}, []string{"TestOne", "TestThree"})
	if err == nil || !strings.Contains(err.Error(), "selected test set drifted") {
		t.Fatalf("compareDiscoveredTests error = %v, want drift rejection", err)
	}
}

func TestRenderAndValidateTestOutputRequiresRunAndPass(t *testing.T) {
	output := strings.Join([]string{
		`{"Action":"run","Test":"TestOne"}`,
		`{"Action":"output","Test":"TestOne","Output":"proof line\n"}`,
		`{"Action":"pass","Test":"TestOne"}`,
	}, "\n")
	var rendered bytes.Buffer
	if err := renderAndValidateTestOutput(&rendered, []byte(output), []string{"TestOne"}); err != nil {
		t.Fatalf("renderAndValidateTestOutput: %v", err)
	}
	if got := rendered.String(); got != "proof line\n" {
		t.Fatalf("rendered output = %q", got)
	}
}

func TestRenderAndValidateTestOutputRejectsSkip(t *testing.T) {
	output := strings.Join([]string{
		`{"Action":"run","Test":"TestOne"}`,
		`{"Action":"skip","Test":"TestOne"}`,
	}, "\n")
	if err := renderAndValidateTestOutput(&bytes.Buffer{}, []byte(output), []string{"TestOne"}); err == nil || !strings.Contains(err.Error(), "was skipped") {
		t.Fatalf("renderAndValidateTestOutput error = %v, want skip rejection", err)
	}
}

func TestRenderAndValidateTestOutputPropagatesWriterFailure(t *testing.T) {
	output := []byte(`{"Action":"output","Test":"TestOne","Output":"proof line\n"}`)
	if err := renderAndValidateTestOutput(failingWriter{}, output, []string{"TestOne"}); err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("renderAndValidateTestOutput error = %v, want writer failure", err)
	}
}

func validManifestYAML() string {
	var b strings.Builder
	b.WriteString("schema_version: 1\n")
	b.WriteString("suite: modular-monolith-phase1-characterization\n")
	b.WriteString("description: test manifest\n")
	b.WriteString("rows:\n")
	for _, id := range requiredRowsBySuite["modular-monolith-phase1-characterization"] {
		b.WriteString("  - id: " + id + "\n")
		b.WriteString("    package: ./internal/example\n")
		b.WriteString("    test_regex: '^TestExample$'\n")
		b.WriteString("    expected_tests: [TestExample]\n")
		b.WriteString("    timeout: 1m\n")
		b.WriteString("    expected_behavior: [Behavior remains stable.]\n")
	}
	return b.String()
}
