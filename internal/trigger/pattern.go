// Package trigger contains control-plane router primitives for trigger
// bindings. It is deliberately separate from internal/driver: this is the
// routing layer used by the memstore dispatch path so that embedded/local
// mode behaves exactly like fleet-db.
//
// The pattern engine in this file MUST stay in lockstep with
// fleet-db/internal/routing/pattern.go (chunk C1). Both packages share the
// same grammar and exported surface, and pattern_test.go duplicates the C1
// test vector table verbatim so any divergence fails tests in both repos.
//
// Grammar (locked decision): a pattern is a dot-segmented glob matched
// against a route key segment-by-segment.
//
//   - an exact segment matches the identical key segment (case-sensitive)
//   - "*" matches any single segment (it never spans dots)
//   - "{a,b,c}" matches if the key segment equals any listed alternative
//
// Route keys themselves stay exact ingress strings; patterns appear only in
// a binding's event_type_patterns. Matching is split-and-compare only — no
// regex is ever compiled from user input.
package trigger

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidPattern is the domain sentinel returned (wrapped) for malformed
// route-key patterns.
var ErrInvalidPattern = errors.New("invalid route-key pattern")

// Match reports whether routeKey matches pattern. A malformed pattern
// returns an error wrapping ErrInvalidPattern; a well-formed pattern that
// simply does not match returns (false, nil). Patterns and route keys with
// different segment counts never match.
func Match(pattern, routeKey string) (bool, error) {
	segs, err := parsePattern(pattern)
	if err != nil {
		return false, err
	}
	keySegs := strings.Split(routeKey, ".")
	if len(keySegs) != len(segs) {
		return false, nil
	}
	for i, seg := range segs {
		if !seg.matches(keySegs[i]) {
			return false, nil
		}
	}
	return true, nil
}

// MatchAny reports whether any of patterns matches routeKey. Malformed
// patterns are skipped: ValidatePattern at binding-create time is the
// enforcement point, so a bad pattern that slipped through must not block
// the remaining patterns from matching.
func MatchAny(patterns []string, routeKey string) bool {
	for _, p := range patterns {
		ok, err := Match(p, routeKey)
		if err == nil && ok {
			return true
		}
	}
	return false
}

// ValidatePattern checks that pattern conforms to the grammar. It is meant
// for binding-create validation. Errors wrap ErrInvalidPattern.
func ValidatePattern(pattern string) error {
	_, err := parsePattern(pattern)
	return err
}

// segment is one parsed pattern segment.
type segment struct {
	wildcard bool
	literal  string
	alts     []string
}

func (s segment) matches(key string) bool {
	if s.wildcard {
		return true
	}
	if s.alts != nil {
		for _, a := range s.alts {
			if a == key {
				return true
			}
		}
		return false
	}
	return s.literal == key
}

func parsePattern(pattern string) ([]segment, error) {
	if pattern == "" {
		return nil, fmt.Errorf("%w: pattern is empty", ErrInvalidPattern)
	}
	raw := strings.Split(pattern, ".")
	segs := make([]segment, 0, len(raw))
	for i, r := range raw {
		seg, err := parseSegment(r)
		if err != nil {
			return nil, fmt.Errorf("%w: segment %d (%q): %s", ErrInvalidPattern, i, r, err)
		}
		segs = append(segs, seg)
	}
	return segs, nil
}

func parseSegment(s string) (segment, error) {
	if s == "" {
		return segment{}, errors.New("empty segment")
	}
	if s == "*" {
		return segment{wildcard: true}, nil
	}
	if strings.HasPrefix(s, "{") || strings.HasSuffix(s, "}") {
		return parseAlternation(s)
	}
	if strings.ContainsAny(s, "*{}") {
		return segment{}, errors.New("'*', '{' and '}' are only allowed as a whole-segment wildcard or alternation")
	}
	return segment{literal: s}, nil
}

func parseAlternation(s string) (segment, error) {
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return segment{}, errors.New("alternation braces must enclose the whole segment")
	}
	inner := s[1 : len(s)-1]
	if inner == "" {
		return segment{}, errors.New("alternation has no alternatives")
	}
	alts := strings.Split(inner, ",")
	for _, a := range alts {
		if a == "" {
			return segment{}, errors.New("alternation has an empty alternative")
		}
		if strings.ContainsAny(a, "*{}") {
			return segment{}, errors.New("alternation alternatives must be literal segments")
		}
	}
	return segment{alts: alts}, nil
}
