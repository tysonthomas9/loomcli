//go:build !unix

package filecoord

import "os"

func openRootedPlatform(_ string, _ os.FileInfo) (rootedPlatform, error) {
	return nil, nil
}
