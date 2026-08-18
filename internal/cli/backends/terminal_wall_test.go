package backends

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

func TestDetectTerminalWall(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want wallKind
	}{
		{"credit balance", "⏺ Your credit balance is too low to run this request.", wallBilling},
		{"out of credits", "The organization is out of credits.", wallBilling},
		{"402", "API error (402): payment declined", wallBilling},
		{"upgrade plan", "Upgrade your plan to continue using Claude Code", wallBilling},
		{"billing issue", "There is a billing problem with your account.", wallBilling},
		{"login", "Invalid API key · Please run /login", wallAuth},
		{"not logged in", "You are not logged in.", wallAuth},
		{"session expired", "Your session expired — sign in to continue", wallAuth},
		{"usage limit curly", "You’ve hit your session limit · resets 10:20pm (Europe/Warsaw)", wallUsageLimit},
		{"usage limit plain", "You have hit your usage limit", wallUsageLimit},
		{"prose", "I finished the refactor and all tests pass.", wallNone},
		{"empty", "", wallNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, line := detectTerminalWall(tc.in)
			if got != tc.want {
				t.Fatalf("detectTerminalWall(%q) = %v, want %v", tc.in, got, tc.want)
			}
			if tc.want != wallNone && strings.TrimSpace(line) == "" {
				t.Fatalf("detectTerminalWall(%q) returned no banner line", tc.in)
			}
		})
	}
}

// The regex matches on TEXT ALONE, quoted or not — this is deliberate, and the
// division of responsibility is the point: the regex is permissive and the
// settle window is what rejects a working agent that merely mentions the
// phrase (an agent implementing this very feature does).
func TestDetectTerminalWallMatchesQuotedText(t *testing.T) {
	quoted := "Here is the banner I am matching:\n\n```\ncredit balance is too low\n```\n\nNow writing the test."
	if got, _ := detectTerminalWall(quoted); got != wallBilling {
		t.Fatalf("detectTerminalWall(quoted) = %v, want wallBilling (the settle window, not the regex, rejects this)", got)
	}
}

func TestDetectTerminalWallPrecedence(t *testing.T) {
	both := "You’ve hit your session limit · resets 10:20pm\nYour credit balance is too low."
	got, _ := detectTerminalWall(both)
	if got != wallBilling {
		t.Fatalf("detectTerminalWall(billing+usage) = %v, want wallBilling", got)
	}
}

func TestDetectTerminalWallCarriesResetTail(t *testing.T) {
	_, line := detectTerminalWall("You’ve hit your session limit · resets 10:20pm (Europe/Warsaw)")
	if !strings.Contains(line, "resets 10:20pm") {
		t.Fatalf("banner line %q dropped the reset tail", line)
	}
}

func TestStripANSI(t *testing.T) {
	raw := []byte("\x1b[2J\x1b[H\x1b]0;claude\x07\x1b[38;5;203m⏺\x1b[0m Your credit balance is too low.\r\n\x1b[1A\x1b[2K")
	got := stripANSI(raw)
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\r') {
		t.Fatalf("stripANSI left control bytes: %q", got)
	}
	if !strings.Contains(got, "Your credit balance is too low.") {
		t.Fatalf("stripANSI ate the banner: %q", got)
	}
	if kind, _ := detectTerminalWall(got); kind != wallBilling {
		t.Fatalf("stripped redraw classified %v, want wallBilling", kind)
	}
}

// fakeClock is a manually advanced clock for the watcher's settle logic.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestWatcher(settle time.Duration, cancel context.CancelFunc) (*wallWatcher, *fakeClock) {
	clk := newFakeClock()
	w := newWallWatcher(settle, cancel)
	w.now = clk.now
	w.lastChange = clk.now()
	return w, clk
}

func TestWallWatcherFiresAfterSettle(t *testing.T) {
	cancels := 0
	w, clk := newTestWatcher(45*time.Second, func() { cancels++ })

	w.observe([]byte("Your credit balance is too low.\n"))
	if w.check() {
		t.Fatal("watcher fired before the settle window elapsed")
	}
	clk.advance(46 * time.Second)
	if !w.check() {
		t.Fatal("watcher did not fire after the settle window")
	}
	kind, line, ok := w.result()
	if !ok || kind != wallBilling || !strings.Contains(line, "credit balance") {
		t.Fatalf("result = (%v, %q, %v), want a billing wall", kind, line, ok)
	}
	clk.advance(time.Hour)
	if w.check() {
		t.Fatal("watcher fired twice")
	}
	if cancels != 1 {
		t.Fatalf("cancel called %d times, want exactly 1", cancels)
	}
}

