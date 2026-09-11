package realtime

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestCatchUpRejectsNonAdvancingPages(t *testing.T) {
	cases := map[string][]backend.MutationPage{
		"missing":        {{HasMore: true}},
		"stalled":        {{Cursor: "c1.start", HasMore: true}},
		"cycle":          {{Cursor: "c1.next", HasMore: true}, {Cursor: "c1.start", HasMore: true}},
		"terminal cycle": {{Cursor: "c1.next", HasMore: true}, {Cursor: "c1.start"}},
	}
	for name, pages := range cases {
		t.Run(name, func(t *testing.T) {
			calls := 0
			h := NewHandler(HandlerConfig{GetMutationPage: func(context.Context, string, string, int) (backend.MutationPage, error) {
				if calls >= len(pages) {
					t.Fatal("retried known invalid page sequence")
				}
				p := pages[calls]
				calls++
				return p, nil
			}})
			_, _, resync, err := h.fetchCatchUp(context.Background(), "c1.start", "WS", nil)
			if err == nil || resync == nil || resync.reason != "error" || resync.cursor != "c1.start" {
				t.Fatalf("resync=%+v err=%v", resync, err)
			}
		})
	}
}

func TestCatchUpCheckpointCoversFilteredTail(t *testing.T) {
	for _, allFiltered := range []bool{false, true} {
		t.Run(map[bool]string{false: "mixed", true: "all filtered"}[allFiltered], func(t *testing.T) {
			firstRepo := "wanted"
			if allFiltered {
				firstRepo = "other"
			}
			calls := 0
			h := NewHandler(HandlerConfig{GetMutationPage: func(context.Context, string, string, int) (backend.MutationPage, error) {
				calls++
				if calls == 1 {
					return backend.MutationPage{Events: []backend.MutationData{{Cursor: "c1.visible", Type: "update", SourceRepo: firstRepo}}, Cursor: "c1.visible", HasMore: true}, nil
				}
				return backend.MutationPage{Events: []backend.MutationData{{Cursor: "c1.tail", Type: "update", SourceRepo: "other"}}, Cursor: "c1.tail"}, nil
			}})
			prepared, _, resync, err := h.fetchCatchUp(context.Background(), "c1.start", "WS", []string{"wanted"})
			if err != nil || resync != nil {
				t.Fatalf("resync=%+v err=%v", resync, err)
			}
			want := 2
			if allFiltered {
				want = 1
			}
			if len(prepared) != want {
				t.Fatalf("prepared=%+v", prepared)
			}
			last := prepared[len(prepared)-1]
			if !last.checkpoint || last.id != "c1.tail" {
				t.Fatalf("last=%+v", last)
			}
			writer := newRecordingFrameWriter()
			if err := writePreparedMutation(writer, last); err != nil {
				t.Fatal(err)
			}
			frame := writer.snapshot()[0]
			if frame.event != "checkpoint" || frame.id != "c1.tail" {
				t.Fatalf("frame=%+v", frame)
			}
		})
	}
}

func TestCatchUpIdlePageDoesNotInventCheckpoint(t *testing.T) {
	h := NewHandler(HandlerConfig{GetMutationPage: func(context.Context, string, string, int) (backend.MutationPage, error) {
		return backend.MutationPage{Cursor: "c1.start"}, nil
	}})
	prepared, _, resync, err := h.fetchCatchUp(context.Background(), "c1.start", "WS", nil)
	if err != nil || resync != nil || len(prepared) != 0 {
		t.Fatalf("prepared=%+v resync=%+v err=%v", prepared, resync, err)
	}
}
