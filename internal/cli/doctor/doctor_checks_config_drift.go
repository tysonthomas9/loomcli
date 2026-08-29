package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon"
)

// driftDetailCap bounds the per-key detail listing so a wildly divergent
// environment produces a readable report rather than a wall of text.
const driftDetailCap = 20

// stackRegistry is the deploy orchestrator's declared configuration: the file a
// redeploy rebuilds the daemon's environment from. Only the fields this check
// compares are modeled.
type stackRegistry struct {
	Apps map[string]struct {
		Env map[string]string `json:"env"`
	} `json:"apps"`
}

// loadRunningDaemonSnapshot returns the env snapshot published by the daemon
// that is currently running, if any. The bool reports whether a daemon is
// running at all — a daemon that is up but published no snapshot (an older
// binary) yields (nil, true), which callers must distinguish from "no daemon".
func loadRunningDaemonSnapshot() (*cfgpkg.DaemonEnvSnapshot, bool) {
	projectDir := cli.GetWorkspaceRuntimeDir()
	snapshotPath := cfgpkg.ResolveDaemonEnvSnapshotPath(projectDir)
	pidFilePath := filepath.Join(filepath.Dir(snapshotPath), "daemon.pid")

	pid, running := daemon.IsLoomDaemonRunning(pidFilePath)
	if !running {
		return nil, false
	}
	snap, err := cfgpkg.LoadDaemonEnvSnapshot(snapshotPath)
	if err != nil || snap == nil {
		return nil, true
	}
	if snap.PID != pid {
		// A SIGKILLed daemon leaves its snapshot behind; the file describes a
		// process that no longer exists.
		return nil, true
	}
	return snap, true
}

// checkConfigDrift compares the running daemon's resolved environment against
// the environment declared in the deploy registry. It WARNS and never fails:
// drift is a latent revert (a runtime-only variable is dropped by the next
// deploy), not a current outage, and a hard failure would break `loom doctor`'s
// exit code for anyone whose stack legitimately sets extra variables.
//
// The check is inert outside this operator's stack: it returns an empty
// CheckResult (the package's "skipped" convention) at any miss.
//
//nolint:funlen // Sequential resolve-then-diff steps that read top-to-bottom.
func checkConfigDrift() CheckResult {
	projectDir := cli.GetWorkspaceRuntimeDir()
	snapshotPath := cfgpkg.ResolveDaemonEnvSnapshotPath(projectDir)
	pidFilePath := filepath.Join(filepath.Dir(snapshotPath), "daemon.pid")

	pid, running := daemon.IsLoomDaemonRunning(pidFilePath)
	if !running {
		return CheckResult{} // checkLoomDaemon covers "not running"
	}
	snap, err := cfgpkg.LoadDaemonEnvSnapshot(snapshotPath)
	if err != nil {
		if os.IsNotExist(err) {
			return CheckResult{} // older daemon binary; nothing to compare
		}
		return CheckResult{
			Name:    "config_drift",
			Status:  StatusWarn,
			Summary: "could not parse the daemon env snapshot",
			Detail:  fmt.Sprintf("%s: %v", snapshotPath, err),
		}
	}
	if snap.PID != pid {
		return CheckResult{} // stale snapshot from a killed daemon
	}

	registryPath, ok := locateStackRegistry()
	if !ok {
		return CheckResult{}
	}
	registry, err := loadStackRegistry(registryPath)
	if err != nil {
		return CheckResult{
			Name:    "config_drift",
			Status:  StatusWarn,
			Summary: "could not parse the deploy registry",
			Detail:  fmt.Sprintf("%s: %v", registryPath, err),
		}
	}

	app, declared, ok := locateRegistryApp(registry, snap.Workspace)
	if !ok {
		return CheckResult{}
	}

	return reportConfigDrift(app, snap, declared)
}

// locateStackRegistry resolves the deploy registry path, honoring the operator
// overrides before the conventional location.
func locateStackRegistry() (string, bool) {
	var candidates []string
	if v := os.Getenv("LOOM_STACK_REGISTRY"); v != "" {
		candidates = append(candidates, v)
	}
	if v := os.Getenv("LOCAL_STACK_DIR"); v != "" {
		candidates = append(candidates, filepath.Join(v, "registry.json"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "local-stack", "registry.json"))
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	return "", false
}

func loadStackRegistry(path string) (*stackRegistry, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from an operator-set env var or the conventional stack location
	if err != nil {
		return nil, err
	}
	var reg stackRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, err
	}
	return &reg, nil
}

