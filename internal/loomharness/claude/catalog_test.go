package claude

import (
	"context"
	"slices"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

func effortIDs(m loomharness.Model) []string {
	if len(m.Options) == 0 {
		return nil
	}
	var ids []string
	for _, c := range m.Options[0].Choices {
		ids = append(ids, c.ID)
	}
	return ids
}

func byID(ms []loomharness.Model, id string) (loomharness.Model, bool) {
	i := slices.IndexFunc(ms, func(m loomharness.Model) bool { return m.ID == id })
	if i < 0 {
		return loomharness.Model{}, false
	}
	return ms[i], true
}

// TestClaudeModelsCatalog: the bundled snapshot keeps the opus, sonnet and
// haiku aliases first with their --effort option (high by default; haiku has
// none), then lists full model ids; none is the default, which the CLI picks
// per account.
func TestClaudeModelsCatalog(t *testing.T) {
	a, _, _ := newAdapter(t)
	ms, err := a.Models(context.Background())
	if err != nil || len(ms) <= 3 {
		t.Fatalf("Models = %+v, %v (want the snapshot, not the 3-entry fallback)", ms, err)
	}
	if ids := []string{ms[0].ID, ms[1].ID, ms[2].ID}; !slices.Equal(ids, []string{"opus", "sonnet", "haiku"}) {
		t.Fatalf("aliases = %v", ids)
	}
	for _, m := range ms {
		if m.Default || m.Provider != "anthropic" || len(m.Input) == 0 || m.ContextLimit == 0 || m.Name == "" {
			t.Fatalf("model = %+v", m)
		}
	}
	full := []string{"low", "medium", "high", "xhigh", "max"}
	if !slices.Equal(effortIDs(ms[0]), full) || ms[0].Options[0].ID != loomharness.OptionEffort || ms[0].Options[0].Current != "high" ||
		!slices.Equal(effortIDs(ms[1]), full) || len(ms[2].Options) != 0 {
		t.Fatalf("alias options = %+v", ms[:3])
	}
	if m, ok := byID(ms, "claude-opus-4-7"); !ok || !slices.Equal(effortIDs(m), full) || m.ContextLimit != 1_000_000 {
		t.Fatalf("claude-opus-4-7 = %+v, %v", m, ok)
	}
}

// TestClaudeCatalogFromSnapshot: entries come from the snapshot (names,
// context, input, effort levels), newest first after the aliases, each alias
// takes its family's newest model, deprecated models drop out, and a level
// list without high defaults to its first level.
func TestClaudeCatalogFromSnapshot(t *testing.T) {
	ms := catalog([]byte(`{
	  "claude-opus-9": {"name":"Claude Opus 9","family":"claude-opus","release_date":"2030-01-02","context":2000000,"input":["text"],"effort":["low","high"]},
	  "claude-opus-8": {"name":"Claude Opus 8","family":"claude-opus","release_date":"2029-01-01","context":500000,"input":["text"]},
	  "claude-sonnet-9": {"name":"Claude Sonnet 9","family":"claude-sonnet","release_date":"2030-01-01","context":300000,"input":["text","image"],"effort":["low","medium"]},
	  "claude-haiku-9": {"name":"Claude Haiku 9","family":"claude-haiku","release_date":"2029-06-01","context":100000,"input":["text"]},
	  "claude-haiku-1": {"family":"claude-haiku","release_date":"2031-01-01","status":"deprecated","context":1}
	}`))
	var ids []string
	for _, m := range ms {
		ids = append(ids, m.ID)
	}
	if !slices.Equal(ids, []string{"opus", "sonnet", "haiku", "claude-opus-9", "claude-sonnet-9", "claude-haiku-9", "claude-opus-8"}) {
		t.Fatalf("ids = %v", ids)
	}
	if ms[0].ContextLimit != 2_000_000 || ms[0].Name != "Opus" || !slices.Equal(effortIDs(ms[0]), []string{"low", "high"}) || ms[0].Options[0].Current != "high" {
		t.Fatalf("opus = %+v", ms[0])
	}
	if ms[1].Options[0].Current != "low" || ms[1].Options[0].Choices[1].Label != "Medium" || !slices.Equal(ms[1].Input, []string{"text", "image"}) {
		t.Fatalf("sonnet = %+v", ms[1])
	}
	if ms[2].ContextLimit != 100_000 || len(ms[2].Options) != 0 || ms[3].Name != "Claude Opus 9" || len(ms[6].Options) != 0 {
		t.Fatalf("catalog = %+v", ms)
	}
}

// TestClaudeCatalogFallback: an unreadable snapshot, or one missing an alias's
// family, gives the hand-written list so the picker never goes empty.
func TestClaudeCatalogFallback(t *testing.T) {
	for name, data := range map[string]string{
		"garbage":   `not json`,
		"empty":     `{}`,
		"no haiku":  `{"claude-opus-9":{"family":"claude-opus","context":1},"claude-sonnet-9":{"family":"claude-sonnet","context":1}}`,
		"wrong top": `[1,2]`,
	} {
		ms := catalog([]byte(data))
		if len(ms) != 3 || ms[0].ID != "opus" || ms[1].ID != "sonnet" || ms[2].ID != "haiku" ||
			!slices.Equal(effortIDs(ms[0]), []string{"low", "medium", "high", "xhigh", "max"}) || ms[0].Options[0].Current != "high" ||
			len(ms[2].Options) != 0 || ms[0].ContextLimit != 1_000_000 {
			t.Fatalf("%s: catalog = %+v", name, ms)
		}
	}
}
