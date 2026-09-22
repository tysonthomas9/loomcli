package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The runtime storage paths are Local and Cloud only, enforced by
// `make check-control-plane-paths`. LOOM_SERVER_URL must therefore NOT
// introduce a third mode, however tempting: it is a sub-option behind Local.
func TestDetectMode_ServerURLDoesNotCreateAThirdMode(t *testing.T) {
	tests := []struct {
		name       string
		fleetDBURL string
		serverURL  string
		want       Mode
	}{
		{
			name: "neither set is local",
			want: ModeLocal,
		},
		{
			name:       "fleet-db url alone is cloud",
			fleetDBURL: "http://127.0.0.1:3011",
			want:       ModeCloud,
		},
		{
			name:      "server url alone stays local",
			serverURL: "http://127.0.0.1:3012",
			want:      ModeLocal,
		},
		{
			name:       "fleet-db url wins when both are set",
			fleetDBURL: "http://127.0.0.1:3011",
			serverURL:  "http://127.0.0.1:3012",
			want:       ModeCloud,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvFleetDBURL, tt.fleetDBURL)
			t.Setenv(EnvServerURL, tt.serverURL)

			if got := DetectMode(); got != tt.want {
				t.Errorf("DetectMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ServerWithoutFleetDB carries the distinction the Mode enum deliberately does
// not: aimed at a remote loom server, with no store named.
func TestServerWithoutFleetDB(t *testing.T) {
	tests := []struct {
		name       string
		fleetDBURL string
		serverURL  string
		want       bool
	}{
		{name: "neither set", want: false},
		{name: "fleet-db only", fleetDBURL: "http://127.0.0.1:3011", want: false},
		{name: "server only", serverURL: "http://127.0.0.1:3012", want: true},
		{
			name:       "both set is configured, not ambiguous",
			fleetDBURL: "http://127.0.0.1:3011",
			serverURL:  "http://127.0.0.1:3012",
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvFleetDBURL, tt.fleetDBURL)
			t.Setenv(EnvServerURL, tt.serverURL)

			if got := ServerWithoutFleetDB(); got != tt.want {
				t.Errorf("ServerWithoutFleetDB() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The regression that matters: OpenStore must refuse rather than start an
// embedded fleet-db. The old behavior spawned one against an empty snapshot and
// then reported the active workspace as missing, which reads as data loss
// instead of misconfiguration.
func TestOpenStore_RefusesInsteadOfSpawningEmbeddedForServerURL(t *testing.T) {
	t.Setenv(EnvFleetDBURL, "")
	t.Setenv(EnvServerURL, "http://127.0.0.1:3012")

	dataDir := t.TempDir()

	handle, err := OpenStore(context.Background(), dataDir, nil)

	if err == nil {
		if handle != nil {
			_ = handle.Close()
		}
		t.Fatal("OpenStore succeeded with only a server URL; want ErrServerModeNoFleetDB")
	}
	if !errors.Is(err, ErrServerModeNoFleetDB) {
		t.Fatalf("OpenStore error = %v, want ErrServerModeNoFleetDB", err)
	}
	if handle != nil {
		_ = handle.Close()
		t.Error("OpenStore returned a non-nil handle alongside an error")
	}

	// StartEmbedded materializes its miniredis snapshot under dataDir/fleet-db.
	// Its absence is the proof that nothing was spawned.
	if _, statErr := os.Stat(filepath.Join(dataDir, "fleet-db")); !os.IsNotExist(statErr) {
		t.Errorf("embedded fleet-db directory was created (stat err = %v)", statErr)
	}
}

// The message is the deliverable here: it replaces a "workspace not found" that
// named the wrong problem, so assert it stays actionable.
//
// Both exits must be named. Pointing at a fleet-db is right for someone who
// meant to talk to the server's store; unsetting the server URL is right for
// someone with a populated local store who exported it only for `loom data`,
// and whose previously-working setup this guard now refuses.
func TestErrServerModeNoFleetDB_NamesBothRemedies(t *testing.T) {
	msg := ErrServerModeNoFleetDB.Error()

	for _, want := range []string{
		EnvServerURL,
		EnvFleetDBURL,
		EnvFleetDBActor,
		"unset " + EnvServerURL,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message does not mention %q:\n%s", want, msg)
		}
	}
}

// A copy-pasteable host from whichever deployment happened to be at hand points
// every other install at nothing. The example must stay a placeholder.
func TestErrServerModeNoFleetDB_ExampleURLIsNotADeploymentAddress(t *testing.T) {
	msg := ErrServerModeNoFleetDB.Error()

	for _, banned := range []string{"127.0.0.1", "localhost", "0.0.0.0"} {
		if strings.Contains(msg, banned) {
			t.Errorf("error message hardcodes %q as an example address:\n%s", banned, msg)
		}
	}
	if !strings.Contains(msg, "<fleet-db-host>") {
		t.Errorf("error message does not show a placeholder fleet-db URL:\n%s", msg)
	}
}
