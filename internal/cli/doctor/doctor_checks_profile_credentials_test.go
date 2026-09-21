package doctor

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
)

// credentialSentinel stands in for token bytes. No summary, detail or error
// this check produces may contain it.
const credentialSentinel = "SENTINEL-DO-NOT-PRINT"

// hollowCredentials is the file observed on 2026-09-11: structurally valid,
// correct scopes, no token.
const hollowCredentials = `{"claudeAiOauth":{"accessToken":"","refreshToken":"",` +
	`"scopes":["user:inference"],"subscriptionType":"max"}}`

// fakeHarnessVersion is what the stubbed probe reports; this check does not
// read it, but stageProfile pins it into the manifest.
const fakeHarnessVersion = "2.1.237 (Claude Code)"

const populatedCredentials = `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-real"}}`

func stageCredentials(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, agentprofile.CredentialsName), []byte(body), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
}

// A fleet with no profiles sees no new output at all — the same contract
// checkAgentProfiles keeps.
func TestCheckProfileCredentials_NoProfilesIsSilent(t *testing.T) {
	stageProfileWorkspace(t, fakeHarnessVersion)
	if got := checkProfileCredentials(); got != (CheckResult{}) {
		t.Fatalf("expected no result, got %+v", got)
	}
}

// The recommended configuration: no credential file, authentication by the
// injected env token. This is the row that must never regress into a fault.
func TestCheckProfileCredentials_AbsentFilePasses(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, fakeHarnessVersion)
	stageProfile(t, runtimeDir, "observer", fakeHarnessVersion)

	got := checkProfileCredentials()
	if got.Status != StatusPass {
		t.Fatalf("status = %v, want pass (%+v)", got.Status, got)
	}
	if !strings.Contains(got.Summary, "or none") {
		t.Errorf("summary must not imply a file was found: %q", got.Summary)
	}
}

func TestCheckProfileCredentials_PopulatedPasses(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, fakeHarnessVersion)
	dir := stageProfile(t, runtimeDir, "observer", fakeHarnessVersion)
	stageCredentials(t, dir, populatedCredentials)

	if got := checkProfileCredentials(); got.Status != StatusPass {
		t.Fatalf("status = %v, want pass (%+v)", got.Status, got)
	}
}

// The outage, reported. The detail must name the profile, explain the
// shadowing, and state the repair — an operator reading this is the same person
// who otherwise spends fourteen hours not knowing.
func TestCheckProfileCredentials_HollowFails(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, fakeHarnessVersion)
	dir := stageProfile(t, runtimeDir, "planner", fakeHarnessVersion)
	stageCredentials(t, dir, hollowCredentials)

	got := checkProfileCredentials()
	if got.Name != "agent_profile_credentials" {
		t.Errorf("name = %q, want agent_profile_credentials", got.Name)
	}
	if got.Status != StatusFail {
		t.Fatalf("status = %v, want fail (%+v)", got.Status, got)
	}
	if !strings.Contains(got.Summary, "1 of 1") {
		t.Errorf("summary = %q, want a 1 of 1 count", got.Summary)
	}
	for _, want := range []string{
		"planner",
		filepath.Join(".loom", "agent-profiles", "planner", "claude"),
		"CLAUDE_CODE_OAUTH_TOKEN",
		"rm ",
		agentprofile.CredentialsName,
	} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("detail missing %q:\n%s", want, got.Detail)
		}
	}
}

// --fix re-blesses a version pin and nothing else. A failing result says so,
// so an operator does not reach for it and wonder why nothing changed.
func TestCheckProfileCredentials_FixSaysItWillNotRemove(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, fakeHarnessVersion)
	stageCredentials(t, stageProfile(t, runtimeDir, "planner", fakeHarnessVersion), hollowCredentials)

	prev := doctorFix
	doctorFix = true
	t.Cleanup(func() { doctorFix = prev })

	got := checkProfileCredentials()
	if got.Status != StatusFail {
		t.Fatalf("status = %v, want fail", got.Status)
	}
	if !strings.Contains(got.Detail, "--fix does not remove credential files") {
		t.Errorf("detail must say --fix will not act:\n%s", got.Detail)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, ".loom", "agent-profiles", "planner", "claude",
		agentprofile.CredentialsName)); err != nil {
		t.Fatalf("--fix must leave the credential file alone: %v", err)
	}
}

