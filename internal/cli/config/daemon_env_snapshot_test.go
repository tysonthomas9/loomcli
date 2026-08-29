package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCaptureDaemonEnvSnapshot_KeepsOnlyLoomAndOtelPrefixes(t *testing.T) {
	t.Parallel()

	environ := []string{
		"LOOM_WORKSPACE=PUPPET",
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318",
		"PATH=/usr/bin",
		"CLAUDE_PID=1234",
		"FLEET_AUTH_ENABLED=true",
		"HOME=/Users/nobody",
		"malformed-entry-without-equals",
	}
	snap := CaptureDaemonEnvSnapshot(environ, "PUPPET")

	kept := []string{"LOOM_WORKSPACE", "OTEL_EXPORTER_OTLP_ENDPOINT"}
	for _, key := range kept {
		if _, ok := snap.Env[key]; !ok {
			t.Errorf("expected %s to be captured", key)
		}
	}
	for _, key := range []string{"PATH", "CLAUDE_PID", "FLEET_AUTH_ENABLED", "HOME"} {
		if _, ok := snap.Env[key]; ok {
			t.Errorf("expected %s to be dropped, got %+v", key, snap.Env[key])
		}
	}
	if len(snap.Env) != len(kept) {
		t.Errorf("expected %d captured vars, got %d: %v", len(kept), len(snap.Env), snap.SortedEnvKeys())
	}
	if snap.Version != 1 {
		t.Errorf("expected version 1, got %d", snap.Version)
	}
	if snap.PID != os.Getpid() {
		t.Errorf("expected pid %d, got %d", os.Getpid(), snap.PID)
	}
	if snap.Workspace != "PUPPET" {
		t.Errorf("expected workspace PUPPET, got %q", snap.Workspace)
	}
}

func TestCaptureDaemonEnvSnapshot_WorkspaceFallsBackToEnv(t *testing.T) {
	t.Parallel()

	snap := CaptureDaemonEnvSnapshot([]string{"LOOM_WORKSPACE=FROM_ENV"}, "")
	if snap.Workspace != "FROM_ENV" {
		t.Errorf("expected workspace from LOOM_WORKSPACE, got %q", snap.Workspace)
	}
}

func TestCaptureDaemonEnvSnapshot_RedactsSecrets(t *testing.T) {
	t.Parallel()

	const secret = "super-secret-value"
	environ := []string{
		"LOOM_FLEET_DB_API_KEY=" + secret,
		"LOOM_X_TOKEN=" + secret,
		"LOOM_DB_PASSWORD=" + secret,
		"LOOM_MY_SECRET=" + secret,
		"LOOM_GCP_CREDENTIAL=" + secret,
		"LOOM_WORKSPACE=PUPPET",
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318",
	}
	snap := CaptureDaemonEnvSnapshot(environ, "PUPPET")

	redacted := []string{"LOOM_FLEET_DB_API_KEY", "LOOM_X_TOKEN", "LOOM_DB_PASSWORD", "LOOM_MY_SECRET", "LOOM_GCP_CREDENTIAL"}
	for _, key := range redacted {
		v := snap.Env[key]
		if !v.Redacted {
			t.Errorf("%s: expected redacted", key)
		}
		if v.Value != "" {
			t.Errorf("%s: expected empty value, got %q", key, v.Value)
		}
		if len(v.Fingerprint) != 8 {
			t.Errorf("%s: expected 8-char fingerprint, got %q", key, v.Fingerprint)
		}
		if got := snap.Plain(key); got != "" {
			t.Errorf("%s: Plain() must not expose a redacted value, got %q", key, got)
		}
	}
	for _, key := range []string{"LOOM_WORKSPACE", "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		if snap.Env[key].Redacted {
			t.Errorf("%s: expected a plain value", key)
		}
		if snap.Env[key].Value == "" {
			t.Errorf("%s: expected a non-empty value", key)
		}
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("secret value leaked into marshaled snapshot: %s", data)
	}
}

