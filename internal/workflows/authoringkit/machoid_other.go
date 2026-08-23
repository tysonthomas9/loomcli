//go:build !darwin

package authoringkit

import "fmt"

// MachOTeamID cannot verify a code signature off macOS, so a Team-ID-bound
// Mach-O entry fails closed on other platforms.
func MachOTeamID(path string) (string, error) {
	return "", fmt.Errorf("Mach-O Team ID verification requires macOS")
}