// The false-positive guard: an agent that keeps writing is working, whatever
// its output quotes.
func TestWallWatcherDoesNotFireWhileOutputContinues(t *testing.T) {
	w, clk := newTestWatcher(45*time.Second, func() { t.Error("cancel called for a working agent") })

	w.observe([]byte("The banner reads: your credit balance is too low.\n"))
	for i := 0; i < 10; i++ {
		clk.advance(30 * time.Second)
		if w.check() {
			t.Fatalf("watcher fired on iteration %d while output continued", i)
		}
		w.observe([]byte("... still editing files ...\n"))
	}
	if _, _, ok := w.result(); ok {
		t.Fatal("watcher recorded a verdict for a working agent")
	}
}

func TestWallWatcherResetsOnNewOutput(t *testing.T) {
	w, clk := newTestWatcher(45*time.Second, func() { t.Error("cancel called after recovery") })

	w.observe([]byte("Your credit balance is too low.\n"))
	clk.advance(20 * time.Second)
	// The tail window is what is judged; a recovered agent overwrites it.
	w.observe([]byte(strings.Repeat("recovered: running tests\n", 2000)))
	clk.advance(10 * time.Minute)
	if w.check() {
		t.Fatal("watcher fired on a clean tail")
	}
}

func TestWallWatcherScreenGeneration(t *testing.T) {
	w, clk := newTestWatcher(45*time.Second, func() {})

	w.observeScreen("Your credit balance is too low.", 7)
	clk.advance(20 * time.Second)
	// Same generation: a repaint, not work. The clock must not be stamped.
	w.observeScreen("Your credit balance is too low.", 7)
	clk.advance(30 * time.Second)
	if !w.check() {
		t.Fatal("an unchanged generation reset the settle timer")
	}

	w2, clk2 := newTestWatcher(45*time.Second, func() {})
	w2.observeScreen("Your credit balance is too low.", 7)
	clk2.advance(40 * time.Second)
	w2.observeScreen("Your credit balance is too low.", 8)
	clk2.advance(20 * time.Second)
	if w2.check() {
		t.Fatal("a changed generation failed to reset the settle timer")
	}
}

func TestWallWatcherRunExitsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w := newWallWatcher(time.Second, cancel)
	done := make(chan struct{})
	go func() {
		w.run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("wallWatcher.run leaked past its context")
	}
}

func TestWallInvocationErrorCarriesMarker(t *testing.T) {
	cases := []struct {
		kind   wallKind
		marker string
	}{
		{wallBilling, agenterr.BillingWallMarker},
		{wallAuth, agenterr.AuthRequiredMarker},
		{wallUsageLimit, agenterr.UsageLimitedMarker},
	}
	for _, tc := range cases {
		ie := wallInvocationError(tc.kind, "the banner line", "trailing output")
		if ie == nil {
			t.Fatalf("wallInvocationError(%v) = nil", tc.kind)
		}
		if !strings.Contains(ie.Error(), tc.marker) {
			t.Errorf("Err %q missing marker %q", ie.Error(), tc.marker)
		}
		if !strings.Contains(ie.Error(), "the banner line") {
			t.Errorf("Err %q dropped the reason", ie.Error())
		}
		if !strings.HasPrefix(ie.OutputTail, tc.marker) {
			t.Errorf("evidence %q does not lead with the marker", ie.OutputTail)
		}
		if !strings.Contains(ie.OutputTail, "trailing output") {
			t.Errorf("evidence %q dropped the output tail", ie.OutputTail)
		}
		if ie.ExitCode != 1 {
			t.Errorf("ExitCode = %d, want 1", ie.ExitCode)
		}
	}
	if ie := wallInvocationError(wallNone, "x", "y"); ie != nil {
		t.Fatalf("wallInvocationError(wallNone) = %v, want nil", ie)
	}
}

func TestWallInvocationErrorDoesNotDoubleMarker(t *testing.T) {
	combined := agenterr.BillingWallMarker + ": banner"
	ie := wallInvocationError(wallBilling, "banner", combined+"\nmore")
	if strings.Count(ie.OutputTail, agenterr.BillingWallMarker) != 1 {
		t.Fatalf("marker duplicated in evidence: %q", ie.OutputTail)
	}
}

func TestWallSettleWindowEnv(t *testing.T) {
	cases := []struct {
		name string
		set  bool
		val  string
		want time.Duration
	}{
		{"default", false, "", defaultWallSettleSeconds * time.Second},
		{"explicit", true, "10", 10 * time.Second},
		{"disabled", true, "0", 0},
		{"negative", true, "-5", defaultWallSettleSeconds * time.Second},
		{"garbage", true, "soon", defaultWallSettleSeconds * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(envWallSettleSeconds, tc.val)
			} else {
				t.Setenv(envWallSettleSeconds, "")
			}
			if got := wallSettleWindow(); got != tc.want {
				t.Fatalf("wallSettleWindow() = %v, want %v", got, tc.want)
			}
		})
	}
}
