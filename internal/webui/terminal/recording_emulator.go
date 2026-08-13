package terminal

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const maxUnhandledSequencePrefixes = 16

type recordingStyle struct {
	FG        string `json:"fg,omitempty"`
	BG        string `json:"bg,omitempty"`
	Bold      bool   `json:"bold,omitempty"`
	Italic    bool   `json:"italic,omitempty"`
	Underline bool   `json:"underline,omitempty"`
	Inverse   bool   `json:"inverse,omitempty"`
}

type recordingCell struct {
	Text  string         `json:"text,omitempty"`
	Style recordingStyle `json:"style,omitempty"`
	Wide  bool           `json:"wide,omitempty"`
}

type recordingPrimaryState struct {
	Screen       [][]recordingCell `json:"screen"`
	CursorX      int               `json:"cursorX"`
	CursorY      int               `json:"cursorY"`
	SavedX       int               `json:"savedX"`
	SavedY       int               `json:"savedY"`
	Style        recordingStyle    `json:"style"`
	ScrollTop    int               `json:"scrollTop"`
	ScrollBottom int               `json:"scrollBottom"`
	WrapPending  bool              `json:"wrapPending"`
}

// recordingEmulatorState is serialized in the indexer checkpoint. The ANSI
// parser itself is checkpointed only while in its ground state, so all parser
// state needed after restore lives here.
type recordingEmulatorState struct {
	Cols               int                         `json:"cols"`
	Rows               int                         `json:"rows"`
	Screen             [][]recordingCell           `json:"screen"`
	CursorX            int                         `json:"cursorX"`
	CursorY            int                         `json:"cursorY"`
	SavedX             int                         `json:"savedX"`
	SavedY             int                         `json:"savedY"`
	Style              recordingStyle              `json:"style"`
	ScrollTop          int                         `json:"scrollTop"`
	ScrollBottom       int                         `json:"scrollBottom"`
	WrapPending        bool                        `json:"wrapPending"`
	AutoWrap           bool                        `json:"autoWrap"`
	OriginMode         bool                        `json:"originMode"`
	CommitBlocked      bool                        `json:"commitBlocked,omitempty"`
	Alt                bool                        `json:"alt"`
	EverAlt            bool                        `json:"everAlt"`
	Primary            *recordingPrimaryState      `json:"primary,omitempty"`
	LastFrameOff       uint64                      `json:"lastFrameOff"`
	LastTimestamp      int64                       `json:"lastTimestamp"`
	UnhandledSequences RecordingUnhandledSequences `json:"unhandledSequences"`
}

type recordingEmulator struct {
	recordingEmulatorState
	parser   *ansi.Parser
	onCommit func([]RecordingRun, int64, uint64) error
	err      error
}

func newRecordingEmulator(cols, rows uint16, onCommit func([]RecordingRun, int64, uint64) error) *recordingEmulator {
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	e := &recordingEmulator{
		recordingEmulatorState: recordingEmulatorState{
			Cols:               int(cols),
			Rows:               int(rows),
			ScrollBottom:       int(rows) - 1,
			AutoWrap:           true,
			UnhandledSequences: RecordingUnhandledSequences{Prefixes: make(map[string]uint64)},
		},
		onCommit: onCommit,
	}
	e.Screen = makeRecordingScreen(e.Rows, e.Cols)
	e.installParser()
	return e
}

func restoreRecordingEmulator(state recordingEmulatorState, onCommit func([]RecordingRun, int64, uint64) error) *recordingEmulator {
	e := &recordingEmulator{recordingEmulatorState: state, onCommit: onCommit}
	e.normalize()
	e.installParser()
	return e
}

func (e *recordingEmulator) installParser() {
	e.parser = ansi.NewParser()
	e.parser.SetHandler(ansi.Handler{
		Print:     e.printRune,
		Execute:   e.execute,
		HandleCsi: e.handleCSI,
		HandleEsc: e.handleESC,
		HandleDcs: func(cmd ansi.Cmd, params ansi.Params, _ []byte) {
			e.noteUnhandled(formatUnhandledCSI("DCS", cmd, params))
		},
		HandleOsc: func(cmd int, _ []byte) {
			e.noteUnhandled(fmt.Sprintf("OSC %d", cmd))
		},
		HandlePm:  func(_ []byte) { e.noteUnhandled("PM") },
		HandleApc: func(_ []byte) { e.noteUnhandled("APC") },
		HandleSos: func(_ []byte) { e.noteUnhandled("SOS") },
	})
}

