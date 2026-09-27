package runtimepreflight

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/fleetdbcap"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// FleetDBPreflightTimeout bounds the capability probe. A fleet-db that accepts
// the connection and then never answers must not be able to wedge daemon
// startup; the timeout is reported as Unreachable, like any other transport
// failure.
const FleetDBPreflightTimeout = 5 * time.Second

// PreflightModeEnvVar names the environment variable that governs what happens
// when the capability endpoint itself is missing.
const PreflightModeEnvVar = "LOOM_FLEETDB_PREFLIGHT"

// Mode governs the Unverified case only — a fleet-db that predates capability
// reporting. Every other outcome is mode-independent: a missing required route
// is fatal and an unreachable server is not, whatever the mode.
type Mode string

const (
	// ModeWarn logs that compatibility could not be verified and continues.
	// This is the default for one release, so that rolling out a loom with
	// preflight does not require rolling out fleet-db first.
	ModeWarn Mode = "warn"
	// ModeStrict treats an unverifiable fleet-db as incompatible.
	ModeStrict Mode = "strict"
	// ModeOff skips the preflight entirely — the escape hatch for local and
	// development fleet-dbs.
	ModeOff Mode = "off"
)

// ModeFromEnv reads LOOM_FLEETDB_PREFLIGHT. Anything unrecognized (including
// unset and empty) is ModeWarn: an operator typo must not turn a boot into an
// exit, and must not silently disable the check either.
func ModeFromEnv() Mode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(PreflightModeEnvVar))) {
	case string(ModeStrict):
		return ModeStrict
	case string(ModeOff):
		return ModeOff
	default:
		return ModeWarn
	}
}

// Outcome is the result class of a fleet-db capability preflight. The values
// are distinct because the operator response differs for each: Incompatible
// means deploy something else, Unreachable means wait or check the network,
// Unverified means the server is too old to say.
type Outcome string

const (
	// OutcomeSkipped means the preflight did not run (ModeOff, or no store).
	OutcomeSkipped Outcome = "skipped"
	// OutcomeCompatible means every Required capability is present.
	OutcomeCompatible Outcome = "compatible"
	// OutcomeDegraded means only Degrades capabilities are absent.
	OutcomeDegraded Outcome = "degraded"
	// OutcomeIncompatible means at least one Required capability is absent.
	OutcomeIncompatible Outcome = "incompatible"
	// OutcomeUnreachable means the capability document could not be read:
	// dial failure, timeout, a server error, or a body that did not parse.
	OutcomeUnreachable Outcome = "unreachable"
	// OutcomeUnverified means the capability endpoint itself is absent — a
	// fleet-db predating capability reporting.
	OutcomeUnverified Outcome = "unverified"
)

// Report is the full result of one preflight, carrying everything the boot
// message and the banner need without a second round trip.
type Report struct {
	Outcome           Outcome
	FleetDBCommit     string
	FleetDBAPIVersion int
	// Endpoint is the fleet-db base URL, for the human-readable message. It
	// is informational and may be empty.
	Endpoint string
	// LoomBuild identifies this loom build in the message.
	LoomBuild string
	// Missing lists the requirements the server does not advertise, in
	// manifest order. Empty for every outcome but Degraded and Incompatible.
	Missing []fleetdbcap.Requirement
	// Err is the transport or decode error behind Unreachable/Unverified.
	Err error
}

// Fatal reports whether the daemon must refuse to start.
//
// Only Incompatible is fatal. Unreachable deliberately is not: the store
// already retries, and turning a transient blip into a manual restart trades
// one outage class for another.
func (r Report) Fatal() bool { return r.Outcome == OutcomeIncompatible }

// CheckFleetDB reads the server's capability document and compares it against
// reqs.
//
// caps may be nil (no capability store wired) — that is Skipped, not a
// failure. The comparison is a subset test in one direction only: a fleet-db
// advertising capabilities loom has never heard of is compatible.
func CheckFleetDB(ctx context.Context, caps store.CapabilityStore, reqs []fleetdbcap.Requirement, mode Mode) Report {
	report := Report{LoomBuild: loomBuildID()}

	if mode == ModeOff || caps == nil {
		report.Outcome = OutcomeSkipped
		return report
	}

	probeCtx, cancel := context.WithTimeout(ctx, FleetDBPreflightTimeout)
	defer cancel()

	doc, err := caps.Get(probeCtx)
	if err != nil {
		report.Err = err
		if errors.Is(err, domain.ErrCapabilityEndpointUnsupported) {
			report.Outcome = OutcomeUnverified
			if mode == ModeStrict {
				report.Outcome = OutcomeIncompatible
			}
			return report
		}
		// Everything else — dial refused, timeout, 5xx, an unparseable
		// 200 — is a failure to READ the answer, never an answer. It must
		// not be read as "no capabilities present".
		report.Outcome = OutcomeUnreachable
		return report
	}

	report.FleetDBCommit = doc.Commit
	report.FleetDBAPIVersion = doc.APIVersion

	// api_version is deliberately not compared: the capability set is
	// authoritative and a version bump that keeps serving the same routes is
	// not an incompatibility.
	fatal := false
	for _, r := range reqs {
		if doc.Has(r.Capability) {
			continue
		}
		report.Missing = append(report.Missing, r)
		if r.Level == fleetdbcap.Required {
			fatal = true
		}
	}

	switch {
	case fatal:
		report.Outcome = OutcomeIncompatible
	case len(report.Missing) > 0:
		report.Outcome = OutcomeDegraded
	default:
		report.Outcome = OutcomeCompatible
	}
	return report
}

