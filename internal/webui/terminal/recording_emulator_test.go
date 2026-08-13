package terminal

import (
	"reflect"
	"testing"
)

func TestRecordingEmulatorCommitsStyledRowsAndTracksColumnsOnResize(t *testing.T) {
	var committed []RecordingLine
	emulator := newRecordingEmulator(8, 2, func(runs []RecordingRun, timestamp int64, offset uint64) error {
		committed = append(committed, RecordingLine{
			Index: uint64(len(committed)), Timestamp: timestamp,
			Offset: OpaqueRecordingOffset(offset), Runs: runs,
		})
		return nil
	})

	fixture := []byte("\x1b[31mred\x1b[0m\r\nplain\r\nnext")
	if err := emulator.feed(fixture, 1000, 12); err != nil {
		t.Fatalf("feed fixture: %v", err)
	}
	if len(committed) != 1 {
		t.Fatalf("committed rows = %d, want 1", len(committed))
	}
	if got := committed[0].Runs; len(got) != 1 || got[0].Text != "red" || got[0].FG == "" {
		t.Fatalf("styled committed row = %#v, want red foreground run", got)
	}

	// A live geometry change applies to future output; the height shrink commits
	// the row that becomes permanently off-screen at its original width.
	emulator.resize(12, 1)
	if emulator.Cols != 12 {
		t.Fatalf("recorded cols = %d, want live width 12", emulator.Cols)
	}
	if len(committed) != 2 || lineText(committed[1]) != "plain" {
		t.Fatalf("committed after resize = %#v, want plain as second row", committed)
	}
	rows := emulator.screenRows()
	if len(rows) != 1 || lineText(rows[0]) != "next" {
		t.Fatalf("screen rows after resize = %#v, want next", rows)
	}
}

func TestRecordingEmulatorDECSTBMMatchesXtermParameterRules(t *testing.T) {
	emulator := newRecordingEmulator(8, 4, nil)
	if err := emulator.feed([]byte("\x1b[2;3r\x1b[3;2H"), 1, 0); err != nil {
		t.Fatalf("establish margins and cursor: %v", err)
	}
	wantTop, wantBottom := emulator.ScrollTop, emulator.ScrollBottom
	wantX, wantY := emulator.CursorX, emulator.CursorY
	if err := emulator.feed([]byte("\x1b[2;2r"), 2, 1); err != nil {
		t.Fatalf("feed invalid equal margins: %v", err)
	}
	if emulator.ScrollTop != wantTop || emulator.ScrollBottom != wantBottom || emulator.CursorX != wantX || emulator.CursorY != wantY {
		t.Fatalf("invalid DECSTBM changed state: margins=(%d,%d) cursor=(%d,%d), want margins=(%d,%d) cursor=(%d,%d)", emulator.ScrollTop, emulator.ScrollBottom, emulator.CursorX, emulator.CursorY, wantTop, wantBottom, wantX, wantY)
	}

	if err := emulator.feed([]byte("\x1b[2;999r"), 3, 2); err != nil {
		t.Fatalf("feed oversized bottom margin: %v", err)
	}
	if emulator.ScrollTop != 1 || emulator.ScrollBottom != 3 || emulator.CursorX != 0 || emulator.CursorY != 0 {
		t.Fatalf("clamped DECSTBM state = margins=(%d,%d) cursor=(%d,%d), want (1,3) and home", emulator.ScrollTop, emulator.ScrollBottom, emulator.CursorX, emulator.CursorY)
	}
}

func TestRecordingEmulatorDECOMHomesAndClampsAbsoluteRowsToMargins(t *testing.T) {
	emulator := newRecordingEmulator(8, 5, nil)
	if err := emulator.feed([]byte("\x1b[2;4r\x1b[?6h"), 1, 0); err != nil {
		t.Fatalf("enable DECOM: %v", err)
	}
	if emulator.CursorY != 1 {
		t.Fatalf("DECOM home row = %d, want scroll top 1", emulator.CursorY)
	}
	if err := emulator.feed([]byte("\x1b[99;1H"), 2, 1); err != nil {
		t.Fatalf("move in DECOM: %v", err)
	}
	if emulator.CursorY != 3 {
		t.Fatalf("DECOM absolute row = %d, want scroll bottom 3", emulator.CursorY)
	}
	if err := emulator.feed([]byte("\x1b[?6l"), 3, 2); err != nil {
		t.Fatalf("disable DECOM: %v", err)
	}
	if emulator.CursorY != 0 {
		t.Fatalf("normal-origin home row = %d, want 0", emulator.CursorY)
	}
}

func TestRecordingEmulatorUnsafePrivateLayoutModeStopsCommits(t *testing.T) {
	var committed []string
	emulator := newRecordingEmulator(4, 2, func(runs []RecordingRun, _ int64, _ uint64) error {
		committed = append(committed, runsText(runs))
		return nil
	})
	if err := emulator.feed([]byte("top\r\nbot\x1b[?69h\r\nnext"), 1, 0); err != nil {
		t.Fatalf("feed unimplemented layout mode: %v", err)
	}
	if !emulator.CommitBlocked {
		t.Fatal("CommitBlocked = false after unimplemented reachability mode")
	}
	if len(committed) != 0 {
		t.Fatalf("committed rows after unsafe mode = %#v, want none", committed)
	}
}

