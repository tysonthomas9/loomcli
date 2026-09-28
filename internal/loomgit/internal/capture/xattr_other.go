//go:build !darwin && !linux

package capture

func countXattrs(string) (int, error) { return 0, nil }
