package trigger

import (
	"errors"
	"math/rand"
	"strings"
	"testing"
)

// matchVectors is the shared lockstep test vector table.
//
// IMPORTANT: keep this table byte-for-byte identical to the one in
// fleet-db/internal/routing/pattern_test.go (chunk C1). The two pattern
// engines must behave identically; divergence must fail tests in both repos.
var matchVectors = []struct {
	name    string
	pattern string
	key     string
	want    bool
	wantErr bool
}{
	// exact
	{"exact match", "github.pull_request.opened", "github.pull_request.opened", true, false},
	{"exact mismatch last segment", "github.pull_request.opened", "github.pull_request.closed", false, false},
	{"exact mismatch first segment", "github.pull_request.opened", "gitlab.pull_request.opened", false, false},
	{"exact single segment match", "tick", "tick", true, false},
	{"exact single segment mismatch", "tick", "tock", false, false},

	// wildcard
	{"wildcard middle segment", "github.*.opened", "github.pull_request.opened", true, false},
	{"wildcard last segment", "github.pull_request.*", "github.pull_request.synchronize", true, false},
	{"wildcard every segment", "*.*.*", "github.pull_request.opened", true, false},
	{"wildcard never spans segments", "github.*", "github.pull_request.opened", false, false},
	{"wildcard single segment key", "*", "tick", true, false},
	{"wildcard matches empty key segment", "github.*.opened", "github..opened", true, false},

	// alternation
	{"alternation hit first alternative", "github.pull_request.{opened,synchronize,reopened,ready_for_review}", "github.pull_request.opened", true, false},
	{"alternation hit last alternative", "github.pull_request.{opened,synchronize,reopened,ready_for_review}", "github.pull_request.ready_for_review", true, false},
	{"alternation miss", "github.pull_request.{opened,synchronize}", "github.pull_request.closed", false, false},
	{"alternation single alternative", "github.{push}.main", "github.push.main", true, false},
	{"alternation is exact not prefix", "github.pull_request.{open}", "github.pull_request.opened", false, false},

	// mixed
	{"wildcard plus alternation", "*.pull_request.{opened,closed}", "github.pull_request.closed", true, false},
	{"mixed mismatch on segment count", "*.{opened,closed}", "github.pull_request.closed", false, false},

	// mismatched segment counts
	{"key longer than pattern", "github.pull_request", "github.pull_request.opened", false, false},
	{"key shorter than pattern", "github.pull_request.opened", "github.pull_request", false, false},

	// empty patterns / segments
	{"empty pattern", "", "github.pull_request.opened", false, true},
	{"empty middle pattern segment", "github..opened", "github.x.opened", false, true},
	{"trailing dot in pattern", "github.pull_request.", "github.pull_request.opened", false, true},

	// malformed alternation
	{"unclosed alternation brace", "github.pull_request.{opened", "github.pull_request.opened", false, true},
	{"unopened alternation brace", "github.pull_request.opened}", "github.pull_request.opened", false, true},
	{"empty alternation", "github.pull_request.{}", "github.pull_request.opened", false, true},
	{"empty alternative", "github.pull_request.{opened,}", "github.pull_request.opened", false, true},
	{"brace not whole segment", "github.pr{1,2}.opened", "github.pr1.opened", false, true},
	{"wildcard inside alternation", "github.{*,push}.opened", "github.push.opened", false, true},
	{"star inside literal segment", "github.pull*.opened", "github.pull_request.opened", false, true},

	// case sensitivity
	{"case sensitive literal", "github.Pull_Request.opened", "github.pull_request.opened", false, false},
	{"case sensitive alternation", "github.pull_request.{Opened}", "github.pull_request.opened", false, false},
}

func TestMatch(t *testing.T) {
	t.Parallel()
	for _, tc := range matchVectors {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Match(tc.pattern, tc.key)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Match(%q, %q): expected error, got nil", tc.pattern, tc.key)
				}
				if !errors.Is(err, ErrInvalidPattern) {
					t.Fatalf("Match(%q, %q): error %v does not wrap ErrInvalidPattern", tc.pattern, tc.key, err)
				}
				if got {
					t.Fatalf("Match(%q, %q): returned true alongside error", tc.pattern, tc.key)
				}
				return
			}
			if err != nil {
				t.Fatalf("Match(%q, %q): unexpected error: %v", tc.pattern, tc.key, err)
			}
			if got != tc.want {
				t.Fatalf("Match(%q, %q) = %v, want %v", tc.pattern, tc.key, got, tc.want)
			}
		})
	}
}

func TestValidatePattern(t *testing.T) {
	t.Parallel()
	for _, tc := range matchVectors {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidatePattern(tc.pattern)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidatePattern(%q): expected error, got nil", tc.pattern)
				}
				if !errors.Is(err, ErrInvalidPattern) {
					t.Fatalf("ValidatePattern(%q): error %v does not wrap ErrInvalidPattern", tc.pattern, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidatePattern(%q): unexpected error: %v", tc.pattern, err)
			}
		})
	}
}

func TestMatchAny(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		patterns []string
		key      string
		want     bool
	}{
		{"nil patterns", nil, "github.pull_request.opened", false},
		{"empty patterns", []string{}, "github.pull_request.opened", false},
		{"single hit", []string{"github.pull_request.*"}, "github.pull_request.opened", true},
		{"second pattern hits", []string{"gitlab.*.*", "github.*.{opened,closed}"}, "github.pull_request.opened", true},
		{"no pattern hits", []string{"gitlab.*.*", "github.issues.*"}, "github.pull_request.opened", false},
		{"invalid pattern skipped, later hit", []string{"github.{", "github.pull_request.opened"}, "github.pull_request.opened", true},
		{"only invalid patterns", []string{"github.{", "a..b"}, "github.pull_request.opened", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := MatchAny(tc.patterns, tc.key); got != tc.want {
				t.Fatalf("MatchAny(%v, %q) = %v, want %v", tc.patterns, tc.key, got, tc.want)
			}
		})
	}
}

// TestMatchRandomRouteKeys is the fuzz-lite obligation: random valid route
// keys must never panic, an exact pattern equal to the key must match, and
// MatchAny over a representative pattern set must not panic either.
func TestMatchRandomRouteKeys(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(0x10ce11))
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-:"
	patterns := []string{
		"github.pull_request.{opened,synchronize,reopened,ready_for_review}",
		"github.*.*",
		"*",
		"*.*",
		"*.*.*.*",
		"tick",
	}
	for i := 0; i < 2000; i++ {
		key := randomRouteKey(rng, alphabet)

		ok, err := Match(key, key)
		if err != nil {
			t.Fatalf("Match(%q, %q): valid exact pattern rejected: %v", key, key, err)
		}
		if !ok {
			t.Fatalf("Match(%q, %q): exact pattern did not match itself", key, key)
		}
		if err := ValidatePattern(key); err != nil {
			t.Fatalf("ValidatePattern(%q): unexpected error: %v", key, err)
		}
		_ = MatchAny(patterns, key)
	}
}

func randomRouteKey(rng *rand.Rand, alphabet string) string {
	segCount := 1 + rng.Intn(6)
	segs := make([]string, segCount)
	for i := range segs {
		segLen := 1 + rng.Intn(12)
		var b strings.Builder
		for j := 0; j < segLen; j++ {
			b.WriteByte(alphabet[rng.Intn(len(alphabet))])
		}
		segs[i] = b.String()
	}
	return strings.Join(segs, ".")
}
