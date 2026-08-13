package terminal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hinshun/vt10x"
)

const recordingChunkSeed int64 = 0x5eedc0de

type recordingCorpusFixture struct {
	Name       string                 `json:"name"`
	Cols       uint16                 `json:"cols"`
	Rows       uint16                 `json:"rows"`
	OracleSkip string                 `json:"oracleSkip,omitempty"`
	Events     []recordingCorpusEvent `json:"events"`
}

type recordingCorpusEvent struct {
	Data   string                 `json:"data,omitempty"`
	Resize *recordingCorpusResize `json:"resize,omitempty"`
}

type recordingCorpusResize struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type recordingEmulatorResult struct {
	Committed []RecordingLine
	State     recordingEmulatorState
}

func TestRecordingEmulatorChunkBoundaryInvariant(t *testing.T) {
	fixtures := loadRecordingCorpus(t)
	for fixtureIndex, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			baseline := runRecordingFixture(t, fixture, nil)
			baselineCommitted := mustJSON(t, baseline.Committed)
			baselineState := mustJSON(t, baseline.State)
			offsets := recordingFixtureByteOffsets(fixture)

			// The one-byte plan explicitly splits inside every CSI/OSC, UTF-8
			// rune, and CRLF present in the corpus.
			assertRecordingChunkPlan(t, fixture, recordingChunkSeed, offsets, baselineCommitted, baselineState)
			for _, offset := range offsets {
				assertRecordingChunkPlan(t, fixture, recordingChunkSeed, []int{offset}, baselineCommitted, baselineState)
			}

			seed := recordingChunkSeed + int64(fixtureIndex)
			rng := rand.New(rand.NewSource(seed)) //nolint:gosec // deterministic property-test split plans
			for range 256 {
				var splits []int
				for _, offset := range offsets {
					if rng.Intn(4) == 0 {
						splits = append(splits, offset)
					}
				}
				assertRecordingChunkPlan(t, fixture, seed, splits, baselineCommitted, baselineState)
			}
		})
	}
}

func TestRecordingEmulatorDoesNotCommitVT10xReachableRows(t *testing.T) {
	tests := []struct {
		name   string
		cols   uint16
		rows   uint16
		events []recordingCorpusEvent
	}{
		{
			name: "width growth uses the live width",
			cols: 4, rows: 2,
			events: []recordingCorpusEvent{
				{Resize: &recordingCorpusResize{Cols: 8, Rows: 2}},
				{Data: "12345678X"},
			},
		},
		{
			name: "disabled autowrap does not scroll",
			cols: 4, rows: 2,
			events: []recordingCorpusEvent{
				{Data: "top\r\nbot"},
				{Data: "\x1b[?7lXY"},
			},
		},
		{
			name: "origin mode constrains saved cursor reachability",
			cols: 4, rows: 3,
			events: []recordingCorpusEvent{
				{Data: "r0\r\nr1\r\nr2"},
				{Data: "\x1b[1;2r\x1b[?6h\x1b[99;1H\x1b[s\x1b[r\x1b[u\n"},
			},
		},
		{
			name: "oversized bottom margin clamps to screen",
			cols: 4, rows: 4,
			events: []recordingCorpusEvent{
				{Data: "r0\r\nr1\r\nr2\r\nr3"},
				{Data: "\x1b[2;999r\x1b[4;1H\n"},
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			oracle := &vt10xOracle{terminal: vt10x.New(vt10x.WithSize(int(test.cols), int(test.rows)))}
			var committed []string
			emulator := newRecordingEmulator(test.cols, test.rows, func(runs []RecordingRun, _ int64, _ uint64) error {
				text := runsText(runs)
				committed = append(committed, text)
				for row := 0; row < int(test.rows); row++ {
					visible := strings.TrimRight(vt10xRowText(oracle.terminal, row), " ")
					if text != "" && strings.HasPrefix(visible, text) {
						t.Fatalf("committed row %q while vt10x row %d is still reachable as %q", text, row, visible)
					}
				}
				return nil
			})

			for eventIndex, event := range test.events {
				if event.Resize != nil {
					oracle.terminal.Resize(int(event.Resize.Cols), int(event.Resize.Rows))
					emulator.LastTimestamp = int64(eventIndex + 1)
					emulator.resize(event.Resize.Cols, event.Resize.Rows)
				}
				for _, b := range []byte(event.Data) {
					if err := oracle.Write([]byte{b}); err != nil {
						t.Fatalf("vt10x write event %d: %v", eventIndex, err)
					}
					if err := emulator.feed([]byte{b}, int64(eventIndex+1), uint64(eventIndex)); err != nil {
						t.Fatalf("recording feed event %d: %v", eventIndex, err)
					}
				}
			}
		})
	}
}

