//go:build !linux && !darwin

package agent

func processGroupHasLiveMemberPlatform(int) (bool, bool) {
	return false, false
}
