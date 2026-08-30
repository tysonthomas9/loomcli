package exe

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/placement"
)

// TestAttachCommandNeverCreatesASession is the one that matters most here.
// "new-session -A" is the natural thing to reach for and it is wrong: when the
// lead failed to boot it silently opens an empty shell, which looks exactly
// like a healthy terminal while no agent is running behind it.
func TestAttachCommandNeverCreatesASession(t *testing.T) {
	cmd := tmuxAttachCommand("lead")
	if strings.Contains(cmd, "new-session") {
		t.Fatalf("attach command can create a session: %q", cmd)
	}
	if !strings.Contains(cmd, "attach-session") {
		t.Fatalf("attach command does not attach: %q", cmd)
	}
	if !strings.Contains(cmd, " -d ") {
		t.Errorf("attach does not detach other clients, so a stale client pins the size: %q", cmd)
	}
}

// TestTmuxTargetsAreExact pins the "=" prefix. tmux target resolution falls
// back to prefix and then fnmatch matching, so a bare -t lead attaches to
// "lead-old" once "lead" is gone -- showing one lead's terminal under another
// lead's identity.
func TestTmuxTargetsAreExact(t *testing.T) {
	for _, cmd := range []string{tmuxAttachCommand("lead"), tmuxHasSessionCommand("lead")} {
		if !strings.Contains(cmd, "'=lead'") {
			t.Errorf("target is not exact-matched: %q", cmd)
		}
	}
}

func TestHasSessionCommandHasNoSideEffect(t *testing.T) {
	cmd := tmuxHasSessionCommand("lead")
	for _, mutating := range []string{"new-session", "kill", "attach", "send-keys"} {
		if strings.Contains(cmd, mutating) {
			t.Errorf("existence check contains %q: %q", mutating, cmd)
		}
	}
}

// TestAttachPTYRejectsBadIdentifiersBeforeDialing pins that validation happens
// before any network I/O. The provider has no dialer, so anything reaching the
// dial would panic rather than quietly pass.
func TestAttachPTYRejectsBadIdentifiersBeforeDialing(t *testing.T) {
	p := &Provider{}
	cases := []struct {
		name      string
		sandboxID string
		session   string
	}{
		{"empty sandbox", "", "lead"},
		{"empty session", "lead-abc", ""},
		{"session addresses a pane", "lead-abc", "lead:0.1"},
		{"session addresses a window", "lead-abc", "other:1"},
		{"session with command substitution", "lead-abc", "$(id)"},
		{"sandbox with a shell metacharacter", "lead;rm -rf /", "lead"},
		{"sandbox uppercase", "LEAD-ABC", "lead"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := p.AttachPTY(context.Background(), tc.sandboxID, tc.session); err == nil {
				t.Fatal("accepted an identifier that must be refused")
			}
		})
	}
}

func TestAttachPTYAcceptsTheLeadSessionID(t *testing.T) {
	// The broker names every lead PTY placement.LeadPTYSessionID. If the
	// allowlist rejected it, every attach would fail with a validation error
	// that reads like a bad request rather than a wiring bug.
	if err := checkArg("pty session", placement.LeadPTYSessionID, rePTYSession); err != nil {
		t.Fatalf("the broker's own lead session id is refused: %v", err)
	}
}

func TestPTYSessionNotFoundIsDistinguishable(t *testing.T) {
	// Callers decide whether to reprovision based on this. If absence were
	// reported as a generic error, a transport failure and a missing lead
	// would be indistinguishable and one of them would be handled wrong.
	err := errors.Join(ErrPTYSessionNotFound, errors.New("vm lead-abc"))
	if !errors.Is(err, ErrPTYSessionNotFound) {
		t.Fatal("ErrPTYSessionNotFound does not survive wrapping")
	}
}

// TestEveryTmuxTargetIsExact sweeps the package for tmux commands built with
// "-t", since each one is a place where prefix matching can address a
// DIFFERENT lead's session. Attaching to the wrong one shows the wrong
// terminal; killing the wrong one destroys a running lead.
func TestEveryTmuxTargetIsExact(t *testing.T) {
	src, err := os.ReadFile("provider.go")
	if err != nil {
		t.Fatalf("read provider.go: %v", err)
	}
	for _, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, "-t %s") {
			continue
		}
		if !strings.Contains(line, "exactTarget(") {
			t.Errorf("tmux target is not exact-matched, so it can address another session:\n\t%s", strings.TrimSpace(line))
		}
	}
}