// Healthy profiles stay out of the report entirely, and the count names how
// many of the fleet are affected rather than how many exist.
func TestCheckProfileCredentials_MixedFleet(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, fakeHarnessVersion)
	for _, agent := range []string{"planner", "tester", "worker"} {
		stageCredentials(t, stageProfile(t, runtimeDir, agent, fakeHarnessVersion), hollowCredentials)
	}
	stageCredentials(t, stageProfile(t, runtimeDir, "critic", fakeHarnessVersion), populatedCredentials)
	stageProfile(t, runtimeDir, "observer", fakeHarnessVersion) // no credential file at all

	got := checkProfileCredentials()
	if got.Status != StatusFail {
		t.Fatalf("status = %v, want fail (%+v)", got.Status, got)
	}
	if !strings.Contains(got.Summary, "3 of 5") {
		t.Errorf("summary = %q, want 3 of 5", got.Summary)
	}
	for _, healthy := range []string{"critic", "observer"} {
		if strings.Contains(got.Detail, healthy) {
			t.Errorf("healthy profile %q must not appear:\n%s", healthy, got.Detail)
		}
	}
	// Sorted by agent, the same ordering rule faultLines uses.
	if i, j := strings.Index(got.Detail, "planner"), strings.Index(got.Detail, "tester"); i > j {
		t.Errorf("detail is not sorted by agent:\n%s", got.Detail)
	}
}

// The severity split, asserted: an unparseable file may be a read that lost the
// race with the harness's own rewrite, so it warns and the profile still boots.
func TestCheckProfileCredentials_MalformedWarns(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, fakeHarnessVersion)
	stageCredentials(t, stageProfile(t, runtimeDir, "planner", fakeHarnessVersion), `{"claudeAiOauth":`)

	got := checkProfileCredentials()
	if got.Status != StatusWarn {
		t.Fatalf("status = %v, want warn (%+v)", got.Status, got)
	}
	if !strings.Contains(got.Summary, "unreadable") {
		t.Errorf("summary = %q, want it to name the unreadable credential", got.Summary)
	}
}

// A valid file in a shape this version does not recognize is forward
// compatibility, not a fault worth bricking a fleet over.
func TestCheckProfileCredentials_UnknownShapeWarns(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, fakeHarnessVersion)
	stageCredentials(t, stageProfile(t, runtimeDir, "planner", fakeHarnessVersion), `{"futureOauth":{"token":"x"}}`)

	if got := checkProfileCredentials(); got.Status != StatusWarn {
		t.Fatalf("status = %v, want warn", got.Status)
	}
}

// A hollow profile outranks an unreadable one: the fleet-level status is the
// worst fault present, and the definite fault is the one to act on.
func TestCheckProfileCredentials_HollowOutranksUnreadable(t *testing.T) {
	runtimeDir := stageProfileWorkspace(t, fakeHarnessVersion)
	stageCredentials(t, stageProfile(t, runtimeDir, "planner", fakeHarnessVersion), hollowCredentials)
	stageCredentials(t, stageProfile(t, runtimeDir, "tester", fakeHarnessVersion), `{`)

	got := checkProfileCredentials()
	if got.Status != StatusFail {
		t.Fatalf("status = %v, want fail (%+v)", got.Status, got)
	}
	if !strings.Contains(got.Summary, "1 of 2") {
		t.Errorf("summary = %q, want the hollow count", got.Summary)
	}
}

// Secret hygiene, asserted rather than reviewed: across every fixture class,
// nothing the check reports carries credential bytes.
func TestCheckProfileCredentials_DetailNeverLeaksTokenBytes(t *testing.T) {
	bodies := map[string]string{
		"hollow":    `{"claudeAiOauth":{"accessToken":"","refreshToken":"` + credentialSentinel + `"}}`,
		"populated": `{"claudeAiOauth":{"accessToken":"` + credentialSentinel + `"}}`,
		"malformed": `{"claudeAiOauth":{"accessToken":"` + credentialSentinel,
		"unknown":   `{"futureOauth":{"token":"` + credentialSentinel + `"}}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			runtimeDir := stageProfileWorkspace(t, fakeHarnessVersion)
			stageCredentials(t, stageProfile(t, runtimeDir, "planner", fakeHarnessVersion), body)

			got := checkProfileCredentials()
			if strings.Contains(got.Summary, "SENTINEL") || strings.Contains(got.Detail, "SENTINEL") {
				t.Fatalf("check leaked credential bytes:\nsummary: %s\ndetail: %s", got.Summary, got.Detail)
			}
		})
	}
}

// A check nobody runs reports nothing, so registration is part of the feature.
// Identity is compared by function pointer rather than by running the list:
// collectDoctorChecks also holds git, tmux and backend probes that have no
// business executing here.
func TestCheckProfileCredentials_IsRegistered(t *testing.T) {
	want := reflect.ValueOf(checkProfileCredentials).Pointer()
	for _, check := range collectDoctorChecks(nil) {
		if reflect.ValueOf(check).Pointer() == want {
			return
		}
	}
	t.Fatal("collectDoctorChecks does not include checkProfileCredentials")
}
