//go:build !unix

package skillmat

import "errors"

var errUnsupportedPlatform = errors.New("skill materialization is not supported on this platform")

func ensurePlatformSupported() error { return errUnsupportedPlatform }

func ensureAtomicProjectionSupported() error { return errUnsupportedPlatform }

func openSecureRoot(string) (secureRoot, error) {
	return nil, errUnsupportedPlatform
}
