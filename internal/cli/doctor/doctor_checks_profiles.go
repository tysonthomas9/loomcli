package doctor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

// profileProbeTimeout bounds one "<harness> --version" fork. The binary is a
// node launcher: a couple of seconds cold, so this is generous, and a probe
// that times out reports StatusWarn rather than inventing drift.
const profileProbeTimeout = 10 * time.Second

// probeVersion is a seam, not indirection for its own sake: every test in this
// package must be able to state what the harness reports without `claude` being
// installed on the CI runner.
var probeVersion = agentprofile.ProbeVersion

// profileFault is one profile that did not verify, kept with the sentinel it
// failed on so the report can bucket by repair rather than by message text.
type profileFault struct {
	profile agentprofile.Profile
	err     error
	got     string // the version the harness reported, "" when unknown
}

// checkAgentProfiles verifies every provisioned harness profile under the
// workspace runtime dir against the harness binary actually on PATH.
//
// It walks the profile DIRECTORY rather than the daemon's agent roster, which
// is the point of running it here: `lead` is not supervisor-spawned, so no
// daemon-driven check ever sees its profile, and drift on `lead` surfaces only
// at the moment an operator tries to use it.
//
// This check stays STRICT while the spawn path does not: every drift is
// reported here, including the same-major drift a boot now proceeds through
// with a warning. That split is deliberate — the daemon decides what a boot
// does about a fault; `loom doctor` reports what is true.
//
// With --fix it re-blesses drift-only profiles. Two safety properties make that
// safe, and both are structural rather than conventional:
//
//   - agentprofile.Bless re-verifies the fingerprint before it writes, so --fix
//     cannot bless content it has not verified — a fingerprint mismatch is a
//     different fault with a different repair (re-provision) and stays unfixed.
//   - This code is reachable only from an operator-typed `loom doctor --fix`.
//     Nothing in the daemon re-blesses on its own; a harness upgrade must always
//     pass through a human.
//
// Blessing a running agent is safe: the manifest is read once per spawn and
// Bless rewrites one JSON field, touching no provisioned content, so the
// agent's next spawn simply succeeds. No stop, no re-provision, no restart.
func checkAgentProfiles() CheckResult {
	projectDir := cli.GetWorkspaceRuntimeDir()
	if projectDir == "" {
		return CheckResult{}
	}
	profiles, err := agentprofile.List(projectDir)
	if err != nil {
		return CheckResult{
			Name:    "agent_profiles",
			Status:  StatusWarn,
			Summary: "could not enumerate agent profiles",
			Detail:  err.Error(),
		}
	}
	// A fleet with no profiles sees no new output at all.
	if len(profiles) == 0 {
		return CheckResult{}
	}

	versions := probeHarnessVersions(profiles)

	var drifted, broken, unknown []profileFault
	for _, p := range profiles {
		got := versions[p.Harness]
		err := agentprofile.Verify(p.Dir, got)
		if err == nil {
			// Verify does not look at the credential at all — the token is
			// deliberately outside the manifest — so a profile with no
			// identity verified clean and doctor reported the fleet green
			// while its agents died on their first API call. Probe it here.
			err = checkProfileCredential(p)
		}
		switch {
		case err == nil:
		case errors.Is(err, agentprofile.ErrVersionDrift):
			drifted = append(drifted, profileFault{profile: p, err: err, got: got})
		case errors.Is(err, agentprofile.ErrVersionUnknown):
			unknown = append(unknown, profileFault{profile: p, err: err})
		default:
			broken = append(broken, profileFault{profile: p, err: err, got: got})
		}
	}

	var blessed []agentprofile.Profile
	if doctorFix && len(drifted) > 0 {
		// Every drifted profile leaves this call either blessed or broken:
		// there is no such thing as "still drifted" once --fix has run.
		blessed, broken = fixDriftedProfiles(drifted, broken, versions)
		drifted = nil
	}

	return renderProfileResult(profiles, versions, blessed, drifted, broken, unknown)
}

