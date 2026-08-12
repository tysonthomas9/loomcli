package middleware

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/fileaccess"
)

// TestKnownFileRole locks in which role names the file-browser capability check
// recognizes, including aliases and normalization (case + surrounding/embedded
// whitespace). Configuration surfaces rely on this to reject a typo at startup
// rather than shipping a resolver that 403s every request at serve time.
func TestKnownFileRole(t *testing.T) {
	known := []string{
		"admin", "owner", "maintainer",
		"editor", "developer", "dev",
		"viewer", "read_only", "readonly", "read-only",
		// normalization: case and whitespace must not change recognition.
		"ADMIN", "  Viewer  ", "Read Only", "read only",
	}
	for _, role := range known {
		if !KnownFileRole(role) {
			t.Errorf("KnownFileRole(%q) = false, want true", role)
		}
	}

	unknown := []string{
		"", "   ", "superuser", "guest", "none", "reader",
		"view", "write", "edit", "read-write",
	}
	for _, role := range unknown {
		if KnownFileRole(role) {
			t.Errorf("KnownFileRole(%q) = true, want false", role)
		}
	}
}

// TestFileCapabilitiesForRole verifies the role -> capability mapping for every
// recognized alias, including the aliases the HTTP-layer test does not exercise
// (owner, maintainer, developer, dev, and the read-only spellings), plus the
// fail-closed default for an unrecognized role.
func TestFileCapabilitiesForRole(t *testing.T) {
	readWrite := fileaccess.Capabilities{Read: true, Write: true, Sensitive: true}
	readOnly := fileaccess.Capabilities{Read: true}

	for _, tc := range []struct {
		role   string
		want   fileaccess.Capabilities
		wantOK bool
	}{
		{role: "admin", want: readWrite, wantOK: true},
		{role: "owner", want: readWrite, wantOK: true},
		{role: "maintainer", want: readWrite, wantOK: true},
		{role: "editor", want: readWrite, wantOK: true},
		{role: "developer", want: readWrite, wantOK: true},
		{role: "dev", want: readWrite, wantOK: true},
		{role: "viewer", want: readOnly, wantOK: true},
		{role: "read_only", want: readOnly, wantOK: true},
		{role: "readonly", want: readOnly, wantOK: true},
		{role: "read-only", want: readOnly, wantOK: true},
		{role: "Read Only", want: readOnly, wantOK: true}, // normalized to read_only
		{role: "unknown", want: fileaccess.Capabilities{}, wantOK: false},
		{role: "", want: fileaccess.Capabilities{}, wantOK: false},
	} {
		t.Run(tc.role, func(t *testing.T) {
			got, ok := fileCapabilitiesForRole(tc.role)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("fileCapabilitiesForRole(%q) = (%+v, %v), want (%+v, %v)",
					tc.role, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestViewerRoleIsReadOnly guards the security-critical invariant that the
// read-only roles never grant Write or Sensitive access.
func TestViewerRoleIsReadOnly(t *testing.T) {
	for _, role := range []string{"viewer", "read_only", "readonly", "read-only"} {
		caps, ok := fileCapabilitiesForRole(role)
		if !ok {
			t.Fatalf("fileCapabilitiesForRole(%q) not recognized", role)
		}
		if !caps.Read {
			t.Errorf("role %q: Read = false, want true", role)
		}
		if caps.Write || caps.Sensitive {
			t.Errorf("role %q: got Write=%v Sensitive=%v, want both false", role, caps.Write, caps.Sensitive)
		}
	}
}