func (e *recordingEmulator) normalize() {
	if e.Cols <= 0 {
		e.Cols = 80
	}
	if e.Rows <= 0 {
		e.Rows = 24
	}
	if len(e.Screen) != e.Rows {
		e.Screen = resizeRecordingScreen(e.Screen, e.Rows, e.Cols)
	}
	if e.UnhandledSequences.Prefixes == nil {
		e.UnhandledSequences.Prefixes = make(map[string]uint64)
	}
	for y := range e.Screen {
		if len(e.Screen[y]) != e.Cols {
			row := make([]recordingCell, e.Cols)
			copy(row, e.Screen[y])
			e.Screen[y] = row
		}
	}
	e.CursorX = clampInt(e.CursorX, 0, e.Cols-1)
	e.CursorY = clampInt(e.CursorY, 0, e.Rows-1)
	e.ScrollTop = clampInt(e.ScrollTop, 0, e.Rows-1)
	e.ScrollBottom = clampInt(e.ScrollBottom, e.ScrollTop, e.Rows-1)
}

func (e *recordingEmulator) feed(payload []byte, timestamp int64, frameOffset uint64) error {
	e.LastTimestamp = timestamp
	e.LastFrameOff = frameOffset
	for _, b := range payload {
		e.parser.Advance(b)
		if e.err != nil {
			return e.err
		}
	}
	return nil
}

func (e *recordingEmulator) parserGround() bool {
	return e.parser != nil && e.parser.StateName() == "GroundState"
}

func (e *recordingEmulator) printRune(r rune) {
	if e.err != nil || e.Rows == 0 || e.Cols == 0 {
		return
	}
	width := ansi.StringWidthWc(string(r))
	if width == 0 {
		x := e.CursorX - 1
		if e.WrapPending {
			x = e.CursorX
		}
		for x >= 0 && e.Screen[e.CursorY][x].Wide {
			x--
		}
		if x >= 0 && e.Screen[e.CursorY][x].Text != "" {
			e.Screen[e.CursorY][x].Text += string(r)
		}
		return
	}
	if e.WrapPending && e.AutoWrap {
		e.CursorX = 0
		e.lineFeed()
		e.WrapPending = false
	}
	if width > 1 && e.CursorX == e.Cols-1 {
		if e.AutoWrap {
			e.CursorX = 0
			e.lineFeed()
		}
	}
	e.clearGlyphAt(e.CursorY, e.CursorX)
	if width > 1 && e.CursorX+1 < e.Cols {
		e.clearGlyphAt(e.CursorY, e.CursorX+1)
	}
	e.Screen[e.CursorY][e.CursorX] = recordingCell{Text: string(r), Style: e.Style}
	if width > 1 && e.CursorX+1 < e.Cols {
		e.Screen[e.CursorY][e.CursorX+1] = recordingCell{Wide: true, Style: e.Style}
	}
	if e.CursorX+width >= e.Cols {
		e.CursorX = e.Cols - 1
		e.WrapPending = true
	} else {
		e.CursorX += width
	}
}

func (e *recordingEmulator) clearGlyphAt(y, x int) {
	if y < 0 || y >= e.Rows || x < 0 || x >= e.Cols {
		return
	}
	row := e.Screen[y]
	if row[x].Wide {
		base := x - 1
		for base >= 0 && row[base].Wide {
			base--
		}
		if base >= 0 {
			row[base] = recordingCell{}
		}
	}
	if x+1 < e.Cols && row[x+1].Wide {
		row[x+1] = recordingCell{}
	}
	row[x] = recordingCell{}
}

func (e *recordingEmulator) execute(b byte) {
	switch b {
	case '\b':
		e.CursorX = maxInt(0, e.CursorX-1)
		e.WrapPending = false
	case '\t':
		next := ((e.CursorX / 8) + 1) * 8
		e.CursorX = minInt(e.Cols-1, next)
		e.WrapPending = false
	case '\n', '\v', '\f':
		e.lineFeed()
		e.WrapPending = false
	case '\r':
		e.CursorX = 0
		e.WrapPending = false
	}
}

