package types

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestGenerateHashID_FieldSeparation(t *testing.T) {
	ts := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("title/description boundary shift produces different hash", func(t *testing.T) {
		h1 := GenerateHashID("bd", "ab", "cd", ts, "ws")
		h2 := GenerateHashID("bd", "abc", "d", ts, "ws")
		if h1 == h2 {
			t.Errorf("expected different hashes for shifted title/description boundary, got same: %s", h1)
		}
	})

	t.Run("another title/description boundary shift produces different hash", func(t *testing.T) {
		h1 := GenerateHashID("bd", "a", "bcd", ts, "ws")
		h2 := GenerateHashID("bd", "ab", "cd", ts, "ws")
		if h1 == h2 {
			t.Errorf("expected different hashes for shifted title/description boundary, got same: %s", h1)
		}
	})

	t.Run("deterministic output", func(t *testing.T) {
		h1 := GenerateHashID("bd", "title", "desc", ts, "ws")
		h2 := GenerateHashID("bd", "title", "desc", ts, "ws")
		if h1 != h2 {
			t.Errorf("expected identical hashes for same inputs, got %s and %s", h1, h2)
		}
	})

	t.Run("non-empty result", func(t *testing.T) {
		h := GenerateHashID("bd", "title", "desc", ts, "ws")
		if h == "" {
			t.Error("expected non-empty hash")
		}
	})

	t.Run("returns prefix-{64-char-hex}", func(t *testing.T) {
		h := GenerateHashID("bd", "title", "desc", ts, "ws")
		if !strings.HasPrefix(h, "bd-") {
			t.Errorf("expected hash to start with 'bd-', got: %s", h)
		}
		hexPart := strings.TrimPrefix(h, "bd-")
		if len(hexPart) != 64 {
			t.Errorf("expected 64-char hex after prefix, got %d chars: %s", len(hexPart), hexPart)
		}
		matched, _ := regexp.MatchString("^[0-9a-f]{64}$", hexPart)
		if !matched {
			t.Errorf("expected valid lowercase hex string after prefix, got: %s", hexPart)
		}
	})

	t.Run("different prefix produces different hash", func(t *testing.T) {
		h1 := GenerateHashID("bd", "title", "desc", ts, "ws")
		h2 := GenerateHashID("proj", "title", "desc", ts, "ws")
		if h1 == h2 {
			t.Errorf("expected different hashes for different prefixes, got same: %s", h1)
		}
		// Also verify different prefixes are reflected in the output prefix
		if !strings.HasPrefix(h1, "bd-") {
			t.Errorf("expected h1 to start with 'bd-', got: %s", h1)
		}
		if !strings.HasPrefix(h2, "proj-") {
			t.Errorf("expected h2 to start with 'proj-', got: %s", h2)
		}
	})

	t.Run("same prefix same other fields produces same hash", func(t *testing.T) {
		h1 := GenerateHashID("bd", "title", "desc", ts, "ws")
		h2 := GenerateHashID("bd", "title", "desc", ts, "ws")
		if h1 != h2 {
			t.Errorf("expected identical hashes for identical inputs, got %s and %s", h1, h2)
		}
	})
}
