package terminal

import (
	"bytes"
	"fmt"
)

// maxTrailingSequenceScan bounds how far back the attach path looks for an
// unterminated escape sequence at the end of the raw output stream. Torn
// sequences are at most a few bytes for CSI; only pathological OSC payloads
// (window titles, hyperlinks) could exceed this, and those degrade to the
// no-fragment behavior.
const maxTrailingSequenceScan = 4096

// renderRecorderScreen converts emulator screen rows into a boundary-safe
// ANSI byte stream that repaints the active screen after a clear+home. Rows
// are positioned absolutely so blank rows can be skipped, every row's styling
// is reset before the next, and the cursor lands at its recorded position.
// Returns nil for a pristine screen (no content, cursor at origin) so a fresh
// session replays nothing, matching the raw-ring behavior.
func renderRecorderScreen(screen []RecordingLine, cursorX, cursorY int) []byte {
	var buf bytes.Buffer
	for i, line := range screen {
		if len(line.Runs) == 0 {
			continue
		}
		fmt.Fprintf(&buf, "\x1b[%d;1H", i+1)
		for _, run := range line.Runs {
			buf.WriteString(sgrForRun(run))
			buf.WriteString(run.Text)
		}
		buf.WriteString("\x1b[0m")
	}
	if buf.Len() == 0 && cursorX <= 0 && cursorY <= 0 {
		return nil
	}
	fmt.Fprintf(&buf, "\x1b[%d;%dH", cursorY+1, cursorX+1)
	return buf.Bytes()
}

// sgrForRun emits a full SGR for the run, starting from a reset so no
// attribute leaks between runs regardless of what preceded it.
func sgrForRun(run RecordingRun) string {
	var buf bytes.Buffer
	buf.WriteString("\x1b[0")
	if run.Bold {
		buf.WriteString(";1")
	}
	if run.Italic {
		buf.WriteString(";3")
	}
	if run.Underline {
		buf.WriteString(";4")
	}
	if run.Inverse {
		buf.WriteString(";7")
	}
	if r, g, b, ok := parseHexColor(run.FG); ok {
		fmt.Fprintf(&buf, ";38;2;%d;%d;%d", r, g, b)
	}
	if r, g, b, ok := parseHexColor(run.BG); ok {
		fmt.Fprintf(&buf, ";48;2;%d;%d;%d", r, g, b)
	}
	buf.WriteByte('m')
	return buf.String()
}

// parseHexColor parses "#rrggbb" (the terminalColorHex format used by
// RecordingRun.FG/BG). Empty or malformed values report ok=false, which the
// renderer treats as "default color".
func parseHexColor(s string) (r, g, b uint8, ok bool) {
	if len(s) != 7 || s[0] != '#' {
		return 0, 0, 0, false
	}
	var out [3]uint8
	for i := 0; i < 3; i++ {
		hi, ok1 := hexNibble(s[1+i*2])
		lo, ok2 := hexNibble(s[2+i*2])
		if !ok1 || !ok2 {
			return 0, 0, 0, false
		}
		out[i] = hi<<4 | lo
	}
	return out[0], out[1], out[2], true
}

func hexNibble(c byte) (uint8, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

// incompleteTrailingSequence returns the suffix of raw that begins an escape
// sequence (or multi-byte UTF-8 rune) left unterminated at the end of the
// stream. A recorder-rendered attach replay appends this fragment verbatim:
// the client's next live chunk starts with the sequence's remaining bytes, so
// without the fragment the client would print those bytes as text.
func incompleteTrailingSequence(raw []byte) []byte {
	scan := raw
	if len(scan) > maxTrailingSequenceScan {
		scan = scan[len(scan)-maxTrailingSequenceScan:]
	}
	if esc := bytes.LastIndexByte(scan, 0x1b); esc >= 0 {
		if frag := scan[esc:]; !escapeSequenceComplete(frag) {
			return append([]byte(nil), frag...)
		}
	}
	if frag := trailingPartialUTF8(scan); frag != nil {
		return frag
	}
	return nil
}

// escapeSequenceComplete reports whether frag — which starts at an ESC that
// is the LAST ESC in the stream — is a fully terminated sequence by the end
// of frag. String-style sequences (DCS/SOS/PM/APC) terminate only with ST
// (ESC \), which would itself be a later ESC, so reaching them here means
// they are unterminated; OSC additionally accepts BEL.
func escapeSequenceComplete(frag []byte) bool {
	if len(frag) < 2 {
		return false
	}
	switch frag[1] {
	case '[': // CSI: params/intermediates until a final byte 0x40-0x7E.
		for _, b := range frag[2:] {
			if b >= 0x40 && b <= 0x7E {
				return true
			}
		}
		return false
	case ']': // OSC: BEL terminates; ST would be a later ESC.
		return bytes.IndexByte(frag[2:], 0x07) >= 0
	case 'P', 'X', '^', '_': // DCS/SOS/PM/APC: ST-only termination.
		return false
	default:
		// ESC + intermediates (0x20-0x2F) + one final byte (0x30-0x7E),
		// which covers two-byte sequences like ESC 7 and charset selections
		// like ESC ( B.
		i := 1
		for i < len(frag) && frag[i] >= 0x20 && frag[i] <= 0x2F {
			i++
		}
		return i < len(frag) && frag[i] >= 0x30 && frag[i] <= 0x7E
	}
}

// trailingPartialUTF8 returns a torn multi-byte rune at the end of raw, if
// any: a lead byte whose declared length extends past the end of the stream.
func trailingPartialUTF8(raw []byte) []byte {
	n := len(raw)
	for back := 1; back <= 3 && back <= n; back++ {
		b := raw[n-back]
		if b&0xC0 == 0x80 {
			continue // continuation byte; keep walking to the lead byte
		}
		var need int
		switch {
		case b&0xE0 == 0xC0:
			need = 2
		case b&0xF0 == 0xE0:
			need = 3
		case b&0xF8 == 0xF0:
			need = 4
		default:
			return nil // ASCII or invalid lead; nothing torn
		}
		if back < need {
			return append([]byte(nil), raw[n-back:]...)
		}
		return nil
	}
	return nil
}
