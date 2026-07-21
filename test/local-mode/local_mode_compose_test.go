package localmode_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	localModeAdminActor = "local-mode-harness@fixture.local"
	localModeAdminKey   = "loom-local-mode-test-only-admin-key-v1"
)

type composeService struct {
	Build       composeBuild      `yaml:"build"`
	Command     []string          `yaml:"command"`
	Environment map[string]string `yaml:"environment"`
	Ports       []string          `yaml:"ports"`
}

type composeBuild struct {
	Context string `yaml:"context"`
}

type localModeCompose struct {
	Services map[string]composeService `yaml:"services"`
}

func TestLocalModeComposeUsesAuthenticatedFleetDBAndLoopbackPublishedUI(t *testing.T) {
	raw, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		t.Fatalf("read local-mode compose file: %v", err)
	}

	var cfg localModeCompose
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse local-mode compose file: %v", err)
	}

	fleet, ok := cfg.Services["fleet-db"]
	if !ok {
		t.Fatal("fleet-db service is missing")
	}
	for key, want := range map[string]string{
		"FLEET_AUTH_ENABLED":                         "true",
		"FLEET_AUTH_DEV_MODE":                        "false",
		"FLEET_AUTHZ_ENABLED":                        "true",
		"FLEET_AUTOMATION_TRIGGER_ADMISSION_ENABLED": "true",
		"FLEET_AUTH_BOOTSTRAP_ADMIN_ACTOR":           localModeEnv("LOCAL_MODE_FLEETDB_ADMIN_ACTOR", localModeAdminActor),
		"FLEET_AUTH_BOOTSTRAP_ADMIN_KEY":             localModeEnv("LOCAL_MODE_FLEETDB_API_KEY", localModeAdminKey),
	} {
		if got := fleet.Environment[key]; got != want {
			t.Errorf("fleet-db environment %s = %q, want %q", key, got, want)
		}
	}
	for _, forbidden := range []string{"--auth-dev-mode", "--auth-dev-mode=true", "--authz-enabled=false"} {
		if slices.Contains(fleet.Command, forbidden) {
			t.Errorf("fleet-db command still contains insecure flag %q", forbidden)
		}
	}
	if !slices.Contains(fleet.Ports, "127.0.0.1:${LOCAL_MODE_FLEETDB_PORT:-8280}:8080") {
		t.Errorf("fleet-db ports are not constrained to host loopback: %v", fleet.Ports)
	}
	if got, want := fleet.Build.Context, localModeEnv("LOCAL_MODE_FLEETDB_SOURCE_ROOT", "../../../fleet-db"); got != want {
		t.Errorf("fleet-db build context = %q, want %q", got, want)
	}

	loom, ok := cfg.Services["loom-local"]
	if !ok {
		t.Fatal("loom-local service is missing")
	}
	wantKey := localModeEnv("LOCAL_MODE_FLEETDB_API_KEY", localModeAdminKey)
	for _, key := range []string{"LOOM_FLEET_DB_API_KEY", "LOOM_FLEET_API_KEY"} {
		if got := loom.Environment[key]; got != wantKey {
			t.Errorf("loom-local environment %s = %q, want %q", key, got, wantKey)
		}
	}
	if got := loom.Environment["LOOM_WORKFLOW_CATALOG_ENABLED"]; got != "true" {
		t.Errorf("LOOM_WORKFLOW_CATALOG_ENABLED = %q, want true", got)
	}
	if got := loom.Environment["LOOM_AUTOMATION_ENABLED"]; got != "true" {
		t.Errorf("LOOM_AUTOMATION_ENABLED = %q, want true", got)
	}
	if !slices.Contains(loom.Ports, "127.0.0.1:${LOCAL_MODE_API_PORT:-8282}:8080") {
		t.Errorf("loom-local ports are not constrained to host loopback: %v", loom.Ports)
	}

	ui, ok := cfg.Services["ui-local"]
	if !ok {
		t.Fatal("ui-local service is missing")
	}
	if !slices.Contains(ui.Ports, "127.0.0.1:${LOCAL_MODE_UI_PORT:-8283}:8080") {
		t.Errorf("ui-local ports are not constrained to host loopback: %v", ui.Ports)
	}
}

func TestLocalModeWebhookProofUsesAPIKeyAuthentication(t *testing.T) {
	raw, err := os.ReadFile("verify-webhook.sh")
	if err != nil {
		t.Fatalf("read webhook verifier: %v", err)
	}
	script := string(raw)
	if !strings.Contains(script, `-H "X-API-Key: $API_KEY"`) {
		t.Fatal("webhook verifier does not send the local-mode FleetDB API key")
	}
	if strings.Contains(script, "X-Actor:") {
		t.Fatal("webhook verifier still relies on X-Actor dev-mode authentication")
	}

	makefile, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makeText := string(makefile)
	if !strings.Contains(makeText, "LOCAL_MODE_FLEETDB_API_KEY ?= "+localModeAdminKey) {
		t.Fatal("Makefile does not define the deterministic local-mode FleetDB API key")
	}
	if !strings.Contains(makeText, "export LOCAL_MODE_FLEETDB_API_KEY") {
		t.Fatal("Makefile does not export the local-mode FleetDB API key to Compose and proof scripts")
	}
	if !strings.Contains(makeText, "export LOCAL_MODE_FLEETDB_SOURCE_ROOT") {
		t.Fatal("Makefile does not export the paired FleetDB source root to Compose")
	}
}

func localModeEnv(name, fallback string) string {
	return "${" + name + ":-" + fallback + "}"
}
