//go:build darwin

package buildsandbox

import (
	"errors"
	"os"
)

var ErrUnavailable = errors.New("authoring_sandbox_unavailable")

func Mode(packageBuild bool) (string, error) {
	if _, e := os.Stat("/usr/bin/sandbox-exec"); e != nil {
		if packageBuild {
			return "", ErrUnavailable
		}
		return "none", nil
	}
	return "seatbelt", nil
}
