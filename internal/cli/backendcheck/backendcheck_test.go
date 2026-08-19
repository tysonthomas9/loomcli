package backendcheck

import (
	"testing"

	"github.com/olesho/harness-wrapper/pkg/discovery"
	"github.com/olesho/harness-wrapper/pkg/versions"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

type healthBackend struct {
	name   string
	status backends.HealthStatus
}

func (h *healthBackend) Name() string { return h.name }

func (h *healthBackend) InvokeInteractive(_, _, _ string) error { return nil }

func (h *healthBackend) InvokeNonInteractive(_, _, _ string, _ <-chan struct{}, _ *usage.Collector) error {
	return nil
}

func (h *healthBackend) HealthCheck() backends.HealthStatus { return h.status }

func TestCheckBackendUsesRegisteredBackendHealth(t *testing.T) {
	cli.TestingResetBackendState(t)
	cli.RegisterBackend(&healthBackend{
		name: "localdogfood",
		status: backends.HealthStatus{
			Healthy:   true,
			Installed: true,
			Version:   "1",
			Message:   "ready",
		},
	})

	info, err := CheckBackend("localdogfood")
	if err != nil {
		t.Fatalf("CheckBackend returned error: %v", err)
	}
	if !info.Installed {
		t.Fatalf("Installed = false, want true")
	}
	if info.DetectedVersion != "1" {
		t.Fatalf("DetectedVersion = %q, want %q", info.DetectedVersion, "1")
	}
	if info.InstallHint != "" {
		t.Fatalf("InstallHint = %q, want empty", info.InstallHint)
	}
}

func TestCheckBackendUsesRegisteredBackendMissingMessage(t *testing.T) {
	cli.TestingResetBackendState(t)
	cli.RegisterBackend(&healthBackend{
		name: "gone",
		status: backends.HealthStatus{
			Healthy:   false,
			Installed: false,
			Message:   "binary no longer found",
		},
	})

	info, err := CheckBackend("gone")
	if err != nil {
		t.Fatalf("CheckBackend returned error: %v", err)
	}
	if info.Installed {
		t.Fatalf("Installed = true, want false")
	}
	if info.InstallHint != "binary no longer found" {
		t.Fatalf("InstallHint = %q, want %q", info.InstallHint, "binary no longer found")
	}
}

func TestCheckBackendFallsBackToDiscoveryForUnregisteredName(t *testing.T) {
	cli.TestingResetBackendState(t)

	info, err := CheckBackend("definitely-not-on-path-backendcheck")
	if err != nil {
		t.Fatalf("CheckBackend returned error: %v", err)
	}
	if info.Installed {
		t.Fatalf("Installed = true, want false")
	}
	if info.Binary != "definitely-not-on-path-backendcheck" {
		t.Fatalf("Binary = %q, want raw lookup name", info.Binary)
	}
}

// checkWithHealth registers a fake health-checkable backend under name that
// reports version, then runs it through CheckBackend. The registry is reset
// for the duration of t, so the fake shadows any real backend of that name.
func checkWithHealth(t *testing.T, name, version string) discovery.Info {
	t.Helper()
	cli.TestingResetBackendState(t)
	cli.RegisterBackend(&healthBackend{
		name: name,
		status: backends.HealthStatus{
			Healthy:   true,
			Installed: true,
			Version:   version,
		},
	})
	info, err := CheckBackend(name)
	if err != nil {
		t.Fatalf("CheckBackend(%q) returned error: %v", name, err)
	}
	return info
}

// claudePin returns the upstream pin for claude-code, skipping the test if
// harness-wrapper ever ships it unpinned (which would make drift unobservable
// rather than make the test wrong).
func claudePin(t *testing.T) string {
	t.Helper()
	pin, ok := versions.Pinned("claude-code")
	if !ok {
		t.Skip("claude-code is unpinned upstream; nothing to compare against")
	}
	return pin
}

func TestCheckBackendPopulatesPinMetadata(t *testing.T) {
	info := checkWithHealth(t, "claude", claudePin(t)+" (Claude Code)")

	// The mapping under test: Loom registers the backend as "claude", but
	// versions.json keys the same harness as "claude-code".
	if info.Harness != "claude-code" {
		t.Errorf("Harness = %q, want %q", info.Harness, "claude-code")
	}
	if info.PinnedVersion == "" {
		t.Error("PinnedVersion is empty, want the versions.json pin")
	}
	all, err := versions.All()
	if err != nil {
		t.Fatalf("versions.All: %v", err)
	}
	if want := all["claude-code"].Package; info.NPMPackage != want {
		t.Errorf("NPMPackage = %q, want %q", info.NPMPackage, want)
	}
}

func TestCheckBackendMatchingVersionSatisfiesPin(t *testing.T) {
	pin := claudePin(t)
	// Loom's probe reports the whole --version line, not a bare semver.
	info := checkWithHealth(t, "claude", pin+" (Claude Code)")

	if !info.VersionMatchesPin {
		t.Errorf("VersionMatchesPin = false for detected %q vs pin %q, want true",
			info.DetectedVersion, info.PinnedVersion)
	}
	if info.PinnedVersion != pin {
		t.Errorf("PinnedVersion = %q, want %q", info.PinnedVersion, pin)
	}
}

func TestCheckBackendDriftedVersionFailsPin(t *testing.T) {
	pin := claudePin(t)
	const detected = "0.0.1 (Claude Code)"
	info := checkWithHealth(t, "claude", detected)

	if info.VersionMatchesPin {
		t.Errorf("VersionMatchesPin = true for detected %q vs pin %q, want false",
			detected, pin)
	}
	// Both sides survive so a caller can say what drifted from what.
	if info.PinnedVersion != pin {
		t.Errorf("PinnedVersion = %q, want %q", info.PinnedVersion, pin)
	}
	if info.DetectedVersion != detected {
		t.Errorf("DetectedVersion = %q, want the unmodified probe line", info.DetectedVersion)
	}
}

func TestCheckBackendUnpinnedBackendIsNotDrift(t *testing.T) {
	// opencode has a versions.json entry but a deliberately empty pin.
	info := checkWithHealth(t, "opencode", "1.2.3")

	if !info.VersionMatchesPin {
		t.Error("VersionMatchesPin = false for an unpinned backend, want true")
	}
	if info.PinnedVersion != "" {
		t.Errorf("PinnedVersion = %q, want empty for an unpinned backend", info.PinnedVersion)
	}
	// The rest of the entry is still worth reporting.
	if info.Harness != "opencode" {
		t.Errorf("Harness = %q, want %q", info.Harness, "opencode")
	}
	if info.NPMPackage == "" {
		t.Error("NPMPackage is empty, want the versions.json package")
	}
}

func TestCheckBackendUnknownBackendIsNotDrift(t *testing.T) {
	// cursor, gemini, echo and external have no versions.json entry at all.
	info := checkWithHealth(t, "cursor", "2026.1.1")

	if !info.VersionMatchesPin {
		t.Error("VersionMatchesPin = false for an unknown backend, want true")
	}
	if info.Harness != "" || info.PinnedVersion != "" || info.NPMPackage != "" {
		t.Errorf("unknown backend leaked pin metadata: harness=%q pinned=%q pkg=%q",
			info.Harness, info.PinnedVersion, info.NPMPackage)
	}
}

func TestCheckBackendUnparseableVersionIsNotDrift(t *testing.T) {
	// A probe that returns something with no semver in it is "unknown", and
	// unknown must never be reported as drift.
	info := checkWithHealth(t, "codex", "unknown build")

	if !info.VersionMatchesPin {
		t.Error("VersionMatchesPin = false for an unparseable version, want true")
	}
	if info.PinnedVersion == "" {
		t.Error("PinnedVersion is empty, want the codex pin regardless of the probe")
	}
}

func TestSemverReExtractsFromRealVersionLines(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"2.1.201 (Claude Code)", "2.1.201"},
		{"codex-cli 0.142.5", "0.142.5"},
		{"0.142.5", "0.142.5"},
		{"1.0.0-beta.2 (nightly)", "1.0.0-beta.2"},
		{"unknown build", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := semverRe.FindString(tc.line); got != tc.want {
			t.Errorf("semverRe.FindString(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}
