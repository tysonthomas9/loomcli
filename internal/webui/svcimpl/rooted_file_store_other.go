//go:build !unix

package svcimpl

import "os"

func openRootedPlatform(_ string, _ os.FileInfo) (rootedPlatform, error) {
	return nil, nil
}
