//go:build !darwin && !linux

package local

import "errors"

const processExecutableInspectionSupported = false

var processExecutablePathFn = processExecutablePath

func processExecutablePath(int) (string, error) {
	return "", errors.New("process executable inspection is unsupported")
}
