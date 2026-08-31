package runtimepreflight_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/fleetdbcap"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/runtimepreflight"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// testRequirements mirrors the shape of the real manifest — several Required
// entries plus the one Degrades entry — without pinning the test to whatever
// the production manifest happens to contain today.
func testRequirements() []fleetdbcap.Requirement {
	return []fleetdbcap.Requirement{
		{
			Capability: "issues",
			Feature:    "task claim and status transitions",
			Level:      fleetdbcap.Required,
			Route:      "GET  /api/v1/{workspace}/issues",
		},
		{
			Capability: "skills",
			Feature:    "skill materialization",
			Level:      fleetdbcap.Required,
			Route:      "GET  /api/v1/{workspace}/skills",
		},
		{
			Capability:     "skill-materialization-leases",
			Feature:        "concurrent skill materialization",
			Level:          fleetdbcap.Degrades,
			Route:          "POST /api/v1/{workspace}/skill-materialization-leases",
			DegradedEffect: "materialization runs unlocked; concurrent spawns on one host may redo work",
		},
	}
}

// capsServing starts a fleet-db stand-in advertising exactly names, and
// returns a CapabilityStore pointed at it.
func capsServing(t *testing.T, commit string, names ...string) store.CapabilityStore {
	t.Helper()
	return capsFrom(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"api_version": 3, "commit": commit, "capabilities": names,
		})
	})
}

func capsFrom(t *testing.T, h http.HandlerFunc) store.CapabilityStore {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := fleetdb.New(fleetdb.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("fleetdb.New: %v", err)
	}
	return c.Capabilities()
}

func check(t *testing.T, caps store.CapabilityStore, mode runtimepreflight.Mode) runtimepreflight.Report {
	t.Helper()
	return runtimepreflight.CheckFleetDB(context.Background(), caps, testRequirements(), mode)
}

func TestCheckFleetDBAllPresentIsCompatible(t *testing.T) {
	caps := capsServing(t, "adca220cdce0", "issues", "skills", "skill-materialization-leases")
	got := check(t, caps, runtimepreflight.ModeWarn)

	if got.Outcome != runtimepreflight.OutcomeCompatible {
		t.Fatalf("outcome = %v, want compatible (err=%v)", got.Outcome, got.Err)
	}
	if got.Fatal() {
		t.Fatal("a compatible report must not be fatal")
	}
	if msg := got.Message(); msg != "" {
		t.Fatalf("a clean run must print nothing, got %q", msg)
	}
}

func TestCheckFleetDBUnknownExtraCapabilitiesStayCompatible(t *testing.T) {
	// A fleet-db newer than loom is always compatible: subset, never equality.
	caps := capsServing(t, "newer", "issues", "skills", "skill-materialization-leases",
		"quantum-tunneling", "time-travel")
	if got := check(t, caps, runtimepreflight.ModeWarn); got.Outcome != runtimepreflight.OutcomeCompatible {
		t.Fatalf("outcome = %v, want compatible", got.Outcome)
	}
}

func TestCheckFleetDBMissingRequiredIsIncompatible(t *testing.T) {
	t.Setenv("LOOM_BUILD_SHA", "a74c7e18e")
	caps := capsServing(t, "adca220cdce0", "issues", "skill-materialization-leases")

	got := check(t, caps, runtimepreflight.ModeWarn)
	if got.Outcome != runtimepreflight.OutcomeIncompatible {
		t.Fatalf("outcome = %v, want incompatible", got.Outcome)
	}
	if !got.Fatal() {
		t.Fatal("a missing required capability must be fatal")
	}
	msg := got.Message()
	for _, want := range []string{
		"skills",
		"GET  /api/v1/{workspace}/skills",
		"adca220cdce0",
		"a74c7e18e",
		"Missing, required:",
		"LOOM_FLEETDB_PREFLIGHT=off",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not name %q:\n%s", want, msg)
		}
	}
}

func TestCheckFleetDBMissingOnlyLeasesIsDegraded(t *testing.T) {
	caps := capsServing(t, "adca220cdce0", "issues", "skills")

	got := check(t, caps, runtimepreflight.ModeWarn)
	if got.Outcome != runtimepreflight.OutcomeDegraded {
		t.Fatalf("outcome = %v, want degraded", got.Outcome)
	}
	if got.Fatal() {
		t.Fatal("a degraded report must not be fatal")
	}
	if len(got.Missing) != 1 || got.Missing[0].Capability != "skill-materialization-leases" {
		t.Fatalf("unexpected Missing: %+v", got.Missing)
	}
	msg := got.Message()
	if !strings.Contains(msg, "materialization runs unlocked") {
		t.Errorf("message does not name the degraded effect:\n%s", msg)
	}
	lines := got.BannerLines()
	if len(lines) == 0 || lines[0] != "Degraded:" {
		t.Errorf("banner lines do not open with a Degraded heading: %v", lines)
	}
}

func TestCheckFleetDBDialRefusedIsUnreachableNotIncompatible(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c, err := fleetdb.New(fleetdb.Config{BaseURL: url})
	if err != nil {
		t.Fatalf("fleetdb.New: %v", err)
	}

	got := check(t, c.Capabilities(), runtimepreflight.ModeWarn)
	if got.Outcome != runtimepreflight.OutcomeUnreachable {
		t.Fatalf("outcome = %v, want unreachable", got.Outcome)
	}
	if got.Outcome == runtimepreflight.OutcomeIncompatible {
		t.Fatal("unreachable must be a distinct outcome from incompatible")
	}
	if got.Fatal() {
		t.Fatal("a closed port must not refuse boot")
	}
	if !strings.Contains(got.Message(), "reason=unreachable") {
		t.Errorf("message must be distinguishable by reason:\n%s", got.Message())
	}
}

