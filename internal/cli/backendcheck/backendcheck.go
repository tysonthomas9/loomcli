// Package backendcheck is the single seam between loom code and the
// in-tree harness CLI discovery primitive. It exists as its own package
// (rather than a var in internal/cli) to keep internal/cli's import
// fanout and file count under the project's per-package gates.
package backendcheck

import (
	"fmt"
	"regexp"

	"github.com/olesho/harness-wrapper/pkg/discovery"
	"github.com/olesho/harness-wrapper/pkg/versions"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
)

// CheckBackend reports whether a named backend is installed and, when a
// version probe is registered, at what version. Registered Loom backends
// with health checks are authoritative; raw CLI names fall back to
// harness-wrapper PATH discovery.
//
// Production code reads CheckBackend; tests reassign it to inject a
// fake without spawning subprocesses or mutating PATH:
//
//	prev := backendcheck.CheckBackend
//	backendcheck.CheckBackend = func(string) (discovery.Info, error) { ... }
//	t.Cleanup(func() { backendcheck.CheckBackend = prev })
var CheckBackend = lookupBackend

func lookupBackend(name string) (discovery.Info, error) {
	if hs, ok := backends.CheckBackendHealth(name); ok {
		info := discovery.Info{
			Name:            name,
			Binary:          name,
			Installed:       hs.Installed,
			DetectedVersion: hs.Version,
			// Default true so no caller reads "unknown" as drift;
			// applyVersionPin flips it only on a real mismatch.
			VersionMatchesPin: true,
		}
		applyVersionPin(&info, name, hs.Version)
		if !hs.Installed {
			if hs.Message != "" {
				info.InstallHint = hs.Message
			} else {
				info.InstallHint = fmt.Sprintf("%q not on PATH", name)
			}
		}
		return info, nil
	}
	return discovery.Lookup(name)
}

// backendHarnessKeys maps a registered Loom backend name to its harness key
// in harness-wrapper's versions.json.
//
// harness-wrapper already owns this reverse mapping — discovery.Lookup walks
// versions.All() matching each entry's Binary against the requested name —
// but the health-check short-circuit above never reaches Lookup, so the
// mapping has to be restated here. It cannot be collapsed into "the backend
// name is the binary name": Loom's registry is its own namespace. "claude" is
// the harness keyed "claude-code", "cursor" probes a binary called
// "cursor-agent", and ExternalBackend registers under a user-chosen name.
//
// Backends absent from this table (cursor, gemini, echo, external) have no
// versions.json entry at all, so they carry no pin and can never report drift.
var backendHarnessKeys = map[string]string{
	backendnames.Claude: "claude-code",
	backendnames.Codex:  "codex",
	"opencode":          "opencode",
}

// semverRe matches the first X.Y.Z token in a string, optionally carrying a
// "-pre.release" or "+build" suffix. It deliberately mirrors the expression
// harness-wrapper's own version probe uses, so the pin and the detected
// version are reduced by the same rule before being compared.
var semverRe = regexp.MustCompile(`\d+\.\d+\.\d+(?:[-+][\w.]+)?`)

// applyVersionPin fills in the versions.json-sourced fields of info —
// Harness, PinnedVersion, NPMPackage — and decides VersionMatchesPin.
//
// It reproduces discovery.Lookup's contract exactly: unknown is never drift.
// A backend with no versions.json entry, an entry with a deliberately empty
// pin (opencode), or a version line with no semver in it all leave
// VersionMatchesPin at its true default. Only two known, non-empty, unequal
// versions flip it to false.
func applyVersionPin(info *discovery.Info, name, detected string) {
	harness, ok := backendHarnessKeys[name]
	if !ok {
		return
	}
	all, err := versions.All()
	if err != nil {
		// versions.json is embedded into harness-wrapper at build time, so a
		// parse failure means a broken build rather than a runtime condition.
		// The caller asked whether a backend is installed and that answer is
		// still sound without the pin metadata, so degrade instead of erroring.
		return
	}
	entry, ok := all[harness]
	if !ok {
		return
	}
	info.Harness = harness
	info.PinnedVersion = entry.Pinned
	info.NPMPackage = entry.Package

	if entry.Pinned == "" {
		return
	}
	// Loom's health probe reports the whole first line of `<binary> --version`
	// ("2.1.201 (Claude Code)", "codex-cli 0.142.5") whereas the pin is a bare
	// semver. Comparing the raw strings would report drift for every backend.
	got := semverRe.FindString(detected)
	if got == "" {
		return
	}
	info.VersionMatchesPin = got == entry.Pinned
}
