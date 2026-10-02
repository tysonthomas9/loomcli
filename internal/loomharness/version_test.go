package loomharness

import (
	"errors"
	"testing"
)

func TestVersionParse(t *testing.T) {
	for in, want := range map[string]Version{
		"codex-cli 0.157.1":      {0, 157, 1},
		"2.1.285 (Claude Code)":  {2, 1, 285},
		"opencode 2.0.19-dev+ab": {2, 0, 19},
	} {
		got, err := ParseVersion(in)
		if err != nil || got != want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseVersion("unknown"); err == nil {
		t.Error("ParseVersion(unknown): want error")
	}
}

func TestVersionGate(t *testing.T) {
	cases := []struct {
		harness, out  string
		tooOld, newer bool
	}{
		{"opencode", "1.18.32", true, false},
		{"opencode", "2.0.19", false, false},
		{"opencode", "2.0.20", false, true},
		{"codex", "codex-cli 0.156.9", true, false},
		{"codex", "codex-cli 0.157.1", false, false},
		{"claude", "2.1.284 (Claude Code)", true, false},
		{"claude", "3.0.0 (Claude Code)", false, true},
	}
	for _, c := range cases {
		got, err := CheckVersion(c.harness, c.out)
		var old *TooOldError
		if errors.As(err, &old) != c.tooOld {
			t.Errorf("%s %s: err = %v, want tooOld=%v", c.harness, c.out, err, c.tooOld)
			continue
		}
		if c.tooOld && !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s %s: too-old error must match ErrUnavailable", c.harness, c.out)
		}
		if got.Newer != c.newer || (got.Warning() != "") != c.newer {
			t.Errorf("%s %s: Newer = %v, warning %q; want %v", c.harness, c.out, got.Newer, got.Warning(), c.newer)
		}
	}
	if _, err := CheckVersion("nope", "1.0.0"); err == nil {
		t.Error("unknown harness: want error")
	}
}