// locateRegistryApp picks the registry entry describing this daemon.
func locateRegistryApp(reg *stackRegistry, workspace string) (string, map[string]string, bool) {
	var names []string
	if v := os.Getenv("LOOM_STACK_APP"); v != "" {
		names = append(names, v)
	}
	if workspace != "" {
		names = append(names, "loom-daemon-"+strings.ToLower(workspace))
	}
	names = append(names, "loom-daemon")

	for _, name := range names {
		if app, ok := reg.Apps[name]; ok {
			env := app.Env
			if env == nil {
				env = map[string]string{}
			}
			return name, env, true
		}
	}
	return "", nil, false
}

// reportConfigDrift diffs the daemon's live environment against the declared
// one, restricting the declared side to the same prefixes the snapshot captures
// so unrelated declared variables are not reported.
func reportConfigDrift(app string, snap *cfgpkg.DaemonEnvSnapshot, declared map[string]string) CheckResult {
	var runtimeOnly, valueDiffers []string

	for _, key := range snap.SortedEnvKeys() {
		live := snap.Env[key]
		want, ok := declared[key]
		switch {
		case !ok:
			runtimeOnly = append(runtimeOnly, fmt.Sprintf("runtime-only %s=%s (lost on next deploy)",
				key, cfgpkg.DisplayEnvValue(live)))
		case !envValueMatches(key, live, want):
			valueDiffers = append(valueDiffers, describeValueDrift(key, live, want))
		}
	}

	declaredOnly := findDeclaredOnly(snap, declared)

	total := len(runtimeOnly) + len(declaredOnly) + len(valueDiffers)
	if total == 0 {
		return CheckResult{
			Name:    "config_drift",
			Status:  StatusPass,
			Summary: fmt.Sprintf("daemon env matches registry.json (%s, %d vars)", app, len(snap.Env)),
		}
	}

	lines := append([]string{}, runtimeOnly...)
	lines = append(lines, declaredOnly...)
	lines = append(lines, valueDiffers...)
	sort.Strings(lines)
	if len(lines) > driftDetailCap {
		more := len(lines) - driftDetailCap
		lines = append(lines[:driftDetailCap:driftDetailCap], fmt.Sprintf("… and %d more", more))
	}
	lines = append(lines, fmt.Sprintf(
		"remediation: curl -fsS -X PATCH http://index.local/apps/%s/env -H 'content-type: application/json' -d '{\"KEY\":\"value\"}'", app))

	return CheckResult{
		Name:   "config_drift",
		Status: StatusWarn,
		Summary: fmt.Sprintf("daemon env differs from registry.json (%s): %d runtime-only, %d declared-only, %d value-differs",
			app, len(runtimeOnly), len(declaredOnly), len(valueDiffers)),
		Detail: strings.Join(lines, "\n"),
	}
}

// findDeclaredOnly lists declared keys the running daemon does not have. The
// declared side is restricted to the prefixes a snapshot captures, so unrelated
// declared variables are never reported.
func findDeclaredOnly(snap *cfgpkg.DaemonEnvSnapshot, declared map[string]string) []string {
	keys := make([]string, 0, len(declared))
	for key := range declared {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var out []string
	for _, key := range keys {
		if !cfgpkg.IsCapturedEnvKey(key) {
			continue
		}
		if _, ok := snap.Env[key]; !ok {
			out = append(out, fmt.Sprintf("declared-only %s (a daemon restart is pending)", key))
		}
	}
	return out
}

// envValueMatches compares a live value against a declared one. Secret keys are
// compared by fingerprint, so a mismatch is detectable without either side's
// value ever being written out.
func envValueMatches(key string, live cfgpkg.EnvValue, declared string) bool {
	if cfgpkg.IsSecretEnvKey(key) {
		return live.Fingerprint == cfgpkg.FingerprintEnvValue(declared)
	}
	return live.Value == declared
}

func describeValueDrift(key string, live cfgpkg.EnvValue, declared string) string {
	if cfgpkg.IsSecretEnvKey(key) {
		return fmt.Sprintf("value-differs %s (runtime %s, declared %s)",
			key, live.Fingerprint, cfgpkg.FingerprintEnvValue(declared))
	}
	return fmt.Sprintf("value-differs %s (runtime %s, declared %s)", key, live.Value, declared)
}
