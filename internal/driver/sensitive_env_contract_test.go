package driver

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

// The vendored canonical sensitive-env-name contract. It is mirrored
// BYTE-IDENTICALLY from meta-harness's contract/sensitive-env-names.json by that
// repo's scripts/sync-sensitive-env-names.sh --to <this repo>; do not hand-edit it.
//
// Before it existed the list was a hand-copied literal in four places across the two
// repos and had already drifted: CLAUDE_CODE_OAUTH_TOKEN was in
// trustedLocalProviderCredentials and in meta-harness's probe but MISSING from
// sandboxLeakProbeCommand(), so a regression leaking that token into a Daytona
// sandbox counted as zero leaks and the run proceeded.
//
// This test is the Go half of the gate. The TS half is
// internal/workflows/builtin/daytona-task-runner.test.mjs, which reads the same file.
const sensitiveEnvContractPath = "testdata/sensitive-env-names.json"

const syncHint = "edit contract/sensitive-env-names.json in meta-harness and re-run " +
	"scripts/sync-sensitive-env-names.sh --to <this repo>, or add the name here"

type sensitiveEnvContract struct {
	Version             int      `json:"version"`
	RunnerInfra         []string `json:"runner_infra"`
	ProviderCredentials []string `json:"provider_credentials"`
}

func loadSensitiveEnvContract(t *testing.T) sensitiveEnvContract {
	t.Helper()
	raw, err := os.ReadFile(sensitiveEnvContractPath)
	if err != nil {
		t.Fatalf("cannot read the vendored sensitive-env-name contract %s: %v\n"+
			"It is vendored from meta-harness; restore it with "+
			"scripts/sync-sensitive-env-names.sh --to <this repo> there.",
			sensitiveEnvContractPath, err)
	}
	var c sensitiveEnvContract
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("%s is not valid JSON: %v\n"+
			"It is vendored byte-for-byte from meta-harness — repair it there and re-mirror "+
			"with scripts/sync-sensitive-env-names.sh --to <this repo>.",
			sensitiveEnvContractPath, err)
	}
	if c.Version != 1 {
		t.Fatalf("%s declares version %d; this test understands version 1 only",
			sensitiveEnvContractPath, c.Version)
	}
	if len(c.RunnerInfra) == 0 || len(c.ProviderCredentials) == 0 {
		t.Fatalf("%s has an empty role list (runner_infra=%d provider_credentials=%d)",
			sensitiveEnvContractPath, len(c.RunnerInfra), len(c.ProviderCredentials))
	}
	return c
}

// The artifact's provider_credentials role IS trustedLocalProviderCredentials. Adding a
// name to one without the other is exactly the drift this contract exists to stop.
func TestSensitiveEnvContractMatchesTrustedLocalProviderCredentials(t *testing.T) {
	c := loadSensitiveEnvContract(t)

	want := make(map[string]struct{}, len(c.ProviderCredentials))
	for _, name := range c.ProviderCredentials {
		if _, dup := want[name]; dup {
			t.Errorf("%s lists %q twice in provider_credentials", sensitiveEnvContractPath, name)
		}
		want[name] = struct{}{}
	}

	var missingHere, extraHere []string
	for name := range want {
		if _, ok := trustedLocalProviderCredentials[name]; !ok {
			missingHere = append(missingHere, name)
		}
	}
	for name := range trustedLocalProviderCredentials {
		if _, ok := want[name]; !ok {
			extraHere = append(extraHere, name)
		}
	}
	sort.Strings(missingHere)
	sort.Strings(extraHere)

	if len(missingHere) > 0 || len(extraHere) > 0 {
		t.Errorf("trustedLocalProviderCredentials has drifted from %s\n"+
			"  in the contract but not in env.go: %s\n"+
			"  in env.go but not in the contract: %s\n"+
			"To fix: %s",
			sensitiveEnvContractPath,
			strings.Join(missingHere, ", "),
			strings.Join(extraHere, ", "),
			syncHint)
	}
}

// driverAllowlistedRunnerInfra are runner_infra names the DRIVER subprocess is
// deliberately allowed to inherit (subprocessEnvAllowExact), even though they must
// never reach a sandbox GUEST. The two scopes are different: the driver is the host-side
// process that *launches* the runner and needs its command payload; the guest is the
// remote sandbox the probe inspects.
//
// This set is spelled out rather than skipped so that widening it is a visible, reviewed
// diff. Anything NOT listed here must be dropped by the strict filter.
var driverAllowlistedRunnerInfra = map[string]struct{}{
	TaskRunnerCommandJSONEnv: {},
}

// The property that actually matters: the strict filter — what keeps credentials out of
// Daytona/remote runners — must drop every contract name. Stronger than comparing
// subprocessEnvSensitiveExact, because several names are covered by prefix/fragment
// rules (GOOGLE_, TOKEN, API_KEY) or simply by being absent from the allowlist
// (CODEX_HOME, DAYTONA_API_KEY) rather than by an exact sensitive entry.
func TestSensitiveEnvContractNamesAreDroppedByStrictFilter(t *testing.T) {
	c := loadSensitiveEnvContract(t)

	for _, name := range append(append([]string{}, c.RunnerInfra...), c.ProviderCredentials...) {
		t.Run(name, func(t *testing.T) {
			got := scopedSubprocessBaseEnv([]string{name + "=x"})
			_, allowed := driverAllowlistedRunnerInfra[name]
			if allowed {
				if len(got) != 1 {
					t.Fatalf("%s is in driverAllowlistedRunnerInfra but scopedSubprocessBaseEnv "+
						"dropped it (got %v); either the allowlist entry is stale or env.go changed",
						name, got)
				}
				return
			}
			if len(got) != 0 {
				t.Fatalf("scopedSubprocessBaseEnv forwarded %q to a remote runner (got %v); "+
					"every name in %s must be dropped, or be justified in "+
					"driverAllowlistedRunnerInfra",
					name, got, sensitiveEnvContractPath)
			}
		})
	}
}

// The local-runner widening is the one place provider credentials are admitted, and it
// must admit exactly the contract's provider_credentials — no more.
func TestLocalTaskRunnerBaseEnvAdmitsExactlyTheContractCredentials(t *testing.T) {
	c := loadSensitiveEnvContract(t)

	input := make([]string, 0, len(c.ProviderCredentials))
	for _, name := range c.ProviderCredentials {
		input = append(input, name+"=x")
	}

	got := make(map[string]struct{}, len(input))
	for _, entry := range localTaskRunnerBaseEnv(input) {
		name, _, ok := strings.Cut(entry, "=")
		if ok {
			got[name] = struct{}{}
		}
	}

	for _, name := range c.ProviderCredentials {
		if _, ok := got[name]; !ok {
			t.Errorf("localTaskRunnerBaseEnv dropped %q, which %s declares as a "+
				"provider credential the local runner must inherit. To fix: %s",
				name, sensitiveEnvContractPath, syncHint)
		}
	}
	if len(got) != len(c.ProviderCredentials) {
		t.Errorf("localTaskRunnerBaseEnv admitted %d names for %d contract credentials: %v",
			len(got), len(c.ProviderCredentials), got)
	}
}
