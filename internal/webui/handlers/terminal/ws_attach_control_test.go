package terminal

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

func TestAttachControlFrame(t *testing.T) {
	stamp := time.Date(2026, 8, 14, 16, 52, 3, 0, time.UTC)

	tests := []struct {
		name       string
		reattached bool
		meta       *tabmeta.TabMetadata
		want       attachControlMessage
	}{
		{
			name: "no metadata still announces the attach",
			want: attachControlMessage{Type: "attach"},
		},
		{
			name: "unmarked tab reports no replacement",
			meta: &tabmeta.TabMetadata{SessionName: "s1"},
			want: attachControlMessage{Type: "attach"},
		},
		{
			name: "this attach is the replacement",
			meta: &tabmeta.TabMetadata{ReplacedAt: stamp, ReplacedReason: "server_restart"},
			want: attachControlMessage{
				Type: "attach", Replaced: true,
				ReplacedAt: "2026-08-14T16:52:03Z", ReplacedReason: "server_restart",
			},
		},
		{
			name:       "reattach carries the stored marker but is not the replacement",
			reattached: true,
			meta:       &tabmeta.TabMetadata{ReplacedAt: stamp, ReplacedReason: "server_restart"},
			want: attachControlMessage{
				Type: "attach", Reattached: true,
				ReplacedAt: "2026-08-14T16:52:03Z", ReplacedReason: "server_restart",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := attachControlFrame(tt.reattached, tt.meta)
			if err != nil {
				t.Fatalf("attachControlFrame: %v", err)
			}
			var got attachControlMessage
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("decode %s: %v", raw, err)
			}
			if got != tt.want {
				t.Errorf("frame = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestMarkSessionReplaced(t *testing.T) {
	startedAt := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 14, 16, 52, 3, 0, time.UTC)

	tests := []struct {
		name      string
		noStore   bool
		meta      *tabmeta.TabMetadata
		startedAt time.Time
		want      bool
	}{
		{
			name:      "tab predating this server process was replaced",
			meta:      &tabmeta.TabMetadata{CreatedAt: startedAt.Add(-time.Hour)},
			startedAt: startedAt,
			want:      true,
		},
		{
			name:      "tab created in this process is merely new",
			meta:      &tabmeta.TabMetadata{CreatedAt: startedAt.Add(time.Minute)},
			startedAt: startedAt,
			want:      false,
		},
		{
			name:      "zero server start time is undecidable",
			meta:      &tabmeta.TabMetadata{CreatedAt: startedAt.Add(-time.Hour)},
			startedAt: time.Time{},
			want:      false,
		},
		{
			name:      "zero tab creation time is undecidable",
			meta:      &tabmeta.TabMetadata{},
			startedAt: startedAt,
			want:      false,
		},
		{
			name:      "nil metadata",
			startedAt: startedAt,
			want:      false,
		},
		{
			name:      "nil store skips the marker entirely",
			noStore:   true,
			meta:      &tabmeta.TabMetadata{CreatedAt: startedAt.Add(-time.Hour)},
			startedAt: startedAt,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			var store *tabmeta.Store
			if !tt.noStore {
				store = newTabMetaStoreForWSTest(t)
			}

			if tt.want && store != nil {
				seed := *tt.meta
				seed.SessionName = "term_1"
				seed.Workspace = "E2E"
				if err := store.Set(ctx, &seed); err != nil {
					t.Fatalf("seed Set: %v", err)
				}
			}

			marked, err := markSessionReplaced(ctx, store, "E2E", "term_1", tt.meta, tt.startedAt, now)
			if err != nil {
				t.Fatalf("markSessionReplaced: %v", err)
			}
			if marked != tt.want {
				t.Fatalf("marked = %v, want %v", marked, tt.want)
			}
			if !tt.want {
				if tt.meta != nil && !tt.meta.ReplacedAt.IsZero() {
					t.Errorf("meta.ReplacedAt = %v, want untouched", tt.meta.ReplacedAt)
				}
				return
			}

			if !tt.meta.ReplacedAt.Equal(now) {
				t.Errorf("meta.ReplacedAt = %v, want %v", tt.meta.ReplacedAt, now)
			}
			if tt.meta.ReplacedReason != replacedReasonServerRestart {
				t.Errorf("meta.ReplacedReason = %q, want %q", tt.meta.ReplacedReason, replacedReasonServerRestart)
			}

			stored, err := store.Get(ctx, "E2E", "term_1")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if !stored.ReplacedAt.Equal(now) {
				t.Errorf("stored ReplacedAt = %v, want %v", stored.ReplacedAt, now)
			}
			if stored.ReplacedReason != replacedReasonServerRestart {
				t.Errorf("stored ReplacedReason = %q, want %q", stored.ReplacedReason, replacedReasonServerRestart)
			}
		})
	}
}

// A missing hash makes Patch fail. The caller treats that as non-fatal, but
// the marker must still be reported so the frame can carry it.
func TestMarkSessionReplaced_ReportsMarkedOnStoreFailure(t *testing.T) {
	meta := &tabmeta.TabMetadata{CreatedAt: time.Date(2026, 8, 14, 11, 0, 0, 0, time.UTC)}
	marked, err := markSessionReplaced(
		context.Background(), newTabMetaStoreForWSTest(t), "E2E", "term_missing", meta,
		time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC), time.Now(),
	)
	if err == nil {
		t.Fatal("expected a store error for an absent session")
	}
	if !marked {
		t.Error("marked = false, want true — the frame must still report the replacement")
	}
}

// A replacement spawns a shell owned by THIS server process, so created_at
// has to move forward with it. Left at its pre-restart value, the staleness
// test in markSessionReplaced keeps matching: every later attach announces
// another replacement, the client draws another restart boundary, and
// ptyAttachable keeps reporting the tab dead while its PTY is live.
func TestMarkSessionReplaced_AdvancesCreatedAtSoTheNextAttachIsNotStale(t *testing.T) {
	ctx := context.Background()
	store := newTabMetaStoreForWSTest(t)
	startedAt := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 14, 13, 0, 0, 0, time.UTC)

	meta := &tabmeta.TabMetadata{
		Workspace:   "E2E",
		SessionName: "term_1",
		CreatedAt:   startedAt.Add(-time.Hour),
	}
	if err := store.Set(ctx, meta); err != nil {
		t.Fatalf("seed Set: %v", err)
	}

	marked, err := markSessionReplaced(ctx, store, "E2E", "term_1", meta, startedAt, now)
	if err != nil {
		t.Fatalf("markSessionReplaced: %v", err)
	}
	if !marked {
		t.Fatal("marked = false, want true for a pre-restart session")
	}
	if !meta.CreatedAt.Equal(meta.ReplacedAt) {
		t.Errorf("meta.CreatedAt = %v, want it advanced to ReplacedAt %v", meta.CreatedAt, meta.ReplacedAt)
	}

	stored, err := store.Get(ctx, "E2E", "term_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.CreatedAt.Before(startedAt) {
		t.Fatalf("stored CreatedAt = %v, want at or after server start %v", stored.CreatedAt, startedAt)
	}

	// The second attach of the same session must no longer look stale.
	againMarked, err := markSessionReplaced(ctx, store, "E2E", "term_1", stored, startedAt, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("second markSessionReplaced: %v", err)
	}
	if againMarked {
		t.Error("second attach was marked replaced again; created_at did not stick")
	}
}
