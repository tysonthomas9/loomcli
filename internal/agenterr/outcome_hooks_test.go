package agenterr

import "testing"

// The wire name is byte-stable: it lands in daemon-agents.json last_error_class,
// events, and checkpoints, so a rename is a breaking change.
func TestCompletionHookFailureOutcome_String(t *testing.T) {
	if got := CompletionHookFailureOutcome.String(); got != "CompletionHookFailure" {
		t.Errorf("CompletionHookFailureOutcome.String() = %q, want %q", got, "CompletionHookFailure")
	}
	o := OutcomeFromDomain(CompletionHookFailureOutcome)
	if got := o.String(); got != "CompletionHookFailure" {
		t.Errorf("Outcome.String() = %q, want %q", got, "CompletionHookFailure")
	}
	if !o.IsDomain() {
		t.Error("CompletionHookFailure should be a domain outcome")
	}
	if o.IsHarness() {
		t.Error("CompletionHookFailure is not a harness class")
	}
	if !o.Is(CompletionHookFailureOutcome) {
		t.Error("Is() should match its own outcome")
	}
	// It must be distinct from every other domain outcome.
	for _, other := range []DomainOutcome{
		DomainNone, NoWorkOutcome, LockConflictOutcome, SpawnFailureOutcome, BackendUnavailableOutcome,
	} {
		if other.String() == "CompletionHookFailure" {
			t.Errorf("%v collides with CompletionHookFailure", other)
		}
		if o.Is(other) {
			t.Errorf("CompletionHookFailure should not match %v", other)
		}
	}
}
