package loomharness

import (
	"fmt"
	"regexp"
	"strconv"
)

// Version is a major.minor.patch harness version.
type Version struct{ Major, Minor, Patch int }

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Less reports whether v sorts before o.
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	return v.Patch < o.Patch
}

var versionRE = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// ParseVersion reads the first x.y.z in a `--version` output, such as
// "codex-cli 0.157.1" or "2.1.285 (Claude Code)".
func ParseVersion(s string) (Version, error) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("loomharness: no version in %q", s)
	}
	var n [3]int
	for i := range n {
		v, err := strconv.Atoi(m[i+1])
		if err != nil {
			return Version{}, fmt.Errorf("loomharness: version %q: %w", s, err)
		}
		n[i] = v
	}
	return Version{n[0], n[1], n[2]}, nil
}

// VersionGate is a harness's minimum and last contract-tested version.
type VersionGate struct{ Minimum, Tested Version }

// Versions is the R21 table. Bump Tested after the contract tests pass on a
// new release; raise Minimum only when an adapter relies on a newer feature.
var Versions = map[string]VersionGate{
	"opencode": {Minimum: Version{2, 0, 19}, Tested: Version{2, 0, 19}},
	"codex":    {Minimum: Version{0, 157, 1}, Tested: Version{0, 157, 1}},
	"claude":   {Minimum: Version{2, 1, 285}, Tested: Version{2, 1, 285}},
}

// VersionCheck is the result of a startup version check.
type VersionCheck struct {
	Harness   string
	Installed Version
	Gate      VersionGate
	Newer     bool // installed is newer than the last tested version: warn only
}

// Warning is the Health warning for a newer-than-tested harness, or "".
func (c VersionCheck) Warning() string {
	if !c.Newer {
		return ""
	}
	return fmt.Sprintf("%s %s is newer than the last tested %s", c.Harness, c.Installed, c.Gate.Tested)
}

// TooOldError refuses a harness below its minimum version.
type TooOldError struct {
	Harness            string
	Installed, Minimum Version
}

func (e *TooOldError) Error() string {
	return fmt.Sprintf("harness_too_old: %s %s is below the minimum %s; upgrade %s",
		e.Harness, e.Installed, e.Minimum, e.Harness)
}

// Unwrap lets errors.Is(err, ErrUnavailable) match a refused harness.
func (e *TooOldError) Unwrap() error { return ErrUnavailable }

// CheckVersion parses a harness's `--version` output and applies its gate.
// It returns *TooOldError below the minimum and sets Newer above Tested.
func CheckVersion(harness, output string) (VersionCheck, error) {
	gate, ok := Versions[harness]
	if !ok {
		return VersionCheck{}, fmt.Errorf("loomharness: unknown harness %q", harness)
	}
	v, err := ParseVersion(output)
	if err != nil {
		return VersionCheck{}, err
	}
	c := VersionCheck{Harness: harness, Installed: v, Gate: gate, Newer: gate.Tested.Less(v)}
	if v.Less(gate.Minimum) {
		return c, &TooOldError{Harness: harness, Installed: v, Minimum: gate.Minimum}
	}
	return c, nil
}
