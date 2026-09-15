//go:build !unix

package doctor

// defaultDiskUsage is not implemented off unix; checkDiskHeadroom skips.
func defaultDiskUsage(string) (free, total uint64, err error) {
	return 0, 0, errDiskUsageUnsupported
}