func (e *recordingEmulator) handleESC(cmd ansi.Cmd) {
	if cmd.Intermediate() != 0 {
		e.noteUnhandled(formatUnhandledESC(cmd))
		return
	}
	handled := true
	switch cmd.Final() {
	case 'D': // IND
		e.lineFeed()
		e.WrapPending = false
	case 'E': // NEL
		e.CursorX = 0
		e.lineFeed()
		e.WrapPending = false
	case 'M': // RI
		if e.CursorY == e.ScrollTop {
			e.scrollDown(1)
		} else {
			e.CursorY = maxInt(0, e.CursorY-1)
		}
		e.WrapPending = false
	case '7':
		e.SavedX, e.SavedY = e.CursorX, e.CursorY
	case '8':
		e.CursorX = clampInt(e.SavedX, 0, e.Cols-1)
		e.CursorY = clampInt(e.SavedY, e.verticalTop(), e.verticalBottom())
		e.WrapPending = false
	case 'c':
		e.reset()
	default:
		handled = false
	}
	if !handled {
		e.noteUnhandled(formatUnhandledESC(cmd))
	}
}

// handleCSI dispatches one branch per CSI final byte. Splitting it up would
// scatter the VT spec across helpers without removing a single decision; the
// golden testdata and the vt10x property tests cover every branch.
//
//nolint:gocognit,cyclop,funlen // the branch count is the size of the VT spec
func (e *recordingEmulator) handleCSI(cmd ansi.Cmd, params ansi.Params) {
	final := cmd.Final()
	p := func(index, def int) int {
		value, _, ok := params.Param(index, def)
		if !ok || value == 0 {
			return def
		}
		return value
	}
	if cmd.Prefix() == '?' && (final == 'h' || final == 'l') {
		set := final == 'h'
		unhandled := false
		params.ForEach(0, func(_ int, mode int, _ bool) {
			switch mode {
			case 6: // DECOM
				e.OriginMode = set
				e.homeCursor()
			case 7: // DECAWM
				e.AutoWrap = set
			case 47, 1047, 1049:
				if set {
					e.enterAltScreen()
				} else {
					e.exitAltScreen()
				}
			case 1048: // save/restore cursor
				if set {
					e.SavedX, e.SavedY = e.CursorX, e.CursorY
				} else {
					e.CursorX = clampInt(e.SavedX, 0, e.Cols-1)
					e.CursorY = clampInt(e.SavedY, e.verticalTop(), e.verticalBottom())
					e.WrapPending = false
				}
			default:
				// Private modes that cannot affect committed history are
				// render/input hints (?2026 sync output, ?25 cursor
				// visibility, mouse/focus reporting, ...); ignore them
				// silently rather than counting them as unsupported.
				if privateModeCanAffectCommit(mode) {
					unhandled = true
					e.CommitBlocked = true
				}
			}
		})
		if unhandled {
			e.noteUnhandled(formatUnhandledCSI("CSI", cmd, params))
		}
		return
	}
	if cmd.Prefix() != 0 || cmd.Intermediate() != 0 {
		if !benignRenderHintCSI(cmd) {
			e.noteUnhandled(formatUnhandledCSI("CSI", cmd, params))
		}
		return
	}

	movedCursor := false
	handled := true
	switch final {
	case 'A':
		e.CursorY = maxInt(e.verticalTop(), e.CursorY-p(0, 1))
		movedCursor = true
	case 'B', 'e':
		e.CursorY = minInt(e.verticalBottom(), e.CursorY+p(0, 1))
		movedCursor = true
	case 'C', 'a':
		e.CursorX = minInt(e.Cols-1, e.CursorX+p(0, 1))
		movedCursor = true
	case 'D':
		e.CursorX = maxInt(0, e.CursorX-p(0, 1))
		movedCursor = true
	case 'E':
		e.CursorY = minInt(e.verticalBottom(), e.CursorY+p(0, 1))
		e.CursorX = 0
		movedCursor = true
	case 'F':
		e.CursorY = maxInt(e.verticalTop(), e.CursorY-p(0, 1))
		e.CursorX = 0
		movedCursor = true
	case 'G', '`':
		e.CursorX = clampInt(p(0, 1)-1, 0, e.Cols-1)
		movedCursor = true
	case 'H', 'f':
		e.CursorY = e.absoluteRow(p(0, 1) - 1)
		e.CursorX = clampInt(p(1, 1)-1, 0, e.Cols-1)
		movedCursor = true
	case 'd':
		e.CursorY = e.absoluteRow(p(0, 1) - 1)
		movedCursor = true
	case 'J':
		e.eraseDisplay(csiRawParam(params, 0, 0))
	case 'K':
		e.eraseLine(csiRawParam(params, 0, 0))
	case 'S':
		e.scrollUp(p(0, 1))
	case 'T':
		e.scrollDown(p(0, 1))
	case 'L':
		e.insertLines(p(0, 1))
	case 'M':
		e.deleteLines(p(0, 1))
	case '@':
		e.insertChars(p(0, 1))
	case 'P':
		e.deleteChars(p(0, 1))
	case 'X':
		e.eraseChars(p(0, 1))
	case 'm':
		if !e.applySGR(params) {
			e.noteUnhandled(formatUnhandledCSI("CSI", cmd, params))
		}
	case 'r':
		top := p(0, 1)
		bottom := minInt(p(1, e.Rows), e.Rows)
		// Xterm ignores an invalid region entirely, including the usual cursor
		// home. A bottom beyond the screen is clamped before validity is tested.
		if top >= 1 && top < bottom && top < e.Rows {
			e.ScrollTop, e.ScrollBottom = top-1, bottom-1
			e.homeCursor()
			movedCursor = true
		}
	case 's':
		e.SavedX, e.SavedY = e.CursorX, e.CursorY
	case 'u':
		e.CursorX = clampInt(e.SavedX, 0, e.Cols-1)
		e.CursorY = clampInt(e.SavedY, e.verticalTop(), e.verticalBottom())
		movedCursor = true
	default:
		handled = false
	}
	if !handled {
		e.noteUnhandled(formatUnhandledCSI("CSI", cmd, params))
	}
	if movedCursor {
		e.WrapPending = false
	}
}

