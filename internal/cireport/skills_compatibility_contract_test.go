package cireport_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func loadWorkflow(t *testing.T, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return doc
}

func mapping(t *testing.T, value any, label string) map[string]any {
	t.Helper()
	m, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s is not a mapping", label)
	}
	return m
}

func stringValue(m map[string]any, key string) string { v, _ := m[key].(string); return v }

func TestSkillsCompatibilityWorkflowHasExplicitGates(t *testing.T) {
	doc := loadWorkflow(t, "skills-compatibility.yml")
	jobs := mapping(t, doc["jobs"], "jobs")
	for _, name := range []string{"resolve", "generated-contract", "proof", "gcs-provider", "edge-readiness"} {
		if _, ok := jobs[name]; !ok {
			t.Fatalf("missing compatibility job %q", name)
		}
	}
	resolve := mapping(t, jobs["resolve"], "resolve")
	steps, _ := resolve["steps"].([]any)
	joined := ""
	for _, raw := range steps {
		joined += "\n" + stringValue(mapping(t, raw, "step"), "name")
	}
	for _, want := range []string{"Checkout LoomCLI", "Checkout FleetDB", "Checkout pinned Vercel Agent Skills corpus", "Resolve and record revision pair", "Upload resolved revision evidence"} {
		if !strings.Contains(joined, want) {
			t.Errorf("resolve job missing step %q", want)
		}
	}
	contract := mapping(t, jobs["generated-contract"], "generated-contract")
	if !needsContain(contract["needs"], "resolve") {
		t.Error("generated-contract must need resolve")
	}
	proof := mapping(t, jobs["proof"], "proof")
	if !needsContain(proof["needs"], "resolve") || !needsContain(proof["needs"], "generated-contract") {
		t.Error("proof must wait for resolved revisions and generated contract")
	}
	readiness := mapping(t, jobs["edge-readiness"], "edge-readiness")
	for _, want := range []string{"resolve", "proof", "gcs-provider"} {
		if !needsContain(readiness["needs"], want) {
			t.Errorf("readiness must need %s", want)
		}
	}
}

func needsContain(value any, want string) bool {
	switch v := value.(type) {
	case string:
		return v == want
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

func TestReleaseWorkflowPassesNamedCompatibilitySecrets(t *testing.T) {
	doc := loadWorkflow(t, "release.yml")
	jobs := mapping(t, doc["jobs"], "jobs")
	compat := mapping(t, jobs["skills-compatibility"], "skills-compatibility")
	if stringValue(compat, "uses") != "./.github/workflows/skills-compatibility.yml" {
		t.Fatal("release must use local compatibility workflow")
	}
	secrets := mapping(t, compat["secrets"], "release compatibility secrets")
	for _, name := range []string{"LOOMCLI_TOKEN", "FLEET_DB_TOKEN", "GCS_TEST_BUCKET", "GCS_HMAC_ACCESS_KEY_ID", "GCS_HMAC_SECRET_ACCESS_KEY"} {
		if _, ok := secrets[name]; !ok {
			t.Errorf("missing named secret %s", name)
		}
	}
	if _, inherited := secrets["inherit"]; inherited {
		t.Error("release must not use secrets: inherit")
	}
	if _, ok := jobs["goreleaser"]; !ok {
		t.Fatal("release missing goreleaser job")
	}
}

func TestCompatibilityHarnessRemainsTheBlackBoxE2ESeam(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "scripts", "test-vercel-skills-compat.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, marker := range []string{"VERCEL_SKILLS_REF", "go test -tags=e2e", "SKILLS_EDGE_REVISION", "import_ok", "verify_binary_file"} {
		if !strings.Contains(s, marker) {
			t.Errorf("compatibility harness lost marker %q", marker)
		}
	}
}
