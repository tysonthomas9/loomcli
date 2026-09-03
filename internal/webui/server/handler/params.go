package handler

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ParseStringParam returns the value for key, or "" if absent.
func ParseStringParam(q url.Values, key string) string {
	return q.Get(key)
}

// ParseBoolParam parses a boolean query parameter.
// Returns (false, nil) if the key is absent.
func ParseBoolParam(q url.Values, key string) (bool, error) {
	v := q.Get(key)
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid %s value: %s (must be true or false)", key, v)
	}
	return b, nil
}

// ParseIntParam parses an optional integer query parameter.
// Returns (nil, nil) if the key is absent.
func ParseIntParam(q url.Values, key string) (*int, error) {
	v := q.Get(key)
	if v == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return nil, fmt.Errorf("invalid %s value: %s (must be an integer)", key, v)
	}
	return &n, nil
}

// ParseArrayParam splits repeated, comma-separated query parameters into trimmed strings.
// Returns nil if the key is absent.
func ParseArrayParam(q url.Values, key string) []string {
	values, ok := q[key]
	if !ok {
		return nil
	}
	var result []string
	for _, value := range values {
		result = append(result, SplitAndTrim(value)...)
	}
	return result
}

// ParseDateParams validates and extracts date-range query parameters.
// Each value must be either RFC3339 or date-only (YYYY-MM-DD).
// Returns a map of param→value for non-empty params.
func ParseDateParams(q url.Values, params []string) (map[string]string, error) {
	result := make(map[string]string, len(params))
	for _, param := range params {
		v := q.Get(param)
		if v == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			if _, err2 := time.Parse("2006-01-02", v); err2 != nil {
				return nil, fmt.Errorf("invalid %s: expected RFC3339 format (e.g., 2024-01-15T00:00:00Z) or date (2024-01-15)", param)
			}
		}
		result[param] = v
	}
	return result, nil
}

// SplitAndTrim splits a comma-separated string and trims whitespace from each element.
func SplitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
