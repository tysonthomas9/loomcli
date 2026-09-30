package gitversion

import (
	_ "embed"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const ErrorCode = "git_version_unsupported"

//go:embed minimum.txt
var minimum string

var Minimum = strings.TrimSpace(minimum)

var pattern = regexp.MustCompile(`^git version (\d+)\.(\d+)(?:\.\d+)?(?:\s|$)`)

func Check(output string) error {
	version := strings.TrimSpace(output)
	matches := pattern.FindStringSubmatch(version)
	if matches != nil {
		major, _ := strconv.Atoi(matches[1])
		minor, _ := strconv.Atoi(matches[2])
		parts := strings.Split(Minimum, ".")
		requiredMajor, _ := strconv.Atoi(parts[0])
		requiredMinor, _ := strconv.Atoi(parts[1])
		if major > requiredMajor || major == requiredMajor && minor >= requiredMinor {
			return nil
		}
	}
	return fmt.Errorf("%s: Loom Git requires Git %s or newer (found %q)", ErrorCode, Minimum, version)
}
