package registry

import (
	"strings"
	"testing"
)

func TestParseGoTestJSONPromotesOnlyPassingMarkers(t *testing.T) {
	t.Parallel()
	input := strings.Join([]string{
		goTestEvent("output", "example/one", "TestPass", EvidenceMarker(5, "TestPass")),
		goTestEvent("pass", "example/one", "TestPass", ""),
		goTestEvent("output", "example/one", "TestFail", EvidenceMarker(6, "TestFail")),
		goTestEvent("fail", "example/one", "TestFail", ""),
	}, "\n")
	report, err := ParseGoTestJSON(strings.NewReader(input), RepositoryLoom, "loom-sha", BackendRedis, ProviderMinIO)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Evidence) != 1 || report.Evidence[0].ID != 5 {
		t.Fatalf("evidence = %#v, want only passing case 5", report.Evidence)
	}
	if got := report.Evidence[0]; got.Package != "example/one" || got.Test != "TestPass" || got.Backend != BackendRedis || got.Provider != ProviderMinIO {
		t.Fatalf("evidence coordinates = %#v", got)
	}
}

func TestParseGoTestJSONKeysPendingMarkersByPackageAndTest(t *testing.T) {
	t.Parallel()
	input := strings.Join([]string{
		goTestEvent("output", "example/one", "TestSame", EvidenceMarker(5, "TestSame")),
		goTestEvent("output", "example/two", "TestSame", EvidenceMarker(6, "TestSame")),
		goTestEvent("pass", "example/two", "TestSame", ""),
		goTestEvent("fail", "example/one", "TestSame", ""),
	}, "\n")
	report, err := ParseGoTestJSON(strings.NewReader(input), RepositoryLoom, "loom-sha", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Evidence) != 1 || report.Evidence[0].ID != 6 || report.Evidence[0].Package != "example/two" {
		t.Fatalf("evidence = %#v", report.Evidence)
	}
}

func TestParseGoTestJSONRejectsMarkerTestMismatchAndDuplicates(t *testing.T) {
	t.Parallel()
	t.Run("test mismatch", func(t *testing.T) {
		input := goTestEvent("output", "example/one", "TestActual", EvidenceMarker(5, "TestOther"))
		_, err := ParseGoTestJSON(strings.NewReader(input), RepositoryLoom, "sha", "", "")
		if err == nil || !strings.Contains(err.Error(), "marker test") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("duplicate", func(t *testing.T) {
		marker := EvidenceMarker(5, "TestActual")
		input := strings.Join([]string{goTestEvent("output", "example/one", "TestActual", marker), goTestEvent("output", "example/one", "TestActual", marker)}, "\n")
		_, err := ParseGoTestJSON(strings.NewReader(input), RepositoryLoom, "sha", "", "")
		if err == nil || !strings.Contains(err.Error(), "duplicate marker") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("subtest", func(t *testing.T) {
		input := goTestEvent("output", "example/one", "TestActual/child", EvidenceMarker(5, "TestActual/child"))
		_, err := ParseGoTestJSON(strings.NewReader(input), RepositoryLoom, "sha", "", "")
		if err == nil || !strings.Contains(err.Error(), "top-level") {
			t.Fatalf("error = %v", err)
		}
	})
}

func goTestEvent(action, pkg, test, output string) string {
	return `{"Action":"` + action + `","Package":"` + pkg + `","Test":"` + test + `","Output":` + quotedJSON(output) + `}`
}

func quotedJSON(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return `"` + value + `"`
}
