//go:build darwin || linux

package capture

import "testing"

func TestAmbientXattrPolicy(t *testing.T) {
	for name, want := range map[string]bool{
		"com.apple.provenance":    true,
		"security.selinux":        true,
		"com.apple.quarantine":    false,
		"security.capability":     false,
		"system.posix_acl_access": false,
		"user.review-data":        false,
	} {
		if got := ambientXattr(name); got != want {
			t.Errorf("ambientXattr(%q) = %t, want %t", name, got, want)
		}
	}
}
