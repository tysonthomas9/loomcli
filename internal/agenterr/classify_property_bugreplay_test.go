//go:build daemon_bugreplay

package agenterr

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/olesho/harness-wrapper/pkg/wrapper"
)

// P4/#764: incidental words in ordinary output cannot justify a fatal auth
// or billing stop. The generated prefix/suffix prevents a single canned line
// from becoming the entire oracle; the minimal examples are the words below.
func TestPropertyFatalClassificationNeedsExplicitSignal(t *testing.T) {
	seed := int64(764)
	if value := os.Getenv("DAEMON_PROPERTY_SEED"); value != "" {
		var err error
		seed, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			t.Fatalf("DAEMON_PROPERTY_SEED: %v", err)
		}
	}
	t.Logf("seed=%d", seed)
	rng := rand.New(rand.NewSource(seed))
	const iterations = 200
	contexts := []string{"reading docs", "editing a comment", "summarizing a task", "writing a test"}
	words := []string{"billing", "quota", "credits", "ANTHROPIC_API_KEY"}
	seen := make(map[string]struct{})
	executed := 0
	defer func() { t.Logf("iterations=%d depth=1 distinct_classes=%d", executed, len(seen)) }()
	for iteration := 0; iteration < iterations; iteration++ {
		executed++
		word := words[rng.Intn(len(words))]
		output := fmt.Sprintf("%s: the word %s appears in source text; %s", contexts[rng.Intn(len(contexts))], word, contexts[rng.Intn(len(contexts))])
		got := ClassifyFromOutput(output, 1, "claude")
		seen[got.Class.String()] = struct{}{}
		if got.Class.Harness == wrapper.ErrAuth || got.Class.Harness == wrapper.ErrBilling {
			minimal := ClassifyFromOutput(word, 1, "claude")
			t.Fatalf("P4/#764 seed=%d iteration=%d word=%q output=%q: incidental prose classified %s; minimal %q -> %s", seed, iteration, word, output, got.Class, word, minimal.Class)
		}
	}
	// P3: a typed harness marker must still win over ambiguous surrounding
	// prose. This checks the positive half of the same precedence rule.
	for _, marker := range []struct {
		text string
		want wrapper.ErrorClass
	}{
		{AuthRequiredMarker, wrapper.ErrAuth},
		{UsageLimitedMarker, wrapper.ErrRateLimited},
	} {
		got := ClassifyFromOutput(strings.Join([]string{"ordinary billing discussion", marker.text, "quota mentioned in a file"}, "\n"), 1, "claude")
		if got.Class.Harness != marker.want {
			t.Fatalf("P3 marker=%q: got %s, want %s", marker.text, got.Class, marker.want)
		}
	}
}
