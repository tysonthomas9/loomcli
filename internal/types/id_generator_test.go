package types

import (
	"regexp"
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

	t.Run("valid 64-char hex string", func(t *testing.T) {
		h := GenerateHashID("bd", "title", "desc", ts, "ws")
		if len(h) != 64 {
			t.Errorf("expected 64-char hash, got %d chars: %s", len(h), h)
		}
		matched, _ := regexp.MatchString("^[0-9a-f]{64}$", h)
		if !matched {
			t.Errorf("expected valid lowercase hex string, got: %s", h)
		}
	})
}
