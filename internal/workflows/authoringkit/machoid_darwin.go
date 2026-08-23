//go:build darwin

package authoringkit

import (
	"fmt"
	"os/exec"
	"strings"
)

// MachOTeamID verifies the Mach-O at path carries a valid code signature and
// returns its TeamIdentifier. It shells to codesign — present on every macOS —
// rather than reimplementing CMS/Team-ID extraction. --verify --strict rejects a
// broken or absent signature before the identifier is trusted.
func MachOTeamID(path string) (string, error) {
	if out, err := exec.Command("/usr/bin/codesign", "--verify", "--strict", path).CombinedOutput(); err != nil { //nolint:gosec // path is a validated kit-relative file.
		return "", fmt.Errorf("codesign --verify: %v: %s", err, strings.TrimSpace(string(out)))
	}
	out, err := exec.Command("/usr/bin/codesign", "-dv", "--verbose=4", path).CombinedOutput() //nolint:gosec // path is a validated kit-relative file.
	if err != nil {
		return "", fmt.Errorf("codesign -dv: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "TeamIdentifier=")
		if !ok {
			continue
		}
		team := strings.TrimSpace(rest)
		if team == "" || team == "not set" {
			return "", fmt.Errorf("codesign: Mach-O has no Team ID")
		}
		return team, nil
	}
	return "", fmt.Errorf("codesign: TeamIdentifier not found")
}
