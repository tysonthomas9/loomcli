package main

import (
	"strings"
	"testing"
)

func TestExtractWritesV2RuntimeCoordinates(t *testing.T) {
	input := `{"Action":"output","Package":"example/pkg","Test":"TestCase","Output":"LOOM_EDGE_CASE_V2 {\"id\":5,\"test\":\"TestCase\"}"}` + "\n" +
		`{"Action":"pass","Package":"example/pkg","Test":"TestCase"}`
	var output strings.Builder
	err := run([]string{"extract", "-repository=loom", "-revision=abc123", "-backend=redis", "-provider=gcs"}, strings.NewReader(input), &output)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"schema_version": "skills-edge-evidence/v2"`, `"package": "example/pkg"`, `"backend": "redis"`, `"provider": "gcs"`} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output missing %s:\n%s", want, output.String())
		}
	}
}

func TestReadinessFailsClosedForIncompleteReports(t *testing.T) {
	var output strings.Builder
	err := run([]string{"readiness", "../../test/skills-e2e/registry/testdata/fleet-evidence-v2.json"}, strings.NewReader(""), &output)
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("error = %v", err)
	}
	for _, want := range []string{`"ready": false`, `"repository": "fleet"`, `"missing": [`} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output missing %s:\n%s", want, output.String())
		}
	}
}