func (e *recordingEmulator) lineFeed() {
	if e.CursorY == e.ScrollBottom {
		e.scrollUp(1)
		return
	}
	e.CursorY = minInt(e.Rows-1, e.CursorY+1)
}

func (e *recordingEmulator) scrollUp(n int) {
	n = clampInt(n, 1, e.ScrollBottom-e.ScrollTop+1)
	for range n {
		removed := e.Screen[e.ScrollTop]
		if !e.Alt && !e.CommitBlocked && e.ScrollTop == 0 && e.onCommit != nil && e.err == nil {
			e.err = e.onCommit(rowRuns(removed), e.LastTimestamp, e.LastFrameOff)
		}
		copy(e.Screen[e.ScrollTop:e.ScrollBottom], e.Screen[e.ScrollTop+1:e.ScrollBottom+1])
		e.Screen[e.ScrollBottom] = make([]recordingCell, e.Cols)
	}
}

func (e *recordingEmulator) scrollDown(n int) {
	n = clampInt(n, 1, e.ScrollBottom-e.ScrollTop+1)
	for range n {
		copy(e.Screen[e.ScrollTop+1:e.ScrollBottom+1], e.Screen[e.ScrollTop:e.ScrollBottom])
		e.Screen[e.ScrollTop] = make([]recordingCell, e.Cols)
	}
}

