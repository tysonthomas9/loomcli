package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// yamlLineRegex extracts the line number from yaml.v3 error messages.
var yamlLineRegex = regexp.MustCompile(`yaml: line (\d+):`)

// yamlSuggestion maps a yaml error substring to a human-friendly explanation.
type yamlSuggestion struct {
	pattern    string
	suggestion string
}

// yamlSuggestionTable maps common yaml.v3 error patterns to actionable fix suggestions.
var yamlSuggestionTable = []yamlSuggestion{
	{"could not find expected ':'", "A key-value pair is missing its colon. Check that every YAML key has the format `key: value`."},
	{"mapping values are not allowed in this context", "A value appears where a key is expected. Check indentation — YAML uses spaces, not tabs."},
	{"did not find expected key", "Expected a mapping key at this position. Check that all entries at this indentation level are key-value pairs."},
	{"found character that cannot start any token", "An illegal character was found. This is usually a tab character — YAML requires spaces for indentation."},
	{"found a tab character that violates indentation", "Tab characters are not allowed for YAML indentation. Replace all tabs with spaces."},
	{"did not find expected '-' indicator", "Expected a list item (`-`) at this position. Check list syntax: each item starts with `- `."},
	{"did not find expected node content", "Expected a value after a key. Check that the line has content after the colon."},
	{"already defined", "A duplicate key was found. Each key must be unique within its mapping."},
	{"cannot unmarshal", "Wrong value type. Check that the value matches the expected type (string, number, boolean, list, etc.)."},
}

const maxContextLineLen = 120
const maxTabLines = 5

// formatYAMLDiagnostic enriches a YAML parse error with file context lines,
// tab detection, and actionable fix suggestions. Non-YAML errors pass through unchanged.
func FormatYAMLDiagnostic(filePath string, rawErr error) string {
	errMsg := rawErr.Error()

	if !strings.Contains(errMsg, "yaml:") {
		return errMsg
	}

	var parts []string
	parts = append(parts, errMsg)

	lineNum := extractYAMLLineNumber(errMsg)

	// Read file for context lines and tab detection
	data, readErr := os.ReadFile(filePath) //nolint:gosec // filePath is from known config paths, not user input
	if readErr == nil && len(data) > 0 {
		lines := strings.Split(string(data), "\n")
		parts = appendContextLines(parts, lines, lineNum)
		parts = appendTabWarning(parts, lines)
	}

	parts = appendSuggestion(parts, errMsg)
	return strings.Join(parts, "\n")
}

func extractYAMLLineNumber(errMsg string) int {
	if m := yamlLineRegex.FindStringSubmatch(errMsg); len(m) >= 2 {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

func appendContextLines(parts []string, lines []string, lineNum int) []string {
	totalLines := len(lines)
	if lineNum <= 0 || lineNum > totalLines {
		return parts
	}

	start := lineNum - 2
	if start < 1 {
		start = 1
	}
	end := lineNum + 2
	if end > totalLines {
		end = totalLines
	}

	parts = append(parts, "")
	for i := start; i <= end; i++ {
		line := lines[i-1]
		if len(line) > maxContextLineLen {
			line = line[:maxContextLineLen] + "..."
		}
		if i == lineNum {
			parts = append(parts, fmt.Sprintf(">>> %4d | %s", i, line))
		} else {
			parts = append(parts, fmt.Sprintf("    %4d | %s", i, line))
		}
	}
	return parts
}

func appendTabWarning(parts []string, lines []string) []string {
	var tabLines []int
	for i, line := range lines {
		if strings.Contains(line, "\t") {
			tabLines = append(tabLines, i+1)
		}
	}
	if len(tabLines) == 0 {
		return parts
	}

	show := tabLines
	extra := 0
	if len(show) > maxTabLines {
		extra = len(show) - maxTabLines
		show = show[:maxTabLines]
	}

	lineStrs := make([]string, len(show))
	for i, ln := range show {
		lineStrs[i] = strconv.Itoa(ln)
	}

	note := fmt.Sprintf("Note: file contains tab character(s) at line(s) %s", strings.Join(lineStrs, ", "))
	if extra > 0 {
		note += fmt.Sprintf(" and %d more", extra)
	}
	note += " — YAML requires spaces for indentation"

	parts = append(parts, "")
	return append(parts, note)
}

func appendSuggestion(parts []string, errMsg string) []string {
	for _, s := range yamlSuggestionTable {
		if strings.Contains(errMsg, s.pattern) {
			parts = append(parts, "")
			return append(parts, "Fix: "+s.suggestion)
		}
	}
	return parts
}
