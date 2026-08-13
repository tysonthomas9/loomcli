package cli

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/leadoccupant"
)

func TestResolveIssueBackendType(t *testing.T) {
	t.Run("defaults to fleetdb", func(t *testing.T) {
		got := resolveIssueBackendType()
		if got != "fleetdb" {
			t.Errorf("resolveIssueBackendType() = %q, want %q", got, "fleetdb")
		}
	})
}

func TestIsFleetDBActive(t *testing.T) {
	t.Run("returns true when fleetdb default", func(t *testing.T) {
		if !isFleetDBActive() {
			t.Error("expected isFleetDBActive() to return true")
		}
	})
}

func TestResolveIssueBackendType_FleetEnvVar(t *testing.T) {
	t.Setenv("LOOM_ISSUE_BACKEND", "fleet")
	got := resolveIssueBackendType()
	if got != "fleet" {
		t.Errorf("resolveIssueBackendType() = %q, want %q", got, "fleet")
	}
}

func TestResolveIssueBackendType_InvalidEnvVar(t *testing.T) {
	t.Setenv("LOOM_ISSUE_BACKEND", "postgres")
	got := resolveIssueBackendType()
	// Invalid value is ignored (with a warning log); falls through to default "fleetdb"
	if got != "fleetdb" {
		t.Errorf("resolveIssueBackendType() = %q, want %q (invalid LOOM_ISSUE_BACKEND should fall through to default)", got, "fleetdb")
	}
}

func TestIsFleetActive(t *testing.T) {
	t.Run("returns true when fleet", func(t *testing.T) {
		t.Setenv("LOOM_ISSUE_BACKEND", "fleet")
		if !isFleetActive() {
			t.Error("expected isFleetActive() to return true when LOOM_ISSUE_BACKEND=fleet")
		}
	})

	t.Run("returns false when fleetdb", func(t *testing.T) {
		t.Setenv("LOOM_ISSUE_BACKEND", "fleetdb")
		if isFleetActive() {
			t.Error("expected isFleetActive() to return false when LOOM_ISSUE_BACKEND=fleetdb")
		}
	})

	t.Run("returns false by default", func(t *testing.T) {
		// No env vars set; defaults to fleetdb, not fleet mode.
		if isFleetActive() {
			t.Error("expected isFleetActive() to return false by default")
		}
	})
}

func TestResolveIssueBackendType_OccupantPresenceBeatsPublicSelection(t *testing.T) {
	t.Setenv(leadoccupant.EnvOccupantToken, "token")
	t.Setenv(leadoccupant.EnvLeadAPIURL, "")
	t.Setenv(leadoccupant.EnvWorkspace, "")
	t.Setenv("LOOM_ISSUE_BACKEND", IssueBackendFleet)
	t.Setenv("LOOM_SERVER_URL", "http://ordinary.invalid")
	if got := resolveIssueBackendType(); got != issueBackendOccupant {
		t.Fatalf("resolveIssueBackendType() = %q, want internal occupant sentinel", got)
	}
}

func TestResolveIssueBackendType_NoOccupantSnapshot(t *testing.T) {
	t.Setenv(leadoccupant.EnvOccupantToken, "")
	for _, tc := range []struct {
		name   string
		issue  string
		server string
		want   string
	}{
		{"default", "", "", IssueBackendFleetDB},
		{"explicit fleet", IssueBackendFleet, "", IssueBackendFleet},
		{"explicit fleetdb", IssueBackendFleetDB, "http://server", IssueBackendFleetDB},
		{"explicit api", IssueBackendAPI, "", IssueBackendAPI},
		{"server", "", "http://server", IssueBackendAPI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LOOM_ISSUE_BACKEND", tc.issue)
			t.Setenv("LOOM_SERVER_URL", tc.server)
			if got := resolveIssueBackendType(); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
