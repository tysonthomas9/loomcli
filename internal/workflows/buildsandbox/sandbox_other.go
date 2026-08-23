//go:build !darwin

package buildsandbox

import "errors"

var ErrUnavailable = errors.New("authoring_sandbox_unavailable")

func Mode(packageBuild bool) (string, error) {
	if packageBuild {
		return "", ErrUnavailable
	}
	return "none", nil
}