// checkProfileCredential reports why a profile's own credential cannot serve as
// an identity, or nil when the harness has none to carry or the one it has is
// usable. It is the doctor-side twin of supervisor.ProfileSecretEnv's refusal,
// and reuses that package's sentinels so the report buckets by the same repair
// the boot path would name.
//
// It never reads the token bytes: existence and a non-whitespace size are
// exactly what distinguishes "never minted" from "minted then broken", and a
// credential must not pass through a reporting path.
func checkProfileCredential(p agentprofile.Profile) error {
	path := supervisor.ProfileTokenPath(p.Dir, p.Harness)
	if path == "" {
		return nil // this harness carries no credential of its own
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", supervisor.ErrProfileTokenMissing, path)
		}
		return fmt.Errorf("%w: %s: %v", supervisor.ErrProfileTokenUnreadable, path, err)
	}
	// Size, not content: a token is never whitespace-only, and anything at or
	// below the size of a stray newline cannot be one.
	if info.Size() <= 1 {
		return fmt.Errorf("%w: %s: file is empty", supervisor.ErrProfileTokenUnreadable, path)
	}
	return nil
}

// probeHarnessVersions forks "<binary> --version" once per DISTINCT harness,
// not once per profile: four agents on claude is one node startup, not four.
func probeHarnessVersions(profiles []agentprofile.Profile) map[string]string {
	versions := make(map[string]string, 2)
	for _, p := range profiles {
		if _, done := versions[p.Harness]; done {
			continue
		}
		binary, ok := agentprofile.HarnessBinary[p.Harness]
		if !ok {
			binary = p.Harness
		}
		versions[p.Harness] = probeVersion(binary, profileProbeTimeout)
	}
	return versions
}

// fixDriftedProfiles re-blesses each drift-only profile and re-verifies it.
// A profile whose Bless or re-Verify fails moves to broken, so a failed repair
// is reported and keeps the check failing rather than being silently dropped.
func fixDriftedProfiles(drifted, broken []profileFault, versions map[string]string) ([]agentprofile.Profile, []profileFault) {
	var blessed []agentprofile.Profile
	for _, f := range drifted {
		got := versions[f.profile.Harness]
		if err := agentprofile.Bless(f.profile.Dir, got); err != nil {
			broken = append(broken, profileFault{profile: f.profile, err: err, got: got})
			continue
		}
		if err := agentprofile.Verify(f.profile.Dir, got); err != nil {
			broken = append(broken, profileFault{profile: f.profile, err: err, got: got})
			continue
		}
		blessed = append(blessed, f.profile)
	}
	return blessed, broken
}

// renderProfileResult maps the buckets onto one CheckResult. StatusFail is what
// makes runDoctor exit non-zero, which is what lets a wrapper script or a cron
// treat "the fleet is bricked" as a failing command.
func renderProfileResult(profiles []agentprofile.Profile, versions map[string]string,
	blessed []agentprofile.Profile, drifted, broken, unknown []profileFault) CheckResult {
	total := len(profiles)

	switch {
	case len(broken) > 0:
		// Fingerprint mismatches, missing and unreadable manifests are reported
		// unfixed and keep the check failing even under --fix.
		faults := append(append([]profileFault{}, drifted...), broken...)
		return CheckResult{
			Name:    "agent_profiles",
			Status:  StatusFail,
			Summary: fmt.Sprintf("%d of %d agent profile(s) failed verification", len(faults), total),
			Detail:  strings.Join(append(blessedLines(blessed, versions), faultLines(faults)...), "\n"),
		}
	case len(blessed) > 0:
		// Something was written, so the operator should see it — but the fleet
		// is no longer broken, so the command exits 0.
		return CheckResult{
			Name:    "agent_profiles",
			Status:  StatusWarn,
			Summary: fmt.Sprintf("Re-blessed %d of %d agent profile(s) for %s", len(blessed), total, versionSummary(versions)),
			Detail:  strings.Join(blessedLines(blessed, versions), "\n"),
		}
	case len(drifted) > 0:
		// Still StatusFail, and deliberately: an unverified fleet is a
		// condition an operator must act on. But the wording no longer says
		// the fleet is DOWN, because since the spawn path softened same-major
		// drift to a warning it is not — these agents boot and run.
		return CheckResult{
			Name:    "agent_profiles",
			Status:  StatusFail,
			Summary: fmt.Sprintf("%d of %d agent profile(s) running UNVERIFIED against %s", len(drifted), total, versionSummary(versions)),
			Detail: strings.Join(append([]string{
				"manifests pin an older version; agents boot with a warning (drift is no longer fatal).",
				"harness-wrapper's verified pin is in pkg/versions/versions.json.",
				"`loom doctor --fix` re-blesses once verification has actually been done.",
			}, faultLines(drifted)...), "\n"),
		}
	case len(unknown) > 0:
		return CheckResult{
			Name:    "agent_profiles",
			Status:  StatusWarn,
			Summary: fmt.Sprintf("cannot verify: %s --version produced nothing", unknownBinaries(unknown)),
			Detail:  strings.Join(faultLines(unknown), "\n"),
		}
	default:
		// Pass, but scoped. Doctor enumerates a directory with its own
		// uncached probe; the supervisor verifies one profile at spawn time
		// from its own project dir. Saying so is what stops a green line here
		// from silently contradicting a supervisor that is refusing every
		// spawn for drift against that same binary.
		return CheckResult{
			Name:    "agent_profiles",
			Status:  StatusPass,
			Summary: fmt.Sprintf("%d agent profile(s) verified against %s", total, versionSummary(versions)),
			Detail:  profileScopeLine(profiles, versions),
		}
	}
}