func vt10xRowText(terminal vt10x.Terminal, row int) string {
	cols, _ := terminal.Size()
	var text strings.Builder
	for col := 0; col < cols; col++ {
		cell := terminal.Cell(col, row).Char
		if cell == 0 {
			cell = ' '
		}
		text.WriteRune(cell)
	}
	return text.String()
}

func TestRecordingEmulatorMatchesVT10xAfterEveryChunk(t *testing.T) {
	for _, fixture := range loadRecordingCorpus(t) {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			if fixture.OracleSkip != "" {
				t.Skip(fixture.OracleSkip)
			}
			emulator := newRecordingEmulator(fixture.Cols, fixture.Rows, nil)
			oracle := &vt10xOracle{terminal: vt10x.New(vt10x.WithSize(int(fixture.Cols), int(fixture.Rows)))}
			byteOffset := 0
			for eventIndex, event := range fixture.Events {
				if event.Resize != nil {
					emulator.resize(event.Resize.Cols, event.Resize.Rows)
					oracle.terminal.Resize(int(event.Resize.Cols), int(event.Resize.Rows))
					assertVT10xScreen(t, fixture, eventIndex, byteOffset, emulator, oracle.terminal)
				}
				for _, b := range []byte(event.Data) {
					if err := emulator.feed([]byte{b}, int64(1000+eventIndex), uint64(eventIndex*100)); err != nil {
						t.Fatalf("feed byte %d: %v", byteOffset, err)
					}
					if err := oracle.Write([]byte{b}); err != nil {
						t.Fatalf("vt10x write byte %d: %v", byteOffset, err)
					}
					byteOffset++
					assertVT10xScreen(t, fixture, eventIndex, byteOffset, emulator, oracle.terminal)
				}
			}
		})
	}
}

func TestRecordingEmulatorWideAndCombiningCells(t *testing.T) {
	fixture := recordingFixtureNamed(t, "wide-cjk-and-combining-marks")
	result := runRecordingFixture(t, fixture, recordingFixtureByteOffsets(fixture))
	row := result.State.Screen[0]
	if row[0].Text != "A" || row[1].Text != "界" || !row[2].Wide || row[3].Text != "é" || row[4].Text != "Z" {
		t.Fatalf("first Unicode row cells = %#v", row[:5])
	}
	if result.State.CursorX != 3 || result.State.CursorY != 2 {
		t.Fatalf("final Unicode cursor = (%d,%d), want (3,2)", result.State.CursorX, result.State.CursorY)
	}
	second := result.State.Screen[1]
	if second[0].Text != "界́" || !second[1].Wide {
		t.Fatalf("combining mark did not attach to wide base: %#v", second[:2])
	}
}

func TestRecordingCorpusUnhandledSequenceMeasurement(t *testing.T) {
	var total uint64
	combined := make(map[string]uint64)
	for _, fixture := range loadRecordingCorpus(t) {
		result := runRecordingFixture(t, fixture, nil)
		total += result.State.UnhandledSequences.Count
		for prefix, count := range result.State.UnhandledSequences.Prefixes {
			combined[prefix] += count
		}
	}
	// Cursor-visibility (?25) is a render hint and is deliberately not
	// counted; only sequences with no safe interpretation remain.
	if total != 1 {
		t.Fatalf("corpus unhandled sequence count = %d, want 1; prefixes=%v", total, combined)
	}
	want := map[string]uint64{"OSC 0": 1}
	for prefix, count := range want {
		if combined[prefix] != count {
			t.Fatalf("unhandled prefix %q = %d, want %d; all=%v", prefix, combined[prefix], count, combined)
		}
	}
	t.Logf("representative recording corpus: unhandledSequences.count=%d prefixes=%v", total, combined)
}

func TestRecordingEmulatorUnhandledSequencePrefixesStayBounded(t *testing.T) {
	emulator := newRecordingEmulator(80, 24, nil)
	for index := 0; index < 40; index++ {
		sequence := fmt.Sprintf("\x1b[%dq", index)
		if err := emulator.feed([]byte(sequence), 1, 0); err != nil {
			t.Fatalf("feed unique sequence %d: %v", index, err)
		}
	}
	for range 50 {
		if err := emulator.feed([]byte("\x1b[999q"), 1, 0); err != nil {
			t.Fatalf("feed frequent sequence: %v", err)
		}
	}
	summary := emulator.unhandledSequenceSummary()
	if summary.Count != 90 {
		t.Fatalf("unhandled count = %d, want 90", summary.Count)
	}
	if len(summary.Prefixes) > maxUnhandledSequencePrefixes {
		t.Fatalf("prefix map size = %d, max %d", len(summary.Prefixes), maxUnhandledSequencePrefixes)
	}
	if summary.Prefixes["CSI 999q"] == 0 {
		t.Fatalf("frequent unhandled prefix missing from bounded map: %v", summary.Prefixes)
	}
}

