package realtime

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func fenceSession(h *Handler, writer frameWriter) *authoritativeSession {
	return &authoritativeSession{handler: h, writer: writer, ctx: context.Background(), client: &Client{workspaceID: "ws", sourceRepos: []string{"wanted"}}}
}

func TestFixedReplayFenceGrowingTailEndsCapturedPass(t *testing.T) {
	headCalls, pageCalls := 0, 0
	head := "c2.b3BhcXVlLWE"
	h := NewHandler(HandlerConfig{Hub: NewHub(), OpenMutationSource: openFixtureMutationSource(func(_ context.Context, ws, since string, limit int) (backend.MutationPage, error) {
		headCalls++
		if ws != "ws" || since != "$" || limit != 1 {
			t.Fatalf("head arguments: %s %s %d", ws, since, limit)
		}
		return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: head}, nil
	}, func(_ context.Context, _ string, since, through string, _ int) (backend.MutationPage, error) {
		pageCalls++
		head = "c2.bmV3ZXItdGFpbA" // The source keeps growing while the captured pass runs.
		switch since {
		case "c2.c3RhcnQ":
			if through != "c2.b3BhcXVlLWE" {
				t.Fatalf("changed fence %q", through)
			}
			return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: "c2.b3BhcXVlLXo", HasMore: true, Events: []backend.MutationData{readerMutation("c2.b3BhcXVlLXo", "wanted")}}, nil
		case "c2.b3BhcXVlLXo":
			if through != "c2.b3BhcXVlLWE" {
				t.Fatalf("changed fence %q", through)
			}
			// A backend-filtered tail still carries the exact raw fence checkpoint.
			return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: through}, nil
		case "c2.b3BhcXVlLWE":
			if through != "c2.bmV3ZXItdGFpbA" {
				t.Fatalf("next pass fence %q", through)
			}
			return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: through, Events: []backend.MutationData{readerMutation(through, "wanted")}}, nil
		default:
			t.Fatalf("unexpected cursor %q", since)
			return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ"}, nil
		}
	})})
	writer := &readerTestWriter{}
	s := fenceSession(h, writer)
	if err := s.initialize("c2.c3RhcnQ"); err != nil {
		t.Fatal(err)
	}
	if err := s.catchUp(nil); err != nil {
		t.Fatal(err)
	}
	if headCalls != 1 || pageCalls != 2 || s.reader.cursor != "c2.b3BhcXVlLWE" {
		t.Fatalf("head=%d pages=%d cursor=%s", headCalls, pageCalls, s.reader.cursor)
	}
	if !reflect.DeepEqual(writer.frames, []readerFrame{{"c2.b3BhcXVlLXo", "mutation"}, {"c2.b3BhcXVlLWE", "checkpoint"}}) {
		t.Fatalf("frames %v", writer.frames)
	}
	if err := s.catchUp(nil); err != nil {
		t.Fatal(err)
	}
	if headCalls != 2 || s.reader.cursor != "c2.bmV3ZXItdGFpbA" {
		t.Fatalf("next pass head=%d cursor=%s", headCalls, s.reader.cursor)
	}
}

func TestFixedReplayFenceRejectsTerminalDisagreementBeforeWrites(t *testing.T) {
	for _, page := range []backend.MutationPage{
		{Cursor: "c2.ZmVuY2U", HasMore: true, Events: []backend.MutationData{readerMutation("c2.ZmVuY2U", "wanted")}},
		{Cursor: "c2.bWlkZGxl", Events: []backend.MutationData{readerMutation("c2.bWlkZGxl", "wanted")}},
	} {
		t.Run(page.Cursor, func(t *testing.T) {
			page.SourceIdentity = "s1.Zml4dHVyZQ"
			writer := &readerTestWriter{}
			h := NewHandler(HandlerConfig{Hub: NewHub(), OpenMutationSource: openFixtureMutationSource(func(context.Context, string, string, int) (backend.MutationPage, error) {
				return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: "c2.ZmVuY2U"}, nil
			}, func(context.Context, string, string, string, int) (backend.MutationPage, error) { return page, nil })})
			s := fenceSession(h, writer)
			if err := s.initialize("c2.c3RhcnQ"); err != nil {
				t.Fatal(err)
			}
			if err := s.catchUp(nil); err == nil {
				t.Fatal("accepted invalid terminal page")
			}
			if len(writer.frames) != 0 || s.reader.cursor != "c2.c3RhcnQ" {
				t.Fatalf("advanced: %v %s", writer.frames, s.reader.cursor)
			}
		})
	}
}