func (e *recordingEmulator) resize(cols, rows uint16) {
	newCols, newRows := int(cols), int(rows)
	if newCols <= 0 || newRows <= 0 || (newCols == e.Cols && newRows == e.Rows) {
		return
	}
	if newRows < e.Rows {
		// Match terminal resize behavior: only slide rows off the top when
		// needed to keep the cursor visible. Merely trimming blank space below
		// the cursor must not evict immutable history.
		slide := maxInt(0, e.CursorY-newRows+1)
		for i := 0; i < slide; i++ {
			if !e.Alt && !e.CommitBlocked && e.onCommit != nil && e.err == nil {
				e.err = e.onCommit(rowRuns(e.Screen[i]), e.LastTimestamp, e.LastFrameOff)
			}
		}
		e.Screen = resizeRecordingScreen(e.Screen[slide:], newRows, newCols)
		e.CursorY = maxInt(0, e.CursorY-slide)
	} else {
		e.Screen = resizeRecordingScreen(e.Screen, newRows, newCols)
	}
	if e.Primary != nil {
		primarySlide := 0
		if newRows < e.Rows {
			primarySlide = maxInt(0, e.Primary.CursorY-newRows+1)
			for i := 0; i < primarySlide; i++ {
				if !e.CommitBlocked && e.onCommit != nil && e.err == nil {
					e.err = e.onCommit(rowRuns(e.Primary.Screen[i]), e.LastTimestamp, e.LastFrameOff)
				}
			}
		}
		e.Primary.Screen = resizeRecordingScreen(e.Primary.Screen[primarySlide:], newRows, newCols)
		e.Primary.CursorX = clampInt(e.Primary.CursorX, 0, newCols-1)
		e.Primary.CursorY = clampInt(e.Primary.CursorY-primarySlide, 0, newRows-1)
		e.Primary.SavedY = clampInt(e.Primary.SavedY-primarySlide, 0, newRows-1)
		e.Primary.ScrollTop, e.Primary.ScrollBottom = 0, newRows-1
		e.Primary.WrapPending = false
	}
	e.Cols, e.Rows = newCols, newRows
	e.ScrollTop, e.ScrollBottom = 0, newRows-1
	e.CursorX = clampInt(e.CursorX, 0, newCols-1)
	e.CursorY = clampInt(e.CursorY, 0, newRows-1)
	e.WrapPending = false
}

func (e *recordingEmulator) resizeRows(rows uint16) {
	e.resize(uint16(e.Cols), rows)
}

func (e *recordingEmulator) enterAltScreen() {
	if e.Alt {
		return
	}
	e.Primary = &recordingPrimaryState{
		Screen: e.Screen, CursorX: e.CursorX, CursorY: e.CursorY,
		SavedX: e.SavedX, SavedY: e.SavedY, Style: e.Style,
		ScrollTop: e.ScrollTop, ScrollBottom: e.ScrollBottom,
		WrapPending: e.WrapPending,
	}
	e.Screen = makeRecordingScreen(e.Rows, e.Cols)
	e.CursorX, e.CursorY, e.SavedX, e.SavedY = 0, 0, 0, 0
	e.ScrollTop, e.ScrollBottom = 0, e.Rows-1
	e.WrapPending = false
	e.Alt, e.EverAlt = true, true
}

func (e *recordingEmulator) exitAltScreen() {
	if !e.Alt {
		return
	}
	if e.Primary != nil {
		e.Screen = e.Primary.Screen
		e.CursorX, e.CursorY = e.Primary.CursorX, e.Primary.CursorY
		e.SavedX, e.SavedY = e.Primary.SavedX, e.Primary.SavedY
		e.Style = e.Primary.Style
		e.ScrollTop, e.ScrollBottom = e.Primary.ScrollTop, e.Primary.ScrollBottom
		e.WrapPending = e.Primary.WrapPending
	}
	e.Primary = nil
	e.Alt = false
	e.normalize()
}

func (e *recordingEmulator) reset() {
	e.Screen = makeRecordingScreen(e.Rows, e.Cols)
	e.CursorX, e.CursorY, e.SavedX, e.SavedY = 0, 0, 0, 0
	e.ScrollTop, e.ScrollBottom = 0, e.Rows-1
	e.Style = recordingStyle{}
	e.WrapPending = false
	e.AutoWrap = true
	e.OriginMode = false
	e.CommitBlocked = false
}

func (e *recordingEmulator) verticalTop() int {
	if e.OriginMode {
		return e.ScrollTop
	}
	return 0
}

func (e *recordingEmulator) verticalBottom() int {
	if e.OriginMode {
		return e.ScrollBottom
	}
	return e.Rows - 1
}

func (e *recordingEmulator) absoluteRow(row int) int {
	if e.OriginMode {
		row += e.ScrollTop
	}
	return clampInt(row, e.verticalTop(), e.verticalBottom())
}

func (e *recordingEmulator) homeCursor() {
	e.CursorX = 0
	e.CursorY = e.absoluteRow(0)
	e.WrapPending = false
}

// These private modes can change geometry, horizontal reachability, or cursor
// restoration. Until implemented, continuing to commit would make immutable
// row addressing unsound. Harmless display/input modes remain diagnostic only.
func privateModeCanAffectCommit(mode int) bool {
	switch mode {
	case 3, // DECCOLM (80/132 columns)
		40,   // allow DECCOLM
		45,   // reverse wraparound
		69,   // left/right margin mode
		95,   // suppress clear on DECCOLM
		2027: // grapheme clustering changes width semantics
		return true
	default:
		return false
	}
}

