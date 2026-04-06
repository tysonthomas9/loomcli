package terminal

import "regexp"

// ansiEscapeRE matches ANSI escape sequences including:
//   - CSI sequences: ESC [ params letter  (colors, cursor, etc.)
//   - OSC sequences: ESC ] ... (ST | BEL)  (window title, hyperlinks, etc.)
//   - Charset switching: ESC ( or ESC ) plus designator
//   - Private modes: ESC followed by =, >, <, %, #
//   - Two-byte escapes: ESC + any single byte
var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\].*?(?:\x1b\\|\x07)|\x1b[()][A-Z0-9]|\x1b[=><%#]|\x1b.`)

// StripANSI removes ANSI escape sequences from s, leaving only visible text.
func StripANSI(s string) string {
	return ansiEscapeRE.ReplaceAllLiteralString(s, "")
}
