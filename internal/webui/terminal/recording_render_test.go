package terminal

import (
	"bytes"
	"strings"
	"testing"
)

func feedRenderEmulator(t *testing.T, payload string) *recordingEmulator {
	t.Helper()
	e := newRecordingEmulator(80, 24, nil)
	if err := e.feed([]byte(payload), 1, 0); err != nil {
		t.Fatalf("feed: %v", err)
	}
	return e
}

func TestRenderRecorderScreenPristineIsNil(t *testing.T) {
	e := newRecordingEmulator(80, 24, nil)
	if got := renderRecorderScreen(e.activeScreenRows(), e.CursorX, e.CursorY); got != nil {
		t.Fatalf("pristine screen rendered %q, want nil", got)
	}
}

func TestRenderRecorderScreenRowsAndCursor(t *testing.T) {
	e := feedRenderEmulator(t, "first\r\n\r\nthird")
	out := renderRecorderScreen(e.activeScreenRows(), e.CursorX, e.CursorY)
	if !bytes.Contains(out, []byte("\x1b[1;1H")) || !bytes.Contains(out, []byte("first")) {
		t.Fatalf("row 1 not rendered with absolute addressing: %q", out)
	}
	if !bytes.Contains(out, []byte("\x1b[3;1H")) || !bytes.Contains(out, []byte("third")) {
		t.Fatalf("row 3 not rendered with absolute addressing: %q", out)
	}
	// The blank row 2 is skipped rather than painted.
	if bytes.Contains(out, []byte("\x1b[2;1H")) {
		t.Fatalf("blank row was rendered: %q", out)
	}
	// Cursor rests after "third": row 3, column 6.
	if !bytes.HasSuffix(out, []byte("\x1b[3;6H")) {
		t.Fatalf("cursor not restored, replay ends %q", out[maxInt(0, len(out)-16):])
	}
}

func TestRenderRecorderScreenCarriesStyles(t *testing.T) {
	e := feedRenderEmulator(t, "\x1b[1;4;31mstyled\x1b[0m plain")
	out := string(renderRecorderScreen(e.activeScreenRows(), e.CursorX, e.CursorY))
	styledAt := strings.Index(out, "styled")
	plainAt := strings.Index(out, "plain")
	if styledAt < 0 || plainAt < 0 {
		t.Fatalf("render lost text: %q", out)
	}
	prefix := out[:styledAt]
	sgr := prefix[strings.LastIndex(prefix, "\x1b["):]
	if !strings.Contains(sgr, ";1") || !strings.Contains(sgr, ";4") || !strings.Contains(sgr, ";38;2;") {
		t.Fatalf("styled run missing bold/underline/truecolor FG: %q", sgr)
	}
	between := out[styledAt:plainAt]
	if !strings.Contains(between, "\x1b[0") {
		t.Fatalf("style not reset before plain run: %q", between)
	}
}

func TestRenderRecorderScreenUsesActiveAltScreen(t *testing.T) {
	e := feedRenderEmulator(t, "primary-line\r\n\x1b[?1049h\x1b[2J\x1b[HALT-CONTENT")
	active := renderRecorderScreen(e.activeScreenRows(), e.CursorX, e.CursorY)
	if !bytes.Contains(active, []byte("ALT-CONTENT")) {
		t.Fatalf("active render missing alt-screen content: %q", active)
	}
	if bytes.Contains(active, []byte("primary-line")) {
		t.Fatalf("active render leaked primary screen while alt is selected: %q", active)
	}
	// History rows stay on the primary screen — committed history never
	// includes alt-screen content.
	history := renderRecorderScreen(e.screenRows(), 0, 0)
	if !bytes.Contains(history, []byte("primary-line")) || bytes.Contains(history, []byte("ALT-CONTENT")) {
		t.Fatalf("history render should show only the primary screen: %q", history)
	}
}

