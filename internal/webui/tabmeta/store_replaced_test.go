package tabmeta

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestSetAndGet_RoundTripsReplacementMarker(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	replaced := now.Add(-2 * time.Hour)
	if err := store.Set(ctx, &TabMetadata{
		SessionName:    "replaced-session",
		Workspace:      testWorkspace,
		Label:          "Replaced",
		CreatedAt:      now,
		UpdatedAt:      now,
		ReplacedAt:     replaced,
		ReplacedReason: "server_restart",
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := store.Get(ctx, testWorkspace, "replaced-session")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.ReplacedAt.Equal(replaced) {
		t.Errorf("ReplacedAt = %v, want %v", got.ReplacedAt, replaced)
	}
	if got.ReplacedReason != "server_restart" {
		t.Errorf("ReplacedReason = %q, want %q", got.ReplacedReason, "server_restart")
	}
}

// A tab name can be reclaimed by a fresh PutTab. Set must clear the previous
// tab's marker rather than let the new tab inherit it — HSet only overwrites
// the keys it is handed.
func TestSet_ZeroReplacedAtClearsStoredMarker(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	base := TabMetadata{
		SessionName: "reclaimed",
		Workspace:   testWorkspace,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	marked := base
	marked.ReplacedAt = now.Add(-time.Hour)
	marked.ReplacedReason = "server_restart"
	if err := store.Set(ctx, &marked); err != nil {
		t.Fatalf("Set marked: %v", err)
	}
	if err := store.Set(ctx, &base); err != nil {
		t.Fatalf("Set fresh: %v", err)
	}

	got, err := store.Get(ctx, testWorkspace, "reclaimed")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.ReplacedAt.IsZero() {
		t.Errorf("ReplacedAt = %v, want zero", got.ReplacedAt)
	}
	if got.ReplacedReason != "" {
		t.Errorf("ReplacedReason = %q, want empty", got.ReplacedReason)
	}
}

// Legacy hashes have no replaced_at key at all, and a corrupt value is not
// worth failing a whole tab list over: both mean "never replaced".
func TestParseMetadata_TolerantReplacedAt(t *testing.T) {
	tests := []struct {
		name string
		vals map[string]string
	}{
		{name: "missing key", vals: map[string]string{"label": "l"}},
		{name: "empty value", vals: map[string]string{"replaced_at": ""}},
		{name: "garbage value", vals: map[string]string{"replaced_at": "not-a-time"}},
		{
			name: "reason without timestamp",
			vals: map[string]string{"replaced_at": "", "replaced_reason": "server_restart"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta, err := parseMetadata(testWorkspace, "s1", tt.vals)
			if err != nil {
				t.Fatalf("parseMetadata: %v", err)
			}
			if !meta.ReplacedAt.IsZero() {
				t.Errorf("ReplacedAt = %v, want zero", meta.ReplacedAt)
			}
			if meta.ReplacedReason != "" {
				t.Errorf("ReplacedReason = %q, want empty", meta.ReplacedReason)
			}
		})
	}
}

// omitempty does nothing for a time.Time, so a zero marker would otherwise
// serialize as "0001-01-01T00:00:00Z" and light up every tab.
func TestTabMetadata_MarshalJSON_OmitsZeroReplacedAt(t *testing.T) {
	raw, err := json.Marshal(&TabMetadata{SessionName: "s1"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := decoded["replaced_at"]; ok {
		t.Errorf("replaced_at present in %s, want omitted", raw)
	}

	stamp := time.Date(2026, 8, 14, 16, 52, 3, 0, time.UTC)
	raw, err = json.Marshal(&TabMetadata{SessionName: "s1", ReplacedAt: stamp})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded["replaced_at"] != "2026-08-14T16:52:03Z" {
		t.Errorf("replaced_at = %v, want 2026-08-14T16:52:03Z", decoded["replaced_at"])
	}
}
