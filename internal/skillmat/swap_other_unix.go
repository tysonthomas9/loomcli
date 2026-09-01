//go:build unix && !darwin && !linux

package skillmat

import "errors"

func ensureAtomicProjectionSupported() error {
	return errors.New("atomic skill projection exchange is not supported on this platform")
}

func swapAt(_ int, _ string, _ int, _ string) error {
	return errors.New("atomic skill projection exchange is not supported on this platform")
}
