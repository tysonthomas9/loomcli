// Package redact scrubs secrets out of captured agent evidence before
// Artifacts sends bytes to durable storage. See ORIGIN.md for upstream
// attribution.
//
// Layered detection:
//  1. Entropy — alphanumeric segments with Shannon entropy above 4.5.
//  2. Pattern — gitleaks default ruleset (180+ known secret shapes).
//
// A substring is replaced with "REDACTED" if either method flags it.
//
// Ported from github.com/entireio/cli redact/redact.go
// (MIT, (c) 2026 Entire Inc.).
// Package redact implements the mechanical secret scrubbing adapter used by
// Artifacts evidence policy.
package redact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/zricethezav/gitleaks/v8/detect"
)

// secretPattern matches high-entropy strings that may be secrets.
// / is excluded so we don't match entire file paths as one token.
var secretPattern = regexp.MustCompile(`[A-Za-z0-9+_=-]{10,}`)

// entropyThreshold is the minimum Shannon entropy for a string to be
// considered a secret. 4.5 catches typical API keys and tokens while
// avoiding false positives on common words and identifiers.
const entropyThreshold = 4.5

// RedactedPlaceholder is the replacement text used for redacted secrets.
const RedactedPlaceholder = "REDACTED"

var (
	gitleaksDetector     *detect.Detector
	gitleaksDetectorOnce sync.Once
)

func getDetector() *detect.Detector {
	gitleaksDetectorOnce.Do(func() {
		d, err := detect.NewDetectorDefaultConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "redact: gitleaks detector init failed: %v; "+
				"falling back to entropy-only detection\n", err)
			return
		}
		gitleaksDetector = d
	})
	return gitleaksDetector
}

type region struct{ start, end int }

// String replaces secrets in s with "REDACTED". A substring is redacted if
// EITHER the entropy check or a gitleaks rule flags it.
//
//nolint:gocognit,funlen // Region collection and merge are one security-sensitive redaction algorithm.
func String(s string) string {
	var regions []region

	for _, loc := range secretPattern.FindAllStringIndex(s, -1) {
		start, end := loc[0], loc[1]

		// Don't consume characters that are part of JSON escape sequences.
		// Example: in "controller.go\nmodel.go", the regex could match
		// "nmodel" (consuming 'n' from '\n'); after replacement the '\'
		// would be followed by 'R' from "REDACTED" — invalid '\R'.
		if start > 0 && s[start-1] == '\\' {
			switch s[start] {
			case 'n', 't', 'r', 'b', 'f', 'u', '"', '\\', '/':
				start++
				if end-start < 10 {
					continue
				}
			}
		}

		if shannonEntropy(s[start:end]) > entropyThreshold {
			regions = append(regions, region{start, end})
		}
	}

	if d := getDetector(); d != nil {
		for _, f := range d.DetectString(s) {
			if f.Secret == "" {
				continue
			}
			searchFrom := 0
			for {
				idx := strings.Index(s[searchFrom:], f.Secret)
				if idx < 0 {
					break
				}
				absIdx := searchFrom + idx
				regions = append(regions, region{absIdx, absIdx + len(f.Secret)})
				searchFrom = absIdx + len(f.Secret)
			}
		}
	}

	if len(regions) == 0 {
		return s
	}

	sort.Slice(regions, func(i, j int) bool {
		return regions[i].start < regions[j].start
	})
	merged := []region{regions[0]}
	for _, r := range regions[1:] {
		last := &merged[len(merged)-1]
		if r.start <= last.end {
			if r.end > last.end {
				last.end = r.end
			}
		} else {
			merged = append(merged, r)
		}
	}

	var b strings.Builder
	prev := 0
	for _, r := range merged {
		b.WriteString(s[prev:r.start])
		b.WriteString(RedactedPlaceholder)
		prev = r.end
	}
	b.WriteString(s[prev:])
	return b.String()
}