// benignRenderHintCSI reports whether a prefixed/intermediate CSI is a known
// render or input hint with no effect on committed history: DECSCUSR
// (CSI Ps SP q), DECRQM (CSI ? Ps $ p), and the kitty keyboard protocol
// (CSI > ... u / CSI = ... u).
func benignRenderHintCSI(cmd ansi.Cmd) bool {
	switch {
	case cmd.Final() == 'q' && cmd.Intermediate() == ' ':
		return true
	case cmd.Final() == 'p' && cmd.Intermediate() == '$':
		return true
	case cmd.Final() == 'u' && (cmd.Prefix() == '>' || cmd.Prefix() == '='):
		return true
	default:
		return false
	}
}

func (e *recordingEmulator) eraseDisplay(mode int) {
	switch mode {
	case 1:
		for y := 0; y < e.CursorY; y++ {
			e.Screen[y] = make([]recordingCell, e.Cols)
		}
		for x := 0; x <= e.CursorX; x++ {
			e.Screen[e.CursorY][x] = recordingCell{}
		}
	case 2, 3:
		for y := range e.Screen {
			e.Screen[y] = make([]recordingCell, e.Cols)
		}
	default:
		for x := e.CursorX; x < e.Cols; x++ {
			e.Screen[e.CursorY][x] = recordingCell{}
		}
		for y := e.CursorY + 1; y < e.Rows; y++ {
			e.Screen[y] = make([]recordingCell, e.Cols)
		}
	}
}

func (e *recordingEmulator) eraseLine(mode int) {
	start, end := e.CursorX, e.Cols
	if mode == 1 {
		start, end = 0, e.CursorX+1
	} else if mode == 2 {
		start, end = 0, e.Cols
	}
	for x := start; x < end; x++ {
		e.Screen[e.CursorY][x] = recordingCell{}
	}
}

func (e *recordingEmulator) insertLines(n int) {
	if e.CursorY < e.ScrollTop || e.CursorY > e.ScrollBottom {
		return
	}
	n = clampInt(n, 1, e.ScrollBottom-e.CursorY+1)
	copy(e.Screen[e.CursorY+n:e.ScrollBottom+1], e.Screen[e.CursorY:e.ScrollBottom+1-n])
	for y := e.CursorY; y < e.CursorY+n; y++ {
		e.Screen[y] = make([]recordingCell, e.Cols)
	}
}

func (e *recordingEmulator) deleteLines(n int) {
	if e.CursorY < e.ScrollTop || e.CursorY > e.ScrollBottom {
		return
	}
	n = clampInt(n, 1, e.ScrollBottom-e.CursorY+1)
	copy(e.Screen[e.CursorY:e.ScrollBottom+1-n], e.Screen[e.CursorY+n:e.ScrollBottom+1])
	for y := e.ScrollBottom + 1 - n; y <= e.ScrollBottom; y++ {
		e.Screen[y] = make([]recordingCell, e.Cols)
	}
}

func (e *recordingEmulator) insertChars(n int) {
	n = clampInt(n, 1, e.Cols-e.CursorX)
	row := e.Screen[e.CursorY]
	copy(row[e.CursorX+n:], row[e.CursorX:e.Cols-n])
	for x := e.CursorX; x < e.CursorX+n; x++ {
		row[x] = recordingCell{}
	}
}

func (e *recordingEmulator) deleteChars(n int) {
	n = clampInt(n, 1, e.Cols-e.CursorX)
	row := e.Screen[e.CursorY]
	copy(row[e.CursorX:], row[e.CursorX+n:])
	for x := e.Cols - n; x < e.Cols; x++ {
		row[x] = recordingCell{}
	}
}

func (e *recordingEmulator) eraseChars(n int) {
	n = clampInt(n, 1, e.Cols-e.CursorX)
	for x := e.CursorX; x < e.CursorX+n; x++ {
		e.Screen[e.CursorY][x] = recordingCell{}
	}
}