func TestParseHexColor(t *testing.T) {
	if r, g, b, ok := parseHexColor("#0a80Ff"); !ok || r != 0x0a || g != 0x80 || b != 0xff {
		t.Fatalf("parseHexColor(#0a80Ff) = %d,%d,%d,%t", r, g, b, ok)
	}
	for _, bad := range []string{"", "#fff", "0a80ff", "#0a80fg"} {
		if _, _, _, ok := parseHexColor(bad); ok {
			t.Fatalf("parseHexColor(%q) unexpectedly ok", bad)
		}
	}
}

func TestIncompleteTrailingSequence(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "hello world", ""},
		{"bare ESC", "text\x1b", "\x1b"},
		{"CSI intro only", "text\x1b[", "\x1b["},
		{"CSI mid-params", "text\x1b[38;2;10", "\x1b[38;2;10"},
		{"complete CSI", "text\x1b[0m", ""},
		{"complete CSI then text", "\x1b[31mred", ""},
		{"OSC unterminated", "\x1b]0;title", "\x1b]0;title"},
		{"OSC BEL-terminated", "\x1b]0;title\x07", ""},
		{"OSC ST-terminated", "\x1b]0;title\x1b\\", ""},
		{"DCS unterminated", "text\x1bP1$r", "\x1bP1$r"},
		{"charset complete", "text\x1b(B", ""},
		{"charset intermediate only", "text\x1b(", "\x1b("},
		{"two-byte ESC complete", "text\x1b7", ""},
		{"torn 2-byte rune", "caf\xc3", "\xc3"},
		{"torn 3-byte rune", "x\xe2\x82", "\xe2\x82"},
		{"torn 4-byte rune", "ok\x1b[0m\xf0\x9f", "\xf0\x9f"},
		{"complete 3-byte rune", "x\xe2\x82\xac", ""},
		{"complete 4-byte rune", "\xf0\x9f\x98\x80", ""},
	}
	for _, tc := range cases {
		got := incompleteTrailingSequence([]byte(tc.in))
		if string(got) != tc.want {
			t.Errorf("%s: incompleteTrailingSequence(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestCurrentModeCheckpointFoldsRetainedEvents(t *testing.T) {
	ring := newRingBuffer(1024)
	ring.Append([]byte("\x1b[?1049h\x1b[?25l\x1b[?1000hdata"))

	// The head checkpoint has folded nothing (no eviction yet), so the
	// mode switches live only in the retained body.
	headCheckpoint, _ := ring.ReplaySnapshot()
	if bytes.Contains(headCheckpoint, []byte("1049")) {
		t.Fatalf("head checkpoint unexpectedly folded retained events: %q", headCheckpoint)
	}
	current := ring.CurrentModeCheckpoint()
	for _, want := range []string{"\x1b[?1049h", "\x1b[?25l", "\x1b[?1000h"} {
		if !bytes.Contains(current, []byte(want)) {
			t.Fatalf("CurrentModeCheckpoint missing %q: %q", want, current)
		}
	}

	// Later switches override earlier ones.
	ring.Append([]byte("\x1b[?1049l\x1b[?25h"))
	current = ring.CurrentModeCheckpoint()
	if !bytes.Contains(current, []byte("\x1b[?1049l")) || bytes.Contains(current, []byte("\x1b[?1049h")) {
		t.Fatalf("alt-screen exit not folded: %q", current)
	}
	if !bytes.Contains(current, []byte("\x1b[?25h")) {
		t.Fatalf("cursor-show not folded: %q", current)
	}
}

func TestRingTailBytes(t *testing.T) {
	ring := newRingBuffer(1024)
	if got := ring.TailBytes(16); got != nil {
		t.Fatalf("empty ring TailBytes = %q", got)
	}
	ring.Append([]byte("abcdefgh"))
	if got := ring.TailBytes(4); string(got) != "efgh" {
		t.Fatalf("TailBytes(4) = %q, want efgh", got)
	}
	if got := ring.TailBytes(100); string(got) != "abcdefgh" {
		t.Fatalf("TailBytes(100) = %q, want full buffer", got)
	}
}