func TestCheckFleetDBTimeoutIsUnreachable(t *testing.T) {
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	caps := capsFrom(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-blocked:
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	})

	// CheckFleetDB bounds the probe at FleetDBPreflightTimeout; a parent
	// deadline shorter than that wins, which is what keeps this test fast.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	got := runtimepreflight.CheckFleetDB(ctx, caps, testRequirements(), runtimepreflight.ModeWarn)
	if got.Outcome != runtimepreflight.OutcomeUnreachable {
		t.Fatalf("outcome = %v (err=%v), want unreachable", got.Outcome, got.Err)
	}
	if got.Fatal() {
		t.Fatal("a hung fleet-db must not refuse boot")
	}
}

func TestCheckFleetDBCapabilityEndpoint404WarnIsUnverified(t *testing.T) {
	caps := capsFrom(t, http.NotFound)

	got := check(t, caps, runtimepreflight.ModeWarn)
	if got.Outcome != runtimepreflight.OutcomeUnverified {
		t.Fatalf("outcome = %v, want unverified", got.Outcome)
	}
	if got.Fatal() {
		t.Fatal("an old fleet-db must not refuse boot under the warn default")
	}
	if !strings.Contains(got.Message(), "predates capability reporting") {
		t.Errorf("unexpected message:\n%s", got.Message())
	}
}

func TestCheckFleetDBCapabilityEndpoint404StrictIsIncompatible(t *testing.T) {
	caps := capsFrom(t, http.NotFound)

	got := check(t, caps, runtimepreflight.ModeStrict)
	if got.Outcome != runtimepreflight.OutcomeIncompatible {
		t.Fatalf("outcome = %v, want incompatible", got.Outcome)
	}
	if !got.Fatal() {
		t.Fatal("strict mode must refuse an unverifiable fleet-db")
	}
	if !strings.Contains(got.Message(), "reason=incompatible") {
		t.Errorf("unexpected message:\n%s", got.Message())
	}
}

func TestCheckFleetDBMalformedBodyIsUnreachable(t *testing.T) {
	// The trap this guards: an unparseable 200 must never decode into "the
	// server advertises nothing", which would read as every route missing.
	for name, body := range map[string]string{
		"truncated": `{"api_version":1,`,
		"html":      `<html>502 bad gateway</html>`,
		"empty":     ``,
		"no field":  `{"api_version":1,"commit":"abc"}`,
	} {
		t.Run(name, func(t *testing.T) {
			caps := capsFrom(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			})
			got := check(t, caps, runtimepreflight.ModeWarn)
			if got.Outcome != runtimepreflight.OutcomeUnreachable {
				t.Fatalf("outcome = %v (err=%v), want unreachable", got.Outcome, got.Err)
			}
		})
	}
}

func TestCheckFleetDBOffSkipsEntirely(t *testing.T) {
	caps := capsFrom(t, func(http.ResponseWriter, *http.Request) {
		t.Error("preflight must not call fleet-db when disabled")
	})
	got := check(t, caps, runtimepreflight.ModeOff)
	if got.Outcome != runtimepreflight.OutcomeSkipped || got.Fatal() {
		t.Fatalf("outcome = %v, want skipped and non-fatal", got.Outcome)
	}
	if got.Message() != "" || got.BannerLines() != nil {
		t.Error("a skipped preflight must print nothing")
	}
}

func TestCheckFleetDBNilStoreIsSkipped(t *testing.T) {
	got := runtimepreflight.CheckFleetDB(context.Background(), nil, testRequirements(), runtimepreflight.ModeWarn)
	if got.Outcome != runtimepreflight.OutcomeSkipped || got.Fatal() {
		t.Fatalf("outcome = %v, want skipped and non-fatal", got.Outcome)
	}
}

func TestCheckFleetDBAPIVersionMismatchAloneIsCompatible(t *testing.T) {
	// api_version is for humans; the capability set is authoritative.
	caps := capsFrom(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"api_version": 99, "commit": "future",
			"capabilities": []string{"issues", "skills", "skill-materialization-leases"},
		})
	})
	got := check(t, caps, runtimepreflight.ModeWarn)
	if got.Outcome != runtimepreflight.OutcomeCompatible {
		t.Fatalf("outcome = %v, want compatible", got.Outcome)
	}
	if got.FleetDBAPIVersion != 99 {
		t.Errorf("api_version not carried into the report: %d", got.FleetDBAPIVersion)
	}
}

func TestModeFromEnv(t *testing.T) {
	// A slice, not a map: one case deliberately carries surrounding
	// whitespace, which ModeFromEnv must trim.
	cases := []struct {
		name string
		env  string
		want runtimepreflight.Mode
	}{
		{name: "unset", env: "", want: runtimepreflight.ModeWarn},
		{name: "warn", env: "warn", want: runtimepreflight.ModeWarn},
		{name: "strict", env: "strict", want: runtimepreflight.ModeStrict},
		{name: "uppercase", env: "STRICT", want: runtimepreflight.ModeStrict},
		{name: "padded", env: " off ", want: runtimepreflight.ModeOff},
		{name: "unrecognized", env: "nonsense", want: runtimepreflight.ModeWarn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(runtimepreflight.PreflightModeEnvVar, tc.env)
			if got := runtimepreflight.ModeFromEnv(); got != tc.want {
				t.Fatalf("ModeFromEnv() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReportLogAttrsCarryReason(t *testing.T) {
	caps := capsServing(t, "abc", "issues")
	got := check(t, caps, runtimepreflight.ModeWarn)
	attrs := got.LogAttrs()
	if len(attrs) < 2 || attrs[0] != "reason" || attrs[1] != string(runtimepreflight.OutcomeIncompatible) {
		t.Fatalf("LogAttrs must lead with the reason: %v", attrs)
	}
}