// applySGR is a flat switch over the SGR code table. Its branch count is the
// size of that table, not the complexity of the logic.
//
//nolint:cyclop,funlen // the branch count is the size of the SGR table
func (e *recordingEmulator) applySGR(params ansi.Params) bool {
	if len(params) == 0 {
		e.Style = recordingStyle{}
		return true
	}
	fullyHandled := true
	values := make([]int, 0, len(params))
	params.ForEach(0, func(_ int, value int, _ bool) { values = append(values, value) })
	for i := 0; i < len(values); i++ {
		value := values[i]
		switch {
		case value == 0:
			e.Style = recordingStyle{}
		case value == 1:
			e.Style.Bold = true
		case value == 3:
			e.Style.Italic = true
		case value == 4:
			e.Style.Underline = true
		case value == 7:
			e.Style.Inverse = true
		case value == 22:
			e.Style.Bold = false
		case value == 23:
			e.Style.Italic = false
		case value == 24:
			e.Style.Underline = false
		case value == 27:
			e.Style.Inverse = false
		case value >= 30 && value <= 37:
			e.Style.FG = terminalColorHex(ansi.BasicColor(value - 30))
		case value == 39:
			e.Style.FG = ""
		case value >= 40 && value <= 47:
			e.Style.BG = terminalColorHex(ansi.BasicColor(value - 40))
		case value == 49:
			e.Style.BG = ""
		case value >= 90 && value <= 97:
			e.Style.FG = terminalColorHex(ansi.BasicColor(value - 90 + 8))
		case value >= 100 && value <= 107:
			e.Style.BG = terminalColorHex(ansi.BasicColor(value - 100 + 8))
		case value == 38 || value == 48:
			colorHex, consumed := sgrExtendedColor(values[i+1:])
			if consumed > 0 {
				if value == 38 {
					e.Style.FG = colorHex
				} else {
					e.Style.BG = colorHex
				}
				i += consumed
			} else {
				fullyHandled = false
			}
		default:
			fullyHandled = false
		}
	}
	return fullyHandled
}

func sgrExtendedColor(values []int) (string, int) {
	if len(values) >= 2 && values[0] == 5 {
		return terminalColorHex(ansi.IndexedColor(clampInt(values[1], 0, 255))), 2
	}
	if len(values) >= 4 && values[0] == 2 {
		return fmt.Sprintf("#%02x%02x%02x", clampInt(values[1], 0, 255), clampInt(values[2], 0, 255), clampInt(values[3], 0, 255)), 4
	}
	return "", 0
}