// missingByLevel splits Missing without allocating for the empty cases.
func (r Report) missingByLevel(level fleetdbcap.Level) []fleetdbcap.Requirement {
	var out []fleetdbcap.Requirement
	for _, m := range r.Missing {
		if m.Level == level {
			out = append(out, m)
		}
	}
	return out
}

// Message renders the operator-facing text for this report: the fatal stderr
// block for Incompatible, and a one-line warning for the non-fatal outcomes.
// Compatible and Skipped render as "" — a clean boot says nothing new.
//
//nolint:funlen // One rendering table; splitting it hides the message shape.
func (r Report) Message() string {
	switch r.Outcome {
	case OutcomeCompatible, OutcomeSkipped:
		return ""
	case OutcomeUnreachable:
		return fmt.Sprintf("Warning: could not verify fleet-db compatibility (reason=unreachable): %v\n"+
			"  Continuing; the store retries. Set %s=off to skip this check.", r.Err, PreflightModeEnvVar)
	case OutcomeUnverified:
		return fmt.Sprintf("Warning: fleet-db predates capability reporting; compatibility unverified (reason=unverified).\n"+
			"  Continuing under %s=warn. Set %s=strict to refuse to start instead.",
			PreflightModeEnvVar, PreflightModeEnvVar)
	}

	var b strings.Builder
	if r.Outcome == OutcomeIncompatible && r.Err != nil {
		// Strict mode over a pre-capability fleet-db: there is no capability
		// list to itemize, so name the reason instead.
		fmt.Fprintf(&b, "Error: fleet-db is not compatible with this loom build (reason=incompatible).\n\n")
		fmt.Fprintf(&b, "  fleet-db predates capability reporting and %s=strict refuses an unverified server.\n", PreflightModeEnvVar)
		fmt.Fprintf(&b, "  %v\n\n", r.Err)
		b.WriteString(bypassAdvice)
		return b.String()
	}

	if r.Outcome == OutcomeIncompatible {
		b.WriteString("Error: fleet-db is not compatible with this loom build (reason=incompatible).\n\n")
	} else {
		b.WriteString("Warning: fleet-db is missing routes this loom build uses (reason=degraded).\n\n")
	}
	fmt.Fprintf(&b, "  loom build:  %s\n", r.LoomBuild)
	fmt.Fprintf(&b, "  fleet-db:    %s\n\n", fleetDBIdentity(r))

	writeMissingSection(&b, "Missing, required:", r.missingByLevel(fleetdbcap.Required))
	writeMissingSection(&b, "Missing, degraded:", r.missingByLevel(fleetdbcap.Degrades))

	if r.Outcome == OutcomeIncompatible {
		b.WriteString(bypassAdvice)
	}
	return strings.TrimRight(b.String(), "\n")
}

const bypassAdvice = "  Deploy a fleet-db that serves the required routes, or run a loom build that does not\n" +
	"  require them. Set " + PreflightModeEnvVar + "=off to bypass this check.\n"

func writeMissingSection(b *strings.Builder, heading string, reqs []fleetdbcap.Requirement) {
	if len(reqs) == 0 {
		return
	}
	fmt.Fprintf(b, "  %s\n", heading)
	for _, r := range reqs {
		fmt.Fprintf(b, "    - %-30s %s\n", r.Capability, r.Route)
		fmt.Fprintf(b, "        needed by: %s\n", r.Feature)
		if r.DegradedEffect != "" {
			fmt.Fprintf(b, "        effect:    %s\n", r.DegradedEffect)
		}
	}
	b.WriteString("\n")
}

func fleetDBIdentity(r Report) string {
	commit := r.FleetDBCommit
	if strings.TrimSpace(commit) == "" {
		commit = "(commit unknown)"
	}
	detail := fmt.Sprintf("api_version %d", r.FleetDBAPIVersion)
	if strings.TrimSpace(r.Endpoint) != "" {
		detail += ", " + r.Endpoint
	}
	return fmt.Sprintf("%s  (%s)", commit, detail)
}

// BannerLines renders the report as banner lines for a daemon that is starting
// anyway. Empty for a clean (or fatal) report: a fatal one never reaches a
// banner.
//
// It returns lines rather than printing, so internal/cli can show the block
// without importing this package.
func (r Report) BannerLines() []string {
	if r.Fatal() {
		return nil
	}
	msg := r.Message()
	if msg == "" {
		return nil
	}
	if r.Outcome == OutcomeDegraded {
		return append([]string{"Degraded:"}, strings.Split(msg, "\n")...)
	}
	return strings.Split(msg, "\n")
}

// LogAttrs returns the report as flat key/value pairs for a structured log
// line. `reason` is the field that separates unreachable from incompatible.
func (r Report) LogAttrs() []any {
	names := make([]string, 0, len(r.Missing))
	for _, m := range r.Missing {
		names = append(names, m.Capability)
	}
	attrs := []any{
		"reason", string(r.Outcome),
		"fleetdb_commit", r.FleetDBCommit,
		"fleetdb_api_version", r.FleetDBAPIVersion,
		"loom_build", r.LoomBuild,
		"missing", strings.Join(names, ","),
	}
	if r.Err != nil {
		attrs = append(attrs, "error", r.Err.Error())
	}
	return attrs
}

// loomBuildID identifies this loom build for the boot message. LOOM_BUILD_SHA
// is set by the deploy tooling; without it the message says so rather than
// inventing a version.
func loomBuildID() string {
	if sha := strings.TrimSpace(os.Getenv("LOOM_BUILD_SHA")); sha != "" {
		return sha
	}
	return "(build sha unknown)"
}
