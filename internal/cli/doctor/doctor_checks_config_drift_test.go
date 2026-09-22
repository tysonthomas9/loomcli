package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// driftFixture lays out a temp workspace runtime dir containing a daemon PID
// file and (optionally) a daemon-env.json, plus a registry.json pointed at by
// LOOM_STACK_REGISTRY. It returns the snapshot path so a case can corrupt it.
func driftFixture(t *testing.T, pid int, snap *cfgpkg.DaemonEnvSnapshot, registry string) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	t.Setenv("LOOM_FLEET_DB_NO_DISCOVERY", "1")
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", dir)
	t.Setenv("LOOM_STACK_REGISTRY", "")
	t.Setenv("LOOM_STACK_APP", "")
	// Point the conventional ~/local-stack/registry.json lookup at an empty
	// temp home, so a case with no registry does not silently read the
	// operator's real one.
	t.Setenv("LOCAL_STACK_DIR", "")
	t.Setenv("HOME", dir)
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

	snapshotPath := cfgpkg.ResolveDaemonEnvSnapshotPath(dir)
	if err := os.MkdirAll(filepath.Dir(snapshotPath), 0o755); err != nil {
		t.Fatalf("mkdir runtime dir: %v", err)
	}
	if pid > 0 {
		pidPath := filepath.Join(filepath.Dir(snapshotPath), "daemon.pid")
		if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
			t.Fatalf("write pid file: %v", err)
		}
	}
	if snap != nil {
		if err := cfgpkg.WriteDaemonEnvSnapshot(snapshotPath, *snap); err != nil {
			t.Fatalf("write snapshot: %v", err)
		}
	}
	if registry != "" {
		registryPath := filepath.Join(dir, "registry.json")
		if err := os.WriteFile(registryPath, []byte(registry), 0o600); err != nil {
			t.Fatalf("write registry: %v", err)
		}
		t.Setenv("LOOM_STACK_REGISTRY", registryPath)
	}
	return snapshotPath
}

// liveSnapshot builds a snapshot attributed to the test process, so the
// liveness check in checkConfigDrift passes.
func liveSnapshot(env map[string]cfgpkg.EnvValue) *cfgpkg.DaemonEnvSnapshot {
	return &cfgpkg.DaemonEnvSnapshot{
		Version:   1,
		PID:       os.Getpid(),
		Workspace: "PUPPET",
		Env:       env,
	}
}

func plainEnv(pairs map[string]string) map[string]cfgpkg.EnvValue {
	env := make(map[string]cfgpkg.EnvValue, len(pairs))
	for k, v := range pairs {
		if cfgpkg.IsSecretEnvKey(k) {
			env[k] = cfgpkg.EnvValue{Redacted: true, Fingerprint: cfgpkg.FingerprintEnvValue(v)}
			continue
		}
		env[k] = cfgpkg.EnvValue{Value: v}
	}
	return env
}

func registryJSON(t *testing.T, app string, env map[string]string) string {
	t.Helper()
	payload := map[string]any{"apps": map[string]any{app: map[string]any{"env": env}}}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	return string(data)
}

func TestCheckConfigDrift_InSync(t *testing.T) {
	env := map[string]string{
		"LOOM_WORKSPACE":              "PUPPET",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318",
	}
	driftFixture(t, os.Getpid(), liveSnapshot(plainEnv(env)),
		registryJSON(t, "loom-daemon-puppet", env))

	result := checkConfigDrift()
	if result.Status != StatusPass {
		t.Fatalf("expected pass, got %v: %s\n%s", result.Status, result.Summary, result.Detail)
	}
	if result.Name != "config_drift" {
		t.Errorf("expected name config_drift, got %q", result.Name)
	}
	if !strings.Contains(result.Summary, "loom-daemon-puppet") {
		t.Errorf("expected the app name in the summary, got %q", result.Summary)
	}
	if !strings.Contains(result.Summary, "2 vars") {
		t.Errorf("expected the var count in the summary, got %q", result.Summary)
	}
}

