package types

import (
	"strings"
	"testing"
)

// ValidateSettableStatus is loom's copy of fleet-db's models.ValidateSettableStatus
// — the PATCH /issues/{id} status contract. Both repos validate independently, so
// the two lists can only stay in step if each pins the same verdicts AND the same
// messages: a value refused here has to be refused there in the words a client
// already acts on.
func TestValidateSettableStatus(t *testing.T) {
	tests := []struct {
		name    string
		status  Status
		wantErr string
	}{
		{name: "open", status: StatusOpen},
		{name: "blocked", status: StatusBlocked},
		{name: "deferred", status: StatusDeferred},
		{name: "review", status: StatusReview},

		// The two that have endpoints of their own. The message is the whole
		// point of refusing them: it tells the caller where the write lives.
		{name: "closed", status: StatusClosed, wantErr: "status closed must use close endpoint"},
		{name: "in_progress", status: StatusInProgress, wantErr: "status in_progress must use claim endpoint"},

		{name: "tombstone", status: StatusTombstone, wantErr: "status tombstone is system-managed"},
		{name: "pinned", status: StatusPinned, wantErr: "status pinned is system-managed"},
		{name: "hooked", status: StatusHooked, wantErr: "status hooked is system-managed"},

		{name: "unknown", status: Status("waiting"), wantErr: `invalid status "waiting"`},
		// A custom workspace status is refused too, matching the endpoint, which
		// validates a status update against the built-in set only.
		{name: "custom workspace status", status: Status("triaged"), wantErr: `invalid status "triaged"`},
		{name: "wrong case", status: Status("Open"), wantErr: `invalid status "Open"`},
		// Untrimmed: no caller may rely on this repairing a typo the server
		// would still reject.
		{name: "leading space", status: Status(" open"), wantErr: `invalid status " open"`},
		{name: "empty", status: Status(""), wantErr: `invalid status ""`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSettableStatus(tt.status)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateSettableStatus(%q) = %v, want nil", tt.status, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateSettableStatus(%q) = nil, want %q", tt.status, tt.wantErr)
			}
			if err.Error() != tt.wantErr {
				t.Errorf("ValidateSettableStatus(%q) = %q, want exactly %q (fleet-db's wording)",
					tt.status, err.Error(), tt.wantErr)
			}
		})
	}
}

// Every built-in status must get a verdict. Without this, adding a status to the
// vocabulary and forgetting the switch would fall through to the "unreachable"
// return and quietly become settable.
func TestValidateSettableStatus_CoversEveryBuiltinStatus(t *testing.T) {
	settable := map[Status]bool{
		StatusOpen: true, StatusBlocked: true, StatusDeferred: true, StatusReview: true,
	}
	for _, s := range BuiltinStatuses() {
		err := ValidateSettableStatus(s)
		if settable[s] {
			if err != nil {
				t.Errorf("status %q must be settable, got %v", s, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("status %q must be refused, got nil — a new status defaults to settable "+
				"only because the switch forgot it", s)
			continue
		}
		// The refusal has to say something actionable, not just "no".
		if !strings.Contains(err.Error(), string(s)) {
			t.Errorf("status %q: error %q does not name the status", s, err)
		}
	}
}

// BuiltinStatuses is what lets the parity checks walk the whole vocabulary
// instead of re-listing it, so it has to actually be whole.
func TestBuiltinStatuses(t *testing.T) {
	got := BuiltinStatuses()
	if len(got) != 9 {
		t.Fatalf("BuiltinStatuses() = %v, want all 9 built-in statuses", got)
	}
	for _, s := range got {
		if !s.IsValid() {
			t.Errorf("BuiltinStatuses() lists %q, which IsValid rejects", s)
		}
	}
	// Returned by value: a caller must not be able to edit the canonical list.
	got[0] = Status("mutated")
	if BuiltinStatuses()[0] != StatusOpen {
		t.Error("BuiltinStatuses() shares its backing array with the package-level list")
	}
}
