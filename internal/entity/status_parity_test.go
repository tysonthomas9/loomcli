package entity

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/types"
)

// The nine statuses are named twice — entity.IssueStatus for the domain model,
// types.Status for the fleet wire path — and these tests are what keeps two names
// from becoming two vocabularies again. IssueStatus is declared in terms of
// types.Status, so the pairing below is mechanical today; the point is the day
// someone adds a status to one side by hand. Then this fails at build time instead
// of the mismatch surfacing as a status the server rejects at runtime.

// statusPairs names every built-in status on both sides. Paired by constant, not
// by position, so reordering either list is not mistaken for agreement.
var statusPairs = []struct {
	entity IssueStatus
	canon  types.Status
}{
	{StatusOpen, types.StatusOpen},
	{StatusInProgress, types.StatusInProgress},
	{StatusBlocked, types.StatusBlocked},
	{StatusDeferred, types.StatusDeferred},
	{StatusReview, types.StatusReview},
	{StatusClosed, types.StatusClosed},
	{StatusTombstone, types.StatusTombstone},
	{StatusPinned, types.StatusPinned},
	{StatusHooked, types.StatusHooked},
}

func TestStatusVocabulariesAgree(t *testing.T) {
	canonical := types.BuiltinStatuses()

	// Cardinality first: this is the arm that fires when a status is added to one
	// side only, whichever side that is.
	if len(canonical) != len(builtinStatuses) {
		t.Fatalf("vocabulary sizes differ: types has %d built-in statuses %v, entity has %d %v — "+
			"a status was added to one side and not the other",
			len(canonical), canonical, len(builtinStatuses), builtinStatuses)
	}
	if len(statusPairs) != len(canonical) {
		t.Fatalf("statusPairs lists %d statuses, the vocabulary has %d — "+
			"a new status needs a row here too", len(statusPairs), len(canonical))
	}

	for _, p := range statusPairs {
		if string(p.entity) != string(p.canon) {
			t.Errorf("paired constants disagree: entity %q vs types %q", p.entity, p.canon)
		}
	}

	// Set equality in both directions, so neither list can carry a value the other
	// has never heard of even when the counts happen to match.
	inEntity := make(map[string]bool, len(builtinStatuses))
	for _, s := range builtinStatuses {
		inEntity[string(s)] = true
	}
	for _, s := range canonical {
		if !inEntity[string(s)] {
			t.Errorf("types built-in %q is missing from entity's vocabulary", s)
		}
	}
	inCanonical := make(map[string]bool, len(canonical))
	for _, s := range canonical {
		inCanonical[string(s)] = true
	}
	for _, s := range builtinStatuses {
		if !inCanonical[string(s)] {
			t.Errorf("entity built-in %q is missing from types' vocabulary", s)
		}
	}
}

// Agreement has to hold for the verdicts too, not just the spelling: a status both
// lists name is worthless if only one of them will validate it.
func TestStatusVocabulariesAgreeOnEveryRealValue(t *testing.T) {
	for _, p := range statusPairs {
		t.Run(string(p.canon), func(t *testing.T) {
			if !p.entity.IsValid() {
				t.Errorf("entity.IssueStatus(%q).IsValid() = false, want true", p.entity)
			}
			if !p.canon.IsValid() {
				t.Errorf("types.Status(%q).IsValid() = false, want true", p.canon)
			}
		})
	}
}

// The empty string is the one value the two are allowed to disagree on, and both
// halves are pinned here so neither can be "aligned" with the other by someone
// tidying up. entity accepts it because a domain record is routinely validated
// before SetDefaults has filled the status in; types rejects it because it guards
// the wire path and the whitelists built on it — types.ValidateSettableStatus most
// of all, which would otherwise pass "" through its validity guard, match no case,
// and report an unstated status as settable.
func TestStatusEmptyIsTheOnlyDivergence(t *testing.T) {
	if !IssueStatus("").IsValid() {
		t.Error(`entity.IssueStatus("").IsValid() = false, want true — ` +
			"callers rely on deferring the default to SetDefaults")
	}
	if types.Status("").IsValid() {
		t.Error(`types.Status("").IsValid() = true, want false — ` +
			"an empty status must not satisfy a wire-path or whitelist guard")
	}

	// Everything that is not the empty string gets the same verdict from both.
	others := []string{"unknown", "OPEN", "deleted", " open", "waiting_qa"}
	for _, p := range statusPairs {
		others = append(others, string(p.canon))
	}
	for _, v := range others {
		if got, want := IssueStatus(v).IsValid(), types.Status(v).IsValid(); got != want {
			t.Errorf("IsValid(%q): entity = %v, types = %v — only %q may differ", v, got, want, "")
		}
	}
}
