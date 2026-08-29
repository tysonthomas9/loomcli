package agentprofile

import (
	"regexp"
	"strconv"
)

// semverRE finds the first dotted numeric triple in a harness --version line.
// The two shapes in the fleet are "2.1.251 (Claude Code)" and
// "codex-cli 0.149.1"; anchoring on the triple rather than on either layout
// keeps this working when a harness changes its banner.
var semverRE = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// ParseVersion returns the major/minor/patch of a harness --version line.
// ok is false when no triple is present.
func ParseVersion(s string) (major, minor, patch int, ok bool) {
	m := semverRE.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, 0, false
	}
	// Every submatch is \d+ by construction, so the only way Atoi fails is a
	// component too long for an int. That is not a version; treat it as
	// unparseable rather than silently truncating.
	nums := [3]int{}
	for i := range nums {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return 0, 0, 0, false
		}
		nums[i] = n
	}
	return nums[0], nums[1], nums[2], true
}

// SameMajorVersion reports whether two --version lines share a major
// component. It is FAIL-CLOSED: if either side has no parseable version, it
// returns false, so an unrecognized version shape refuses the spawn rather
// than being waved through as "probably a patch bump".
//
// MAJOR, not MAJOR.MINOR, and deliberately so. The two releases that genuinely
// broke turn detection (2.1.246, 2.1.247) were both patches, so a minor gate
// buys no safety; and `lead`'s codex root is pinned at 0.x (0.144.5 -> 0.149.1
// in weeks), where a MAJOR.MINOR gate would refuse lead's boot on every
// ordinary codex release — re-creating the outage this gate exists to end.
func SameMajorVersion(a, b string) bool {
	amaj, _, _, aok := ParseVersion(a)
	if !aok {
		return false
	}
	bmaj, _, _, bok := ParseVersion(b)
	if !bok {
		return false
	}
	return amaj == bmaj
}
