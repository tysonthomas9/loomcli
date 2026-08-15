package agentstate

import (
	"fmt"
	"strings"
)

const (
	fencePrefix      = "<!-- loom:agent:"
	beginFenceSuffix = ":begin -->"
	endFenceSuffix   = ":end -->"
)

// FenceMarkers returns the paired marker grammar for one agent instance.
func FenceMarkers(serviceID string) (begin, end string) {
	return fencePrefix + serviceID + beginFenceSuffix, fencePrefix + serviceID + endFenceSuffix
}

// StripFenceMarkerLines removes agent-instance fence marker lines while
// retaining the content between them and all non-marker lines.
func StripFenceMarkerLines(text string) string {
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if !isFenceMarkerLine(line) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func isFenceMarkerLine(line string) bool {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, fencePrefix) {
		return false
	}
	for _, suffix := range []string{beginFenceSuffix, endFenceSuffix} {
		if strings.HasSuffix(line, suffix) && len(line) > len(fencePrefix)+len(suffix) {
			return true
		}
	}
	return false
}

// MergePendingFence extracts serviceID's first complete fence pair from the
// staged candidate and merges that exact block into the current shared file.
// Other instances' bytes are retained. If the current file contains duplicate
// blocks for serviceID, the first pair is replaced and later pairs are removed.
func MergePendingFence(current, pending, serviceID string) (string, error) {
	begin, end := FenceMarkers(serviceID)
	block, ok := firstFenceBlock(pending, begin, end)
	if !ok {
		return "", fmt.Errorf("pending agents.md has no complete fence pair for agent %q", serviceID)
	}
	return rewriteFence(current, begin, end, block), nil
}

func firstFenceBlock(text, begin, end string) (string, bool) {
	start := strings.Index(text, begin)
	if start < 0 {
		return "", false
	}
	endOffset := strings.Index(text[start+len(begin):], end)
	if endOffset < 0 {
		return "", false
	}
	finish := start + len(begin) + endOffset + len(end)
	return text[start:finish], true
}

func rewriteFence(current, begin, end, block string) string {
	start := strings.Index(current, begin)
	if start < 0 {
		return appendFence(current, block)
	}
	endOffset := strings.Index(current[start+len(begin):], end)
	if endOffset < 0 {
		// An unmatched begin marker is still owned by this instance. Replace it
		// in place rather than appending a second ambiguous begin marker.
		suffix := current[start+len(begin):]
		return current[:start] + block + removeExtraFencePairs(suffix, begin, end)
	}
	finish := start + len(begin) + endOffset + len(end)
	return current[:start] + block + removeExtraFencePairs(current[finish:], begin, end)
}

func removeExtraFencePairs(text, begin, end string) string {
	for {
		start := strings.Index(text, begin)
		if start < 0 {
			return text
		}
		endOffset := strings.Index(text[start+len(begin):], end)
		if endOffset < 0 {
			// Remove a stray duplicate begin marker without claiming adjacent
			// human-authored bytes whose ownership cannot be established.
			text = text[:start] + text[start+len(begin):]
			continue
		}
		finish := start + len(begin) + endOffset + len(end)
		text = text[:start] + text[finish:]
	}
}

func appendFence(current, block string) string {
	if current == "" {
		return block + "\n"
	}
	separator := "\n\n"
	if strings.HasSuffix(current, "\n\n") {
		separator = ""
	} else if strings.HasSuffix(current, "\n") {
		separator = "\n"
	}
	return current + separator + block + "\n"
}