// profileScopeLine states exactly what this check looked at, so its verdict is
// comparable with the supervisor's rather than assumed to agree with it.
func profileScopeLine(profiles []agentprofile.Profile, versions map[string]string) string {
	root := "the agent-profiles directory"
	if len(profiles) > 0 {
		// profile.Dir is <root>/<agent>/<harness>; two levels up is the root.
		root = displayDir(filepath.Dir(filepath.Dir(profiles[0].Dir)))
	}
	return fmt.Sprintf("scope: %d profile(s) under %s, probed with doctor's own %s; "+
		"the supervisor verifies each profile at spawn time from its own project dir "+
		"and may see a different set.", len(profiles), root, versionSummary(versions))
}

// blessedLines reports what --fix wrote, naming the agents so the operator can
// see which spawns just became viable again.
func blessedLines(blessed []agentprofile.Profile, versions map[string]string) []string {
	if len(blessed) == 0 {
		return nil
	}
	names := make([]string, 0, len(blessed))
	for _, p := range blessed {
		names = append(names, fmt.Sprintf("%s (%s %s)", p.Agent, p.Harness, versions[p.Harness]))
	}
	return []string{fmt.Sprintf("re-blessed: %s: harness_version updated; no restart needed",
		strings.Join(names, ", "))}
}

// faultLines renders one block per failing profile: the agent, its directory,
// what is wrong, and the exact repair. The operator reading this is the same
// person who otherwise spends an hour reading metadata by hand.
func faultLines(faults []profileFault) []string {
	sorted := append([]profileFault{}, faults...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].profile.Agent != sorted[j].profile.Agent {
			return sorted[i].profile.Agent < sorted[j].profile.Agent
		}
		return sorted[i].profile.Harness < sorted[j].profile.Harness
	})

	var out []string
	for _, f := range sorted {
		out = append(out,
			fmt.Sprintf("%s  %s", f.profile.Agent, displayDir(f.profile.Dir)),
			"    "+faultReason(f),
			"    repair: "+faultRepair(f))
	}
	return out
}

// faultReason states the fault in the operator's terms. Version drift carries
// BOTH version strings, because "stale" without the two numbers is not
// actionable.
func faultReason(f profileFault) string {
	binary := f.profile.Harness
	if b, ok := agentprofile.HarnessBinary[f.profile.Harness]; ok {
		binary = b
	}
	switch {
	case errors.Is(f.err, agentprofile.ErrVersionDrift):
		return fmt.Sprintf("manifest pins %q, %s reports %q", pinnedVersion(f.profile.Dir), binary, f.got)
	case errors.Is(f.err, agentprofile.ErrVersionUnknown):
		return fmt.Sprintf("cannot verify: %s --version produced nothing", binary)
	case errors.Is(f.err, agentprofile.ErrFingerprintMismatch):
		return "fingerprint mismatch: " + fingerprintPair(f.profile.Dir)
	case errors.Is(f.err, supervisor.ErrProfileTokenMissing):
		return "no oauth-token: profile was never minted"
	case errors.Is(f.err, supervisor.ErrProfileTokenUnreadable):
		return "oauth-token unusable: " + f.err.Error()

	case errors.Is(f.err, agentprofile.ErrManagedContentDrift):
		// The error already names the file and the dotted JSON path of the
		// divergence, which is the whole operator-facing value; restating it
		// here would only lose the path.
		return f.err.Error()
	case errors.Is(f.err, agentprofile.ErrManifestMissing):
		return "no " + agentprofile.ManifestName + ": profile dir exists but was never provisioned"
	default:
		return f.err.Error()
	}
}