func TestRecordingCorpusExercisesRequiredSplitBoundaries(t *testing.T) {
	var stream []byte
	for _, fixture := range loadRecordingCorpus(t) {
		for _, event := range fixture.Events {
			stream = append(stream, event.Data...)
		}
	}
	for name, needle := range map[string][]byte{
		"CSI":   []byte("\x1b["),
		"OSC":   []byte("\x1b]"),
		"CRLF":  []byte("\r\n"),
		"UTF-8": []byte("界"),
	} {
		if !bytes.Contains(stream, needle) {
			t.Fatalf("recording corpus does not contain required %s split target", name)
		}
	}
}

func assertRecordingChunkPlan(
	t *testing.T,
	fixture recordingCorpusFixture,
	seed int64,
	splits []int,
	wantCommitted, wantState []byte,
) {
	t.Helper()
	result := runRecordingFixture(t, fixture, splits)
	if got := mustJSON(t, result.Committed); !bytes.Equal(got, wantCommitted) {
		t.Fatalf("committed output changed across chunking: seed=%d splits=%v\nwant=%s\n got=%s", seed, splits, wantCommitted, got)
	}
	if got := mustJSON(t, result.State); !bytes.Equal(got, wantState) {
		t.Fatalf("final screen state changed across chunking: seed=%d splits=%v\nwant=%s\n got=%s", seed, splits, wantState, got)
	}
}

func runRecordingFixture(t *testing.T, fixture recordingCorpusFixture, splits []int) recordingEmulatorResult {
	t.Helper()
	var committed []RecordingLine
	emulator := newRecordingEmulator(fixture.Cols, fixture.Rows, func(runs []RecordingRun, timestamp int64, offset uint64) error {
		committed = append(committed, RecordingLine{
			Index: uint64(len(committed)), Timestamp: timestamp,
			Offset: OpaqueRecordingOffset(offset), Runs: runs,
		})
		return nil
	})

	splitSet := make(map[int]struct{}, len(splits))
	for _, split := range splits {
		splitSet[split] = struct{}{}
	}
	streamOffset := 0
	for eventIndex, event := range fixture.Events {
		if event.Resize != nil {
			emulator.LastTimestamp = int64(1000 + eventIndex)
			emulator.LastFrameOff = uint64(eventIndex * 100)
			emulator.resize(event.Resize.Cols, event.Resize.Rows)
		}
		data := []byte(event.Data)
		chunkStart := 0
		for index := 1; index <= len(data); index++ {
			_, split := splitSet[streamOffset+index]
			if !split && index != len(data) {
				continue
			}
			if err := emulator.feed(data[chunkStart:index], int64(1000+eventIndex), uint64(eventIndex*100)); err != nil {
				t.Fatalf("feed event %d bytes %d:%d: %v", eventIndex, chunkStart, index, err)
			}
			chunkStart = index
		}
		streamOffset += len(data)
	}
	return recordingEmulatorResult{Committed: committed, State: cloneRecordingState(t, emulator.recordingEmulatorState)}
}

func recordingFixtureByteOffsets(fixture recordingCorpusFixture) []int {
	var offsets []int
	streamOffset := 0
	for _, event := range fixture.Events {
		for index := 1; index < len([]byte(event.Data)); index++ {
			offsets = append(offsets, streamOffset+index)
		}
		streamOffset += len([]byte(event.Data))
	}
	return offsets
}

func loadRecordingCorpus(t *testing.T) []recordingCorpusFixture {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "recording", "*.json"))
	if err != nil {
		t.Fatalf("glob recording corpus: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("recording corpus is empty")
	}
	sort.Strings(paths)
	fixtures := make([]recordingCorpusFixture, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var fixture recordingCorpusFixture
		if err := json.Unmarshal(data, &fixture); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if fixture.Name == "" || fixture.Cols == 0 || fixture.Rows == 0 || len(fixture.Events) == 0 {
			t.Fatalf("incomplete fixture %s: %#v", path, fixture)
		}
		fixtures = append(fixtures, fixture)
	}
	return fixtures
}

