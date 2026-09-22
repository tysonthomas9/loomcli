// Package preflighttest provides shared assertions for local task runner
// preflight gate tests.
package preflighttest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/runtimepreflight"
)

// GateParityFixture is the canonical readiness verdict exercised by automatic
// gate consumers.
type GateParityFixture struct {
	Workspace  string                        `json:"workspace"`
	Backend    string                        `json:"backend"`
	Health     runtimepreflight.HealthStatus `json:"health"`
	ErrorClass runtimepreflight.ErrorClass   `json:"error_class"`
	Message    string                        `json:"message"`
}

// LoadGateParityFixture loads the single canonical automatic-gate fixture.
func LoadGateParityFixture(t testing.TB) GateParityFixture {
	t.Helper()
	data, err := os.ReadFile(gateParityFixturePath(t))
	if err != nil {
		t.Fatalf("read gate parity fixture: %v", err)
	}
	var fixture GateParityFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode gate parity fixture: %v", err)
	}
	return fixture
}

// AssertGateParityResult verifies every verdict field carried by the canonical
// fixture.
func AssertGateParityResult(t testing.TB, result runtimepreflight.Result, fixture GateParityFixture) {
	t.Helper()
	if result.Backend != fixture.Backend || result.Health == nil || *result.Health != fixture.Health ||
		result.ErrorClass != fixture.ErrorClass || result.Message != fixture.Message {
		t.Fatalf("gate result = %+v, want backend:%q health:%+v class:%q message:%q", result, fixture.Backend, fixture.Health, fixture.ErrorClass, fixture.Message)
	}
}

// AssertGateParityError verifies a gate returned the canonical typed not-ready
// verdict.
func AssertGateParityError(t testing.TB, err error, fixture GateParityFixture) {
	t.Helper()
	var notReady *runtimepreflight.NotReadyError
	if !errors.As(err, &notReady) {
		t.Fatalf("gate error = %T %v, want *runtimepreflight.NotReadyError", err, err)
	}
	AssertGateParityResult(t, notReady.Result, fixture)
}

func gateParityFixturePath(t testing.TB) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve gate parity fixture path")
	}
	return filepath.Join(filepath.Dir(filename), "..", "testdata", "gate-parity.json")
}