// faultRepair names the one command that fixes this fault. --fix is offered
// only where --fix actually applies; everything else routes to the operator's
// provisioner, which is the only thing allowed to change profile content.
func faultRepair(f profileFault) string {
	if errors.Is(f.err, agentprofile.ErrVersionDrift) {
		return "loom doctor --fix   (re-blesses the pin once verified; agents are already running)"
	}
	if errors.Is(f.err, supervisor.ErrProfileTokenMissing) {
		return fmt.Sprintf("scripts/setup-profile-token.sh %s   (interactive, then provision-profile.sh %s)",
			f.profile.Agent, f.profile.Agent)
	}
	if errors.Is(f.err, agentprofile.ErrVersionUnknown) {
		return fmt.Sprintf("install or PATH-expose the %s binary, then re-run loom doctor", f.profile.Harness)
	}
	return fmt.Sprintf("scripts/provision-profile.sh %s   (--fix will not touch this)", f.profile.Agent)
}

// pinnedVersion re-reads the manifest for the report. It is a second read of a
// small file on an already-failing path, which is cheaper than threading the
// manifest through every bucket for the common case where nothing failed.
func pinnedVersion(dir string) string {
	m, err := agentprofile.LoadManifest(dir)
	if err != nil {
		return "(unreadable)"
	}
	return m.HarnessVersion
}

// fingerprintPair renders manifest-vs-disk for a mismatch, recomputed through
// agentprofile.Fingerprint so the report can never disagree with the verifier.
func fingerprintPair(dir string) string {
	m, err := agentprofile.LoadManifest(dir)
	if err != nil {
		return "manifest unreadable"
	}
	sum, err := agentprofile.Fingerprint(dir, m.Files)
	if err != nil {
		return fmt.Sprintf("manifest %s, on disk unreadable (%v)", shortFingerprint(m.Fingerprint), err)
	}
	return fmt.Sprintf("manifest %s, on disk %s", shortFingerprint(m.Fingerprint), shortFingerprint(sum))
}

func shortFingerprint(s string) string {
	if s == "" {
		return "(empty)"
	}
	if len(s) > 12 {
		return s[:12] + "…"
	}
	return s
}

// displayDir trims the workspace runtime dir off the front, so the report reads
// as .loom/agent-profiles/observer/claude rather than an absolute path that
// wraps the terminal.
func displayDir(dir string) string {
	root := cli.GetWorkspaceRuntimeDir()
	if root == "" {
		return dir
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return dir
	}
	return rel
}

// versionSummary names the version(s) the profiles were compared against: bare
// for a single harness, harness-qualified when a fleet runs both.
func versionSummary(versions map[string]string) string {
	harnesses := make([]string, 0, len(versions))
	for h := range versions {
		harnesses = append(harnesses, h)
	}
	sort.Strings(harnesses)

	if len(harnesses) == 1 {
		v := versions[harnesses[0]]
		if v == "" {
			return harnesses[0] + " (version unknown)"
		}
		return v
	}
	parts := make([]string, 0, len(harnesses))
	for _, h := range harnesses {
		v := versions[h]
		if v == "" {
			v = "(version unknown)"
		}
		parts = append(parts, h+" "+v)
	}
	return strings.Join(parts, ", ")
}

// unknownBinaries lists the distinct binaries that produced no version, so the
// summary line names what to install.
func unknownBinaries(unknown []profileFault) string {
	seen := make(map[string]bool, len(unknown))
	var out []string
	for _, f := range unknown {
		binary := f.profile.Harness
		if b, ok := agentprofile.HarnessBinary[f.profile.Harness]; ok {
			binary = b
		}
		if seen[binary] {
			continue
		}
		seen[binary] = true
		out = append(out, binary)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