func recordingFixtureNamed(t *testing.T, name string) recordingCorpusFixture {
	t.Helper()
	for _, fixture := range loadRecordingCorpus(t) {
		if fixture.Name == name {
			return fixture
		}
	}
	t.Fatalf("recording fixture %q not found", name)
	return recordingCorpusFixture{}
}

func cloneRecordingState(t *testing.T, state recordingEmulatorState) recordingEmulatorState {
	t.Helper()
	data := mustJSON(t, state)
	var clone recordingEmulatorState
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatalf("clone emulator state: %v", err)
	}
	return clone
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal property value: %v", err)
	}
	return data
}

// vt10xOracle compensates only for vt10x.Write's documented inability to
// retain an incomplete UTF-8 rune across Write calls. It does not alter VT
// semantics, so screen/cursor comparisons remain independent.
type vt10xOracle struct {
	terminal vt10x.Terminal
	pending  []byte
}

func (o *vt10xOracle) Write(chunk []byte) error {
	o.pending = append(o.pending, chunk...)
	complete := 0
	for complete < len(o.pending) {
		if !utf8.FullRune(o.pending[complete:]) {
			break
		}
		_, size := utf8.DecodeRune(o.pending[complete:])
		if size == 0 {
			break
		}
		complete += size
	}
	if complete == 0 {
		return nil
	}
	written, err := o.terminal.Write(o.pending[:complete])
	if err != nil {
		return err
	}
	if written != complete {
		return fmt.Errorf("vt10x wrote %d of %d complete bytes", written, complete)
	}
	o.pending = append(o.pending[:0], o.pending[complete:]...)
	return nil
}

func assertVT10xScreen(
	t *testing.T,
	fixture recordingCorpusFixture,
	eventIndex, byteOffset int,
	emulator *recordingEmulator,
	oracle vt10x.Terminal,
) {
	t.Helper()
	cols, rows := oracle.Size()
	if cols != emulator.Cols || rows != emulator.Rows {
		t.Fatalf("size divergence after event=%d byte=%d: ours=%dx%d vt10x=%dx%d", eventIndex, byteOffset, emulator.Cols, emulator.Rows, cols, rows)
	}
	// vt10x carries the normal-buffer cursor into its alternate buffer,
	// whereas this recorder deliberately homes the disposable alternate grid.
	// Alt rows are never committed. Assert mode agreement while active, then
	// resume full cell/cursor comparison immediately after normal-buffer restore.
	if emulator.Alt {
		if oracle.Mode()&vt10x.ModeAltScreen == 0 {
			t.Fatalf("alternate-screen mode divergence fixture=%q event=%d byte=%d", fixture.Name, eventIndex, byteOffset)
		}
		return
	}
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			ours := emulator.Screen[y][x].Text
			if ours == "" {
				ours = " "
			}
			theirs := string(oracle.Cell(x, y).Char)
			if theirs == "\x00" {
				theirs = " "
			}
			if ours != theirs {
				t.Fatalf("cell divergence fixture=%q event=%d byte=%d cell=(%d,%d): ours=%q vt10x=%q\nours rows:\n%s\nvt10x rows:\n%s", fixture.Name, eventIndex, byteOffset, x, y, ours, theirs, recordingScreenText(emulator.Screen), oracle.String())
			}
		}
	}
	cursor := oracle.Cursor()
	if emulator.CursorX != cursor.X || emulator.CursorY != cursor.Y {
		t.Fatalf("cursor divergence fixture=%q event=%d byte=%d: ours=(%d,%d) vt10x=(%d,%d)\nours rows:\n%s\nvt10x rows:\n%s", fixture.Name, eventIndex, byteOffset, emulator.CursorX, emulator.CursorY, cursor.X, cursor.Y, recordingScreenText(emulator.Screen), oracle.String())
	}
}

func recordingScreenText(screen [][]recordingCell) string {
	rows := make([]string, 0, len(screen))
	for _, row := range screen {
		var text strings.Builder
		for _, cell := range row {
			if cell.Wide {
				text.WriteRune('·')
			} else if cell.Text == "" {
				text.WriteByte(' ')
			} else {
				text.WriteString(cell.Text)
			}
		}
		rows = append(rows, text.String())
	}
	return strings.Join(rows, "\n")
}

func TestRecordingCorpusNamesAreUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, fixture := range loadRecordingCorpus(t) {
		if seen[fixture.Name] {
			t.Fatalf("duplicate corpus fixture name %q", fixture.Name)
		}
		seen[fixture.Name] = true
	}
}