// Bytes is a []byte wrapper around String. Returns the original slice
// unchanged when no redaction is needed.
func Bytes(b []byte) []byte {
	s := string(b)
	redacted := String(s)
	if redacted == s {
		return b
	}
	return []byte(redacted)
}

// JSONLBytes is a []byte wrapper around JSONLContent.
func JSONLBytes(b []byte) ([]byte, error) {
	s := string(b)
	redacted, err := JSONLContent(s)
	if err != nil {
		return nil, err
	}
	if redacted == s {
		return b, nil
	}
	return []byte(redacted), nil
}

// JSONLContent parses each line as JSON, collects string values that need
// redaction, then performs targeted replacements on the raw JSON bytes.
// Lines with no secrets are returned unchanged, preserving original
// formatting (whitespace, key order).
func JSONLContent(content string) (string, error) {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			b.WriteString(line)
			continue
		}
		var parsed any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
			b.WriteString(String(line))
			continue
		}
		repls := collectJSONLReplacements(parsed)
		if len(repls) == 0 {
			b.WriteString(line)
			continue
		}
		result := line
		for _, r := range repls {
			origJSON, err := jsonEncodeString(r[0])
			if err != nil {
				return "", err
			}
			replJSON, err := jsonEncodeString(r[1])
			if err != nil {
				return "", err
			}
			result = strings.ReplaceAll(result, origJSON, replJSON)
		}
		b.WriteString(result)
	}
	return b.String(), nil
}

func collectJSONLReplacements(v any) [][2]string {
	seen := make(map[string]bool)
	var repls [][2]string
	var walk func(v any)
	walk = func(v any) {
		switch val := v.(type) {
		case map[string]any:
			if shouldSkipJSONLObject(val) {
				return
			}
			for k, child := range val {
				if shouldSkipJSONLField(k) {
					continue
				}
				walk(child)
			}
		case []any:
			for _, child := range val {
				walk(child)
			}
		case string:
			redacted := String(val)
			if redacted != val && !seen[val] {
				seen[val] = true
				repls = append(repls, [2]string{val, redacted})
			}
		}
	}
	walk(v)
	return repls
}

// shouldSkipJSONLField: structural fields that are never secrets. Uses an
// explicit allowlist of field names known to hold IDs/paths in the native
// transcripts we parse (Claude Code, Codex, OpenCode). Avoid loose suffix
// matches like HasSuffix("id") — those incorrectly match kid, paid, avoid,
// hybrid, etc. and let real secrets through when stuffed under those fields.
func shouldSkipJSONLField(key string) bool {
	lower := strings.ToLower(key)
	switch lower {
	case "signature":
		return true
	case "id", "uuid":
		return true
	case "sessionid", "session_id",
		"messageid", "message_id",
		"parentuuid", "parent_uuid",
		"tooluseid", "tool_use_id",
		"callid", "call_id",
		"requestid", "request_id",
		"traceid", "trace_id",
		"spanid", "span_id",
		"taskid", "task_id",
		"threadid", "thread_id",
		"eventid", "event_id":
		return true
	case "filepath", "file_path", "cwd", "root", "directory", "dir", "path":
		return true
	}
	return false
}

// shouldSkipJSONLObject: image content blocks contain base64 payloads that
// look like secrets but aren't.
func shouldSkipJSONLObject(obj map[string]any) bool {
	t, ok := obj["type"].(string)
	return ok && (strings.HasPrefix(t, "image") || t == "base64")
}

func shannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	freq := make(map[byte]int)
	for i := range len(s) {
		freq[s[i]]++
	}
	length := float64(len(s))
	var entropy float64
	for _, count := range freq {
		p := float64(count) / length
		entropy -= p * math.Log2(p)
	}
	return entropy
}

// jsonEncodeString returns the JSON encoding of s without HTML escaping.
func jsonEncodeString(s string) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return "", fmt.Errorf("json encode string: %w", err)
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}
