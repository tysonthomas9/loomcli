// Ported from github.com/entireio/cli cmd/entire/cli/transcript/parse.go
// (MIT, (c) 2026 Entire Inc.). See ORIGIN.md.

package artifacttranscript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// ParseFromBytes parses transcript content from a byte slice.
// Uses bufio.Reader to handle arbitrarily long lines.
func ParseFromBytes(content []byte) ([]Line, error) {
	var lines []Line
	reader := bufio.NewReader(bytes.NewReader(content))

	for {
		lineBytes, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("failed to read transcript: %w", err)
		}

		if len(lineBytes) == 0 {
			if err == io.EOF {
				break
			}
			continue
		}

		var line Line
		if err := json.Unmarshal(lineBytes, &line); err == nil {
			normalizeLineType(&line)
			lines = append(lines, line)
		}

		if err == io.EOF {
			break
		}
	}

	return lines, nil
}

// ParseFromFileAtLine reads and parses a transcript file starting from a
// specific line. startLine is 0-indexed. Malformed lines are skipped.
func ParseFromFileAtLine(path string, startLine int) ([]Line, error) {
	file, err := os.Open(path) //nolint:gosec // path is a controlled transcript file path
	if err != nil {
		return nil, fmt.Errorf("failed to open transcript: %w", err)
	}
	defer func() { _ = file.Close() }()

	var lines []Line
	reader := bufio.NewReader(file)

	totalLines := 0
	for {
		lineBytes, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("failed to read transcript: %w", err)
		}

		if len(lineBytes) == 0 {
			if err == io.EOF {
				break
			}
			continue
		}

		if totalLines >= startLine {
			var line Line
			if err := json.Unmarshal(lineBytes, &line); err == nil {
				normalizeLineType(&line)
				lines = append(lines, line)
			}
		}
		totalLines++

		if err == io.EOF {
			break
		}
	}

	return lines, nil
}

// normalizeLineType ensures line.Type is populated for all transcript formats.
// Claude Code uses "type" while Cursor uses "role" for the same purpose.
func normalizeLineType(line *Line) {
	if line.Type == "" && line.Role != "" {
		line.Type = line.Role
	}
}

// SliceFromLine returns content starting at line number startLine (0-indexed).
// Returns empty slice if startLine exceeds the number of lines.
func SliceFromLine(content []byte, startLine int) []byte {
	if len(content) == 0 || startLine <= 0 {
		return content
	}

	lineCount := 0
	offset := 0
	for i, b := range content {
		if b == '\n' {
			lineCount++
			if lineCount == startLine {
				offset = i + 1
				break
			}
		}
	}

	if lineCount < startLine {
		return nil
	}
	if offset >= len(content) {
		return nil
	}

	return content[offset:]
}

// ExtractUserContent extracts user prompt text from a raw message.
// Handles both string and array content formats. Strips IDE/system context
// tags from the result. Returns empty string when the message cannot be
// parsed or contains no text.
func ExtractUserContent(message json.RawMessage) string {
	var msg UserMessage
	if err := json.Unmarshal(message, &msg); err != nil {
		return ""
	}

	if str, ok := msg.Content.(string); ok {
		return StripIDEContextTags(str)
	}

	if arr, ok := msg.Content.([]interface{}); ok {
		var texts []string
		for _, item := range arr {
			if m, ok := item.(map[string]interface{}); ok {
				if m["type"] == ContentTypeText {
					if text, ok := m["text"].(string); ok {
						texts = append(texts, text)
					}
				}
			}
		}
		if len(texts) > 0 {
			return StripIDEContextTags(strings.Join(texts, "\n\n"))
		}
	}

	return ""
}