func TestIsSecretEnvKey(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"LOOM_FLEET_DB_API_KEY":       true,
		"X_TOKEN":                     true,
		"DB_PASSWORD":                 true,
		"MY_SECRET":                   true,
		"loom_api_key":                true,
		"GOOGLE_CREDENTIALS":          true,
		"LOOM_WORKSPACE":              false,
		"OTEL_EXPORTER_OTLP_ENDPOINT": false,
	}
	for key, want := range tests {
		if got := IsSecretEnvKey(key); got != want {
			t.Errorf("IsSecretEnvKey(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestFingerprintEnvValue(t *testing.T) {
	t.Parallel()

	// Hard-coded so the Go and JS implementations cannot drift apart:
	// sha256("hello") starts 2cf24dba.
	if got := FingerprintEnvValue("hello"); got != "2cf24dba" {
		t.Errorf("FingerprintEnvValue(\"hello\") = %q, want 2cf24dba", got)
	}
	if FingerprintEnvValue("hello") != FingerprintEnvValue("hello") {
		t.Error("fingerprint is not stable across calls")
	}
	if FingerprintEnvValue("hello") == FingerprintEnvValue("hello ") {
		t.Error("different values produced the same fingerprint")
	}
}

func TestIsCapturedEnvKey(t *testing.T) {
	t.Parallel()

	for key, want := range map[string]bool{
		"LOOM_WORKSPACE":              true,
		"OTEL_SDK_DISABLED":           true,
		"FLEET_AUTH_ENABLED":          false,
		"PATH":                        false,
		"NOT_LOOM_PREFIXED":           false,
		"OTEL_EXPORTER_OTLP_ENDPOINT": true,
	} {
		if got := IsCapturedEnvKey(key); got != want {
			t.Errorf("IsCapturedEnvKey(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestWriteAndLoadDaemonEnvSnapshot_RoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), SnapshotFileName)
	want := DaemonEnvSnapshot{
		Version:   1,
		PID:       4242,
		Workspace: "PUPPET",
		Binary:    "/usr/local/bin/loom",
		Cwd:       "/tmp/project",
		StartedAt: time.Now().UTC().Truncate(time.Second),
		Env: map[string]EnvValue{
			"LOOM_WORKSPACE":        {Value: "PUPPET"},
			"LOOM_FLEET_DB_API_KEY": {Redacted: true, Fingerprint: "2cf24dba"},
		},
	}
	if err := WriteDaemonEnvSnapshot(path, want); err != nil {
		t.Fatalf("WriteDaemonEnvSnapshot: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected mode 0600, got %o", perm)
	}

	got, err := LoadDaemonEnvSnapshot(path)
	if err != nil {
		t.Fatalf("LoadDaemonEnvSnapshot: %v", err)
	}
	if got.PID != want.PID || got.Workspace != want.Workspace || got.Binary != want.Binary || got.Cwd != want.Cwd {
		t.Errorf("round trip mismatch: got %+v, want %+v", got, want)
	}
	if !got.StartedAt.Equal(want.StartedAt) {
		t.Errorf("started_at = %v, want %v", got.StartedAt, want.StartedAt)
	}
	if got.Plain("LOOM_WORKSPACE") != "PUPPET" {
		t.Errorf("plain value lost: %+v", got.Env["LOOM_WORKSPACE"])
	}
	if got.Env["LOOM_FLEET_DB_API_KEY"].Fingerprint != "2cf24dba" {
		t.Errorf("fingerprint lost: %+v", got.Env["LOOM_FLEET_DB_API_KEY"])
	}
	if keys := got.SortedEnvKeys(); len(keys) != 2 || keys[0] != "LOOM_FLEET_DB_API_KEY" {
		t.Errorf("SortedEnvKeys = %v", keys)
	}
}

func TestLoadDaemonEnvSnapshot_Errors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	_, err := LoadDaemonEnvSnapshot(filepath.Join(dir, "missing.json"))
	if err == nil || !os.IsNotExist(err) {
		t.Errorf("expected IsNotExist error for a missing file, got %v", err)
	}

	garbage := filepath.Join(dir, "garbage.json")
	if err := os.WriteFile(garbage, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	_, err = LoadDaemonEnvSnapshot(garbage)
	if err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
	if os.IsNotExist(err) {
		t.Errorf("malformed JSON must not report as not-exist: %v", err)
	}
}

func TestDisplayEnvValue(t *testing.T) {
	t.Parallel()

	if got := DisplayEnvValue(EnvValue{Value: "plain"}); got != "plain" {
		t.Errorf("got %q", got)
	}
	got := DisplayEnvValue(EnvValue{Redacted: true, Fingerprint: "2cf24dba"})
	if got != "<redacted sha256:2cf24dba>" {
		t.Errorf("got %q", got)
	}
}

func TestResolveDaemonEnvSnapshotPath_FallsBackBesideProject(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	got := ResolveDaemonEnvSnapshotPath(dir)
	if filepath.Base(got) != SnapshotFileName {
		t.Errorf("expected basename %s, got %s", SnapshotFileName, got)
	}
}

func TestDaemonEnvSnapshot_NilReceiverIsSafe(t *testing.T) {
	t.Parallel()

	var snap *DaemonEnvSnapshot
	if got := snap.Plain("LOOM_WORKSPACE"); got != "" {
		t.Errorf("expected empty string from nil snapshot, got %q", got)
	}
	if keys := snap.SortedEnvKeys(); keys != nil {
		t.Errorf("expected nil keys from nil snapshot, got %v", keys)
	}
}