func TestRecordingEmulatorAlternateScreenDoesNotCommit(t *testing.T) {
	var committed []string
	emulator := newRecordingEmulator(10, 2, func(runs []RecordingRun, _ int64, _ uint64) error {
		committed = append(committed, runsText(runs))
		return nil
	})
	if err := emulator.feed([]byte("before\r\n\x1b[?1049hfull\r\nscreen\r\nnoise\x1b[?1049l\r\nafter\r\nend"), 1, 0); err != nil {
		t.Fatalf("feed fixture: %v", err)
	}
	if !emulator.EverAlt {
		t.Fatal("EverAlt = false, want true")
	}
	for _, line := range committed {
		if line == "full" || line == "screen" || line == "noise" {
			t.Fatalf("alternate-screen row was committed: %q", line)
		}
	}
	if !reflect.DeepEqual(committed, []string{"before", ""}) {
		t.Fatalf("normal-buffer commits = %#v, want [before <blank>]", committed)
	}
	rows := emulator.screenRows()
	if got := []string{lineText(rows[0]), lineText(rows[1])}; !reflect.DeepEqual(got, []string{"after", "end"}) {
		t.Fatalf("normal screen after alt exit = %#v", got)
	}
}

func TestRecordingEmulatorAltScreenShrinkCommitsDisplacedPrimaryRows(t *testing.T) {
	var committed []string
	emulator := newRecordingEmulator(8, 3, func(runs []RecordingRun, _ int64, _ uint64) error {
		committed = append(committed, runsText(runs))
		return nil
	})
	if err := emulator.feed([]byte("one\r\ntwo\r\nthree\x1b[?1049h"), 1, 0); err != nil {
		t.Fatalf("enter alternate screen: %v", err)
	}

	emulator.resize(8, 2)
	if err := emulator.feed([]byte("\x1b[?1049l"), 2, 1); err != nil {
		t.Fatalf("exit alternate screen: %v", err)
	}

	if !reflect.DeepEqual(committed, []string{"one"}) {
		t.Fatalf("committed primary rows = %#v, want [one]", committed)
	}
	rows := emulator.screenRows()
	got := []string{lineText(rows[0]), lineText(rows[1])}
	if !reflect.DeepEqual(got, []string{"two", "three"}) {
		t.Fatalf("primary rows after alt-screen shrink = %#v, want [two three]", got)
	}
	if emulator.CursorY != 1 {
		t.Fatalf("restored primary cursor row = %d, want 1", emulator.CursorY)
	}
}

func TestRecordingEmulatorOverwriteWideContinuationClearsWholeGlyph(t *testing.T) {
	emulator := newRecordingEmulator(6, 2, nil)
	if err := emulator.feed([]byte("界\r\x1b[2GX"), 1, 0); err != nil {
		t.Fatalf("feed fixture: %v", err)
	}
	row := emulator.Screen[0]
	if row[0].Text != "" || row[0].Wide || row[1].Text != "X" || row[1].Wide {
		t.Fatalf("wide overwrite cells = %#v, want blank then X", row[:2])
	}
}

func lineText(line RecordingLine) string { return runsText(line.Runs) }

func runsText(runs []RecordingRun) string {
	var text string
	for _, run := range runs {
		text += run.Text
	}
	return text
}

func TestRecordingEmulatorIgnoresBenignRenderHintSequences(t *testing.T) {
	var committed []string
	emulator := newRecordingEmulator(20, 4, func(runs []RecordingRun, _ int64, _ uint64) error {
		committed = append(committed, runsText(runs))
		return nil
	})
	benign := "\x1b[?2026h\x1b[?2026l\x1b[?25l\x1b[?25h\x1b[?1004h\x1b[?1004l" +
		"\x1b[2 q\x1b[0 q\x1b[?2004$p\x1b[>1u\x1b[=5;1u"
	if err := emulator.feed([]byte("one\r\n"+benign+"two"), 1, 0); err != nil {
		t.Fatalf("feed benign sequences: %v", err)
	}
	if emulator.CommitBlocked {
		t.Fatal("CommitBlocked = true after benign render-hint sequences")
	}
	if got := emulator.UnhandledSequences.Count; got != 0 {
		t.Fatalf("UnhandledSequences.Count = %d after benign sequences, want 0 (prefixes: %#v)",
			got, emulator.UnhandledSequences.Prefixes)
	}
	if len(committed) != 0 {
		t.Fatalf("committed rows = %#v, want none (nothing scrolled off)", committed)
	}
}

func TestRecordingEmulatorStillCountsCommitAffectingAndUnknownSequences(t *testing.T) {
	emulator := newRecordingEmulator(20, 4, func([]RecordingRun, int64, uint64) error { return nil })
	if err := emulator.feed([]byte("\x1b[?3h"), 1, 0); err != nil {
		t.Fatalf("feed DECCOLM: %v", err)
	}
	if !emulator.CommitBlocked {
		t.Fatal("CommitBlocked = false after DECCOLM")
	}
	if emulator.UnhandledSequences.Count == 0 {
		t.Fatal("UnhandledSequences.Count = 0 after DECCOLM, want counted")
	}
	before := emulator.UnhandledSequences.Count

	grapheme := newRecordingEmulator(20, 4, func([]RecordingRun, int64, uint64) error { return nil })
	if err := grapheme.feed([]byte("\x1b[?2027h"), 1, 0); err != nil {
		t.Fatalf("feed grapheme clustering mode: %v", err)
	}
	if !grapheme.CommitBlocked || grapheme.UnhandledSequences.Count == 0 {
		t.Fatalf("mode 2027: CommitBlocked=%v count=%d, want blocked and counted",
			grapheme.CommitBlocked, grapheme.UnhandledSequences.Count)
	}

	if err := emulator.feed([]byte("\x1b[5z"), 2, 0); err != nil {
		t.Fatalf("feed unknown CSI final: %v", err)
	}
	if emulator.UnhandledSequences.Count <= before {
		t.Fatal("unknown CSI final not counted")
	}
}
