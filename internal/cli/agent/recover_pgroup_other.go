//go:build !linux

package agent

func processGroupHasLiveMemberPlatform(int) (bool, bool) {
	return false, false
}
