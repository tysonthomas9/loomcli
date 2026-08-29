package agentprofile

import "testing"

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name                string
		in                  string
		major, minor, patch int
		ok                  bool
	}{
		{"claude banner", "2.1.251 (Claude Code)", 2, 1, 251, true},
		{"codex banner", "codex-cli 0.149.1", 0, 149, 1, true},
		{"bare triple", "1.2.3", 1, 2, 3, true},
		{"empty", "", 0, 0, 0, false},
		{"no triple", "unknown", 0, 0, 0, false},
		{"two components only", "2.1 (Claude Code)", 0, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			maj, min, pat, ok := ParseVersion(tt.in)
			if ok != tt.ok {
				t.Fatalf("ParseVersion(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			}
			if !ok {
				return
			}
			if maj != tt.major || min != tt.minor || pat != tt.patch {
				t.Errorf("ParseVersion(%q) = %d.%d.%d, want %d.%d.%d",
					tt.in, maj, min, pat, tt.major, tt.minor, tt.patch)
			}
		})
	}
}

func TestSameMajorVersion(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"patch bump", "2.1.250 (Claude Code)", "2.1.251 (Claude Code)", true},
		{"downgrade is drift like any other", "2.1.243 (Claude Code)", "2.1.241 (Claude Code)", true},
		{"minor bump inside a major", "2.1.251 (Claude Code)", "2.9.0 (Claude Code)", true},
		{"major bump", "2.9.9 (Claude Code)", "3.0.0 (Claude Code)", false},
		{"codex 0.x minor bump stays same major", "codex-cli 0.144.5", "codex-cli 0.149.1", true},
		// Fail-closed: an unrecognized shape must refuse, never be waved
		// through as "probably a patch bump".
		{"empty observed", "2.1.251 (Claude Code)", "", false},
		{"empty manifest", "", "2.1.251 (Claude Code)", false},
		{"unparseable observed", "2.1.251 (Claude Code)", "unknown", false},
		{"both unparseable", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SameMajorVersion(tt.a, tt.b); got != tt.want {
				t.Errorf("SameMajorVersion(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