func terminalColorHex(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

func (e *recordingEmulator) noteUnhandled(prefix string) {
	if prefix == "" {
		prefix = "unknown"
	}
	if len(prefix) > 64 {
		prefix = prefix[:64]
	}
	summary := &e.UnhandledSequences
	summary.Count++
	if summary.Prefixes == nil {
		summary.Prefixes = make(map[string]uint64)
	}
	if _, ok := summary.Prefixes[prefix]; ok {
		summary.Prefixes[prefix]++
		return
	}
	if len(summary.Prefixes) < maxUnhandledSequencePrefixes {
		summary.Prefixes[prefix] = 1
		return
	}

	// Space-Saving keeps an O(1)-bounded approximation of the most common
	// prefixes while Count remains exact. Lexical tie-breaking makes recovery
	// and chunk-invariance tests deterministic despite Go map iteration order.
	var victim string
	var minimum uint64
	for candidate, count := range summary.Prefixes {
		if victim == "" || count < minimum || (count == minimum && candidate < victim) {
			victim, minimum = candidate, count
		}
	}
	delete(summary.Prefixes, victim)
	summary.Prefixes[prefix] = minimum + 1
}

func (e *recordingEmulator) unhandledSequenceSummary() RecordingUnhandledSequences {
	result := RecordingUnhandledSequences{
		Count:    e.UnhandledSequences.Count,
		Prefixes: make(map[string]uint64, len(e.UnhandledSequences.Prefixes)),
	}
	for prefix, count := range e.UnhandledSequences.Prefixes {
		result.Prefixes[prefix] = count
	}
	return result
}

func formatUnhandledESC(cmd ansi.Cmd) string {
	var sequence strings.Builder
	sequence.WriteString("ESC ")
	if cmd.Intermediate() != 0 {
		sequence.WriteByte(cmd.Intermediate())
	}
	sequence.WriteByte(cmd.Final())
	return sequence.String()
}

func formatUnhandledCSI(kind string, cmd ansi.Cmd, params ansi.Params) string {
	var sequence strings.Builder
	sequence.WriteString(kind)
	sequence.WriteByte(' ')
	if cmd.Prefix() != 0 {
		sequence.WriteByte(cmd.Prefix())
	}
	params.ForEach(0, func(index, value int, hasMore bool) {
		if index > 0 {
			sequence.WriteByte(';')
		}
		_, _ = fmt.Fprintf(&sequence, "%d", value)
		if hasMore {
			sequence.WriteByte(':')
		}
	})
	if cmd.Intermediate() != 0 {
		sequence.WriteByte(cmd.Intermediate())
	}
	sequence.WriteByte(cmd.Final())
	return sequence.String()
}

func (e *recordingEmulator) screenRows() []RecordingLine {
	if e.Alt && e.Primary != nil {
		return rowsFromCells(e.Primary.Screen, e.Primary.CursorY, e.LastTimestamp, e.LastFrameOff)
	}
	return rowsFromCells(e.Screen, e.CursorY, e.LastTimestamp, e.LastFrameOff)
}

// activeScreenRows returns the rows of the buffer the terminal is currently
// drawing into (the alt screen while a fullscreen TUI is up). screenRows, by
// contrast, always returns the primary screen because committed history never
// includes alt-screen content. Attach replay must repaint what is actually on
// screen, so it uses this accessor.
func (e *recordingEmulator) activeScreenRows() []RecordingLine {
	return rowsFromCells(e.Screen, e.CursorY, e.LastTimestamp, e.LastFrameOff)
}

func rowsFromCells(screen [][]recordingCell, cursorY int, timestamp int64, offset uint64) []RecordingLine {
	last := clampInt(cursorY, 0, maxInt(0, len(screen)-1))
	for y := len(screen) - 1; y >= 0; y-- {
		if !rowEmpty(screen[y]) {
			last = maxInt(last, y)
			break
		}
	}
	rows := make([]RecordingLine, 0, last+1)
	for y := 0; y <= last && y < len(screen); y++ {
		rows = append(rows, RecordingLine{Timestamp: timestamp, Offset: OpaqueRecordingOffset(offset), Cols: uint16(len(screen[y])), Runs: rowRuns(screen[y])})
	}
	return rows
}

func rowRuns(row []recordingCell) []RecordingRun {
	end := len(row)
	for end > 0 && row[end-1].Text == "" && !row[end-1].Wide {
		end--
	}
	if end == 0 {
		return []RecordingRun{}
	}
	runs := make([]RecordingRun, 0, 4)
	for _, cell := range row[:end] {
		if cell.Wide {
			continue
		}
		text := cell.Text
		if text == "" {
			text = " "
		}
		run := RecordingRun{
			Text: text, FG: cell.Style.FG, BG: cell.Style.BG,
			Bold: cell.Style.Bold, Italic: cell.Style.Italic,
			Underline: cell.Style.Underline, Inverse: cell.Style.Inverse,
		}
		if len(runs) > 0 && sameRunStyle(runs[len(runs)-1], run) {
			runs[len(runs)-1].Text += text
		} else {
			runs = append(runs, run)
		}
	}
	return runs
}

func sameRunStyle(a, b RecordingRun) bool {
	return a.FG == b.FG && a.BG == b.BG && a.Bold == b.Bold &&
		a.Italic == b.Italic && a.Underline == b.Underline && a.Inverse == b.Inverse
}

func rowEmpty(row []recordingCell) bool {
	for _, cell := range row {
		if cell.Text != "" && strings.TrimSpace(cell.Text) != "" {
			return false
		}
	}
	return true
}

func makeRecordingScreen(rows, cols int) [][]recordingCell {
	screen := make([][]recordingCell, rows)
	for y := range screen {
		screen[y] = make([]recordingCell, cols)
	}
	return screen
}

func resizeRecordingScreen(screen [][]recordingCell, rows, cols int) [][]recordingCell {
	result := makeRecordingScreen(rows, cols)
	for y := 0; y < minInt(rows, len(screen)); y++ {
		copy(result[y], screen[y])
	}
	return result
}

func csiRawParam(params ansi.Params, index, def int) int {
	value, _, ok := params.Param(index, def)
	if !ok {
		return def
	}
	return value
}

func clampInt(value, low, high int) int {
	if high < low {
		return low
	}
	return minInt(high, maxInt(low, value))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
