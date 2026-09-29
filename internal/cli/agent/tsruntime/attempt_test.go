package tsruntime

import (
	"strings"
	"testing"
)

func TestFallbackTaskRunIDIsUniquePerLeafAttempt(t *testing.T) {
	first := fallbackTaskRunID("coder")
	second := fallbackTaskRunID("coder")
	if first == second || !strings.HasPrefix(first, "tr-coder-") || !strings.HasPrefix(second, "tr-coder-") {
		t.Fatalf("fallback IDs = %q, %q", first, second)
	}
}