func TestFixedReplayFenceExpiryDoesNotWriteFreshHeadOrResetResume(t *testing.T) {
	for _, start := range []string{"", "c2.YWNjZXB0ZWQ"} {
		t.Run("start="+start, func(t *testing.T) {
			writer := &readerTestWriter{}
			h := NewHandler(HandlerConfig{Hub: NewHub(), OpenMutationSource: openFixtureMutationSource(func(context.Context, string, string, int) (backend.MutationPage, error) {
				return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: "c2.aGVhZA"}, nil
			}, func(context.Context, string, string, string, int) (backend.MutationPage, error) {
				return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ"}, backend.ErrMutationCursorExpired
			})})
			s := fenceSession(h, writer)
			if err := s.initialize(start); err != nil {
				t.Fatal(err)
			}
			if err := s.catchUp(nil); !errors.Is(err, backend.ErrMutationCursorExpired) {
				t.Fatalf("error %v", err)
			}
			if len(writer.frames) != 0 {
				t.Fatalf("wrote unvalidated head %v", writer.frames)
			}
			if start != "" && s.reader.cursor != start {
				t.Fatalf("reset cursor %s", s.reader.cursor)
			}
		})
	}
}

func TestFixedReplayFenceWriteFailureResumesWithinSameFence(t *testing.T) {
	heads := 0
	writeErr := errors.New("write failed")
	h := NewHandler(HandlerConfig{Hub: NewHub(), OpenMutationSource: openFixtureMutationSource(func(context.Context, string, string, int) (backend.MutationPage, error) {
		heads++
		return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: "c2.dGFpbA"}, nil
	}, func(_ context.Context, _ string, since, through string, _ int) (backend.MutationPage, error) {
		if through != "c2.dGFpbA" {
			t.Fatalf("fence %s", through)
		}
		events := []backend.MutationData{readerMutation("c2.Zmlyc3Q", "wanted"), readerMutation("c2.dGFpbA", "wanted")}
		if since == "c2.Zmlyc3Q" {
			events = events[1:]
		} else if since != "c2.c3RhcnQ" {
			t.Fatalf("since %s", since)
		}
		return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: through, Events: events}, nil
	})})
	writer := &readerTestWriter{failAt: 2, writeError: writeErr}
	s := fenceSession(h, writer)
	if err := s.initialize("c2.c3RhcnQ"); err != nil {
		t.Fatal(err)
	}
	if err := s.catchUp(nil); !errors.Is(err, writeErr) || !isAuthoritativeWriteError(err) {
		t.Fatalf("error %v", err)
	}
	if s.reader.cursor != "c2.Zmlyc3Q" {
		t.Fatalf("cursor %s", s.reader.cursor)
	}
	writer.failAt = 0
	if err := s.catchUp(nil); err != nil {
		t.Fatal(err)
	}
	if heads != 1 || !reflect.DeepEqual(writer.frames, []readerFrame{{"c2.Zmlyc3Q", "mutation"}, {"c2.dGFpbA", "mutation"}}) {
		t.Fatalf("heads=%d frames=%v", heads, writer.frames)
	}
}

func TestFixedReplayFenceRequiresBoundedSource(t *testing.T) {
	headCalls := 0
	h := NewHandler(HandlerConfig{Hub: NewHub(), OpenMutationSource: openFixtureMutationSource(func(context.Context, string, string, int) (backend.MutationPage, error) {
		headCalls++
		return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: "c2.aGVhZA"}, nil
	}, nil)})
	writer := &readerTestWriter{}
	s := fenceSession(h, writer)
	if err := s.initialize("c2.c3RhcnQ"); err == nil {
		t.Fatal("accepted unbounded-only source")
	}
	if headCalls != 0 || len(writer.frames) != 0 {
		t.Fatal("read or wrote before checking bounded capability")
	}
}