func TestCheckConfigDrift_RuntimeOnlyKeyIsLostOnNextDeploy(t *testing.T) {
	// The parent ticket's exact scenario: an OTEL endpoint set by hand at
	// runtime that the registry has never heard of.
	driftFixture(t, os.Getpid(), liveSnapshot(plainEnv(map[string]string{
		"LOOM_WORKSPACE":              "PUPPET",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318",
	})), registryJSON(t, "loom-daemon-puppet", map[string]string{"LOOM_WORKSPACE": "PUPPET"}))

	result := checkConfigDrift()
	if result.Status != StatusWarn {
		t.Fatalf("expected warn, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Detail, "OTEL_EXPORTER_OTLP_ENDPOINT") {
		t.Errorf("expected the drifting key in the detail, got %q", result.Detail)
	}
	if !strings.Contains(result.Detail, "lost on next deploy") {
		t.Errorf("expected the 'lost on next deploy' warning, got %q", result.Detail)
	}
	if !strings.Contains(result.Summary, "1 runtime-only") {
		t.Errorf("expected '1 runtime-only' in the summary, got %q", result.Summary)
	}
	if !strings.Contains(result.Detail, "remediation:") {
		t.Errorf("expected a remediation line, got %q", result.Detail)
	}
}

func TestCheckConfigDrift_DeclaredOnlyKey(t *testing.T) {
	driftFixture(t, os.Getpid(), liveSnapshot(plainEnv(map[string]string{
		"LOOM_WORKSPACE": "PUPPET",
	})), registryJSON(t, "loom-daemon-puppet", map[string]string{
		"LOOM_WORKSPACE": "PUPPET",
		"LOOM_NEW_THING": "1",
		"PATH":           "/usr/bin", // not a captured prefix: must be ignored
	}))

	result := checkConfigDrift()
	if result.Status != StatusWarn {
		t.Fatalf("expected warn, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "1 declared-only") {
		t.Errorf("expected '1 declared-only', got %q", result.Summary)
	}
	if !strings.Contains(result.Detail, "declared-only LOOM_NEW_THING") {
		t.Errorf("expected the declared-only key, got %q", result.Detail)
	}
	if strings.Contains(result.Detail, "PATH") {
		t.Errorf("unrelated declared vars must not be reported, got %q", result.Detail)
	}
}

func TestCheckConfigDrift_SecretValueDiffersShowsFingerprintsOnly(t *testing.T) {
	const runtimeSecret = "runtime-secret-value"
	const declaredSecret = "declared-secret-value"

	driftFixture(t, os.Getpid(), liveSnapshot(plainEnv(map[string]string{
		"LOOM_FLEET_DB_API_KEY": runtimeSecret,
	})), registryJSON(t, "loom-daemon-puppet", map[string]string{
		"LOOM_FLEET_DB_API_KEY": declaredSecret,
	}))

	result := checkConfigDrift()
	if result.Status != StatusWarn {
		t.Fatalf("expected warn, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "1 value-differs") {
		t.Errorf("expected '1 value-differs', got %q", result.Summary)
	}
	for _, fp := range []string{
		cfgpkg.FingerprintEnvValue(runtimeSecret),
		cfgpkg.FingerprintEnvValue(declaredSecret),
	} {
		if !strings.Contains(result.Detail, fp) {
			t.Errorf("expected fingerprint %s in the detail, got %q", fp, result.Detail)
		}
	}
	for _, secret := range []string{runtimeSecret, declaredSecret} {
		if strings.Contains(result.Detail, secret) {
			t.Fatalf("secret leaked into the detail: %q", result.Detail)
		}
	}
}

func TestCheckConfigDrift_PlainValueDiffers(t *testing.T) {
	driftFixture(t, os.Getpid(), liveSnapshot(plainEnv(map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318",
	})), registryJSON(t, "loom-daemon-puppet", map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://otel.example:4318",
	}))

	result := checkConfigDrift()
	if result.Status != StatusWarn {
		t.Fatalf("expected warn, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Detail, "value-differs OTEL_EXPORTER_OTLP_ENDPOINT") {
		t.Errorf("expected a value-differs line, got %q", result.Detail)
	}
}

func TestCheckConfigDrift_SkippedCases(t *testing.T) {
	t.Run("no snapshot", func(t *testing.T) {
		driftFixture(t, os.Getpid(), nil,
			registryJSON(t, "loom-daemon-puppet", map[string]string{"LOOM_WORKSPACE": "PUPPET"}))
		if result := checkConfigDrift(); result.Name != "" {
			t.Errorf("expected skip, got %+v", result)
		}
	})

	t.Run("dead daemon pid", func(t *testing.T) {
		driftFixture(t, 999999, liveSnapshot(plainEnv(map[string]string{"LOOM_WORKSPACE": "PUPPET"})),
			registryJSON(t, "loom-daemon-puppet", map[string]string{"LOOM_WORKSPACE": "PUPPET"}))
		if result := checkConfigDrift(); result.Name != "" {
			t.Errorf("expected skip, got %+v", result)
		}
	})

	t.Run("snapshot pid does not match the live daemon", func(t *testing.T) {
		snap := liveSnapshot(plainEnv(map[string]string{"LOOM_WORKSPACE": "PUPPET"}))
		snap.PID = os.Getpid() + 1
		driftFixture(t, os.Getpid(), snap,
			registryJSON(t, "loom-daemon-puppet", map[string]string{"LOOM_WORKSPACE": "PUPPET"}))
		if result := checkConfigDrift(); result.Name != "" {
			t.Errorf("expected skip, got %+v", result)
		}
	})

	t.Run("no registry", func(t *testing.T) {
		driftFixture(t, os.Getpid(), liveSnapshot(plainEnv(map[string]string{"LOOM_WORKSPACE": "PUPPET"})), "")
		if result := checkConfigDrift(); result.Name != "" {
			t.Errorf("expected skip, got %+v", result)
		}
	})

	t.Run("registry has no matching app", func(t *testing.T) {
		driftFixture(t, os.Getpid(), liveSnapshot(plainEnv(map[string]string{"LOOM_WORKSPACE": "PUPPET"})),
			registryJSON(t, "some-other-app", map[string]string{"LOOM_WORKSPACE": "PUPPET"}))
		if result := checkConfigDrift(); result.Name != "" {
			t.Errorf("expected skip, got %+v", result)
		}
	})
}

func TestCheckConfigDrift_StackAppOverrideFindsOddlyNamedApp(t *testing.T) {
	driftFixture(t, os.Getpid(), liveSnapshot(plainEnv(map[string]string{"LOOM_WORKSPACE": "PUPPET"})),
		registryJSON(t, "some-other-app", map[string]string{"LOOM_WORKSPACE": "PUPPET"}))
	t.Setenv("LOOM_STACK_APP", "some-other-app")

	result := checkConfigDrift()
	if result.Status != StatusPass {
		t.Fatalf("expected pass once the app is named, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "some-other-app") {
		t.Errorf("expected the overridden app name, got %q", result.Summary)
	}
}

func TestCheckConfigDrift_MalformedSnapshotWarns(t *testing.T) {
	snapshotPath := driftFixture(t, os.Getpid(),
		liveSnapshot(plainEnv(map[string]string{"LOOM_WORKSPACE": "PUPPET"})),
		registryJSON(t, "loom-daemon-puppet", map[string]string{"LOOM_WORKSPACE": "PUPPET"}))
	if err := os.WriteFile(snapshotPath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt snapshot: %v", err)
	}

	result := checkConfigDrift()
	if result.Status != StatusWarn {
		t.Fatalf("expected warn for a malformed snapshot, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "snapshot") {
		t.Errorf("expected the summary to name the snapshot, got %q", result.Summary)
	}
}

func TestCheckConfigDrift_MalformedRegistryWarns(t *testing.T) {
	driftFixture(t, os.Getpid(), liveSnapshot(plainEnv(map[string]string{"LOOM_WORKSPACE": "PUPPET"})),
		"{not json")

	result := checkConfigDrift()
	if result.Status != StatusWarn {
		t.Fatalf("expected warn for a malformed registry, got %v: %s", result.Status, result.Summary)
	}
	if !strings.Contains(result.Summary, "registry") {
		t.Errorf("expected the summary to name the registry, got %q", result.Summary)
	}
}

func TestCheckConfigDrift_DetailIsCapped(t *testing.T) {
	env := map[string]string{}
	for i := range 25 {
		env[fmt.Sprintf("LOOM_DRIFT_%02d", i)] = "v"
	}
	driftFixture(t, os.Getpid(), liveSnapshot(plainEnv(env)),
		registryJSON(t, "loom-daemon-puppet", map[string]string{}))

	result := checkConfigDrift()
	if result.Status != StatusWarn {
		t.Fatalf("expected warn, got %v: %s", result.Status, result.Summary)
	}
	lines := strings.Split(result.Detail, "\n")
	// 20 capped keys + the "… and N more" tail + the remediation line.
	if len(lines) != driftDetailCap+2 {
		t.Fatalf("expected %d detail lines, got %d:\n%s", driftDetailCap+2, len(lines), result.Detail)
	}
	if !strings.Contains(result.Detail, "… and 5 more") {
		t.Errorf("expected the '… and 5 more' tail, got:\n%s", result.Detail)
	}
}

func TestLoadRunningDaemonSnapshot(t *testing.T) {
	t.Run("no daemon", func(t *testing.T) {
		driftFixture(t, 999999, liveSnapshot(plainEnv(map[string]string{"LOOM_WORKSPACE": "PUPPET"})), "")
		snap, running := loadRunningDaemonSnapshot()
		if snap != nil || running {
			t.Errorf("expected (nil, false), got (%v, %v)", snap, running)
		}
	})

	t.Run("daemon running without a snapshot", func(t *testing.T) {
		driftFixture(t, os.Getpid(), nil, "")
		snap, running := loadRunningDaemonSnapshot()
		if snap != nil || !running {
			t.Errorf("expected (nil, true), got (%v, %v)", snap, running)
		}
	})

	t.Run("daemon running with a snapshot", func(t *testing.T) {
		driftFixture(t, os.Getpid(), liveSnapshot(plainEnv(map[string]string{
			"LOOM_FLEETDB_REDIS_URL": "redis://localhost:6379",
		})), "")
		snap, running := loadRunningDaemonSnapshot()
		if snap == nil || !running {
			t.Fatalf("expected a snapshot and running=true, got (%v, %v)", snap, running)
		}
		if got := snap.Plain("LOOM_FLEETDB_REDIS_URL"); got != "redis://localhost:6379" {
			t.Errorf("unexpected value %q", got)
		}
	})
}
