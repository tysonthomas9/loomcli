package registry

import "testing"

func TestRecorderOmitsEvidenceWhenExecutableTestFails(t *testing.T) {
	t.Parallel()
	recorder := coverageRecorder{}
	failed := &recordingTest{name: "TestFailedScenario", failed: true}
	recorder.covers(failed, validScenario())
	failed.runCleanups()
	if scenarios := recorder.snapshot(); len(scenarios) != 0 {
		t.Fatalf("recorded failed scenarios = %d, want 0", len(scenarios))
	}
}

func TestRecorderProducesV2EvidenceFromActualCoordinates(t *testing.T) {
	t.Parallel()
	recorder := coverageRecorder{}
	scenario := validScenario()
	scenario.Test = "TestExactRoundTrip"
	if err := recorder.record(scenario); err != nil {
		t.Fatal(err)
	}
	report, err := recorder.report("loom-sha", BackendRedis, ProviderGCS)
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != EvidenceSchemaVersion || report.Repository != RepositoryLoom || report.Revision != "loom-sha" {
		t.Fatalf("report identity = %#v", report)
	}
	if len(report.Evidence) != 1 {
		t.Fatalf("evidence = %#v", report.Evidence)
	}
	got := report.Evidence[0]
	if got.ID != 1 || got.Package != loomE2EPackage || got.Test != "TestExactRoundTrip" || got.Backend != BackendRedis || got.Provider != ProviderGCS {
		t.Fatalf("evidence = %#v", got)
	}
}

func validScenario() Scenario {
	return Scenario{ID: "exact-round-trip", Behavior: "files round-trip exactly", Cases: []EdgeCase{{ID: 1}}}
}

type recordingTest struct {
	name     string
	failed   bool
	cleanups []func()
}

func (t *recordingTest) Helper()                {}
func (t *recordingTest) Name() string           { return t.name }
func (t *recordingTest) Failed() bool           { return t.failed }
func (t *recordingTest) Cleanup(cleanup func()) { t.cleanups = append(t.cleanups, cleanup) }
func (t *recordingTest) Fatalf(string, ...any)  { panic("unexpected Fatalf") }
func (t *recordingTest) Errorf(string, ...any)  { panic("unexpected Errorf") }
func (t *recordingTest) runCleanups() {
	for index := len(t.cleanups) - 1; index >= 0; index-- {
		t.cleanups[index]()
	}
}
