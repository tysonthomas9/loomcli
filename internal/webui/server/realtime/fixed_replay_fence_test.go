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
	head := "opaque-a"
	h := NewHandler(HandlerConfig{Hub: NewHub(), GetMutationPage: func(_ context.Context, ws, since string, limit int) (backend.MutationPage, error) {
		headCalls++
		if ws != "ws" || since != "$" || limit != 1 {
			t.Fatalf("head arguments: %s %s %d", ws, since, limit)
		}
		return backend.MutationPage{Cursor: head}, nil
	}, GetMutationPageThrough: func(_ context.Context, _ string, since, through string, _ int) (backend.MutationPage, error) {
		pageCalls++
		head = "newer-tail" // The source keeps growing while the captured pass runs.
		switch since {
		case "start":
			if through != "opaque-a" {
				t.Fatalf("changed fence %q", through)
			}
			return backend.MutationPage{Cursor: "opaque-z", HasMore: true, Events: []backend.MutationData{readerMutation("opaque-z", "wanted")}}, nil
		case "opaque-z":
			if through != "opaque-a" {
				t.Fatalf("changed fence %q", through)
			}
			// A backend-filtered tail still carries the exact raw fence checkpoint.
			return backend.MutationPage{Cursor: through}, nil
		case "opaque-a":
			if through != "newer-tail" {
				t.Fatalf("next pass fence %q", through)
			}
			return backend.MutationPage{Cursor: through, Events: []backend.MutationData{readerMutation(through, "wanted")}}, nil
		default:
			t.Fatalf("unexpected cursor %q", since)
			return backend.MutationPage{}, nil
		}
	}})
	writer := &readerTestWriter{}
	s := fenceSession(h, writer)
	if err := s.initialize("start"); err != nil {
		t.Fatal(err)
	}
	if err := s.catchUp(nil); err != nil {
		t.Fatal(err)
	}
	if headCalls != 1 || pageCalls != 2 || s.reader.cursor != "opaque-a" {
		t.Fatalf("head=%d pages=%d cursor=%s", headCalls, pageCalls, s.reader.cursor)
	}
	if !reflect.DeepEqual(writer.frames, []readerFrame{{"opaque-z", "mutation"}, {"opaque-a", "checkpoint"}}) {
		t.Fatalf("frames %v", writer.frames)
	}
	if err := s.catchUp(nil); err != nil {
		t.Fatal(err)
	}
	if headCalls != 2 || s.reader.cursor != "newer-tail" {
		t.Fatalf("next pass head=%d cursor=%s", headCalls, s.reader.cursor)
	}
}

func TestFixedReplayFenceRejectsTerminalDisagreementBeforeWrites(t *testing.T) {
	for _, page := range []backend.MutationPage{
		{Cursor: "fence", HasMore: true, Events: []backend.MutationData{readerMutation("fence", "wanted")}},
		{Cursor: "middle", Events: []backend.MutationData{readerMutation("middle", "wanted")}},
	} {
		t.Run(page.Cursor, func(t *testing.T) {
			writer := &readerTestWriter{}
			h := NewHandler(HandlerConfig{Hub: NewHub(), GetMutationPage: func(context.Context, string, string, int) (backend.MutationPage, error) {
				return backend.MutationPage{Cursor: "fence"}, nil
			}, GetMutationPageThrough: func(context.Context, string, string, string, int) (backend.MutationPage, error) { return page, nil }})
			s := fenceSession(h, writer)
			if err := s.initialize("start"); err != nil {
				t.Fatal(err)
			}
			if err := s.catchUp(nil); err == nil {
				t.Fatal("accepted invalid terminal page")
			}
			if len(writer.frames) != 0 || s.reader.cursor != "start" {
				t.Fatalf("advanced: %v %s", writer.frames, s.reader.cursor)
			}
		})
	}
}

func TestFixedReplayFenceExpiryDoesNotWriteFreshHeadOrResetResume(t *testing.T) {
	for _, start := range []string{"", "accepted"} {
		t.Run("start="+start, func(t *testing.T) {
			writer := &readerTestWriter{}
			h := NewHandler(HandlerConfig{Hub: NewHub(), GetMutationPage: func(context.Context, string, string, int) (backend.MutationPage, error) {
				return backend.MutationPage{Cursor: "head"}, nil
			}, GetMutationPageThrough: func(context.Context, string, string, string, int) (backend.MutationPage, error) {
				return backend.MutationPage{}, backend.ErrMutationCursorExpired
			}})
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
	h := NewHandler(HandlerConfig{Hub: NewHub(), GetMutationPage: func(context.Context, string, string, int) (backend.MutationPage, error) {
		heads++
		return backend.MutationPage{Cursor: "tail"}, nil
	}, GetMutationPageThrough: func(_ context.Context, _ string, since, through string, _ int) (backend.MutationPage, error) {
		if through != "tail" {
			t.Fatalf("fence %s", through)
		}
		events := []backend.MutationData{readerMutation("first", "wanted"), readerMutation("tail", "wanted")}
		if since == "first" {
			events = events[1:]
		} else if since != "start" {
			t.Fatalf("since %s", since)
		}
		return backend.MutationPage{Cursor: through, Events: events}, nil
	}})
	writer := &readerTestWriter{failAt: 2, writeError: writeErr}
	s := fenceSession(h, writer)
	if err := s.initialize("start"); err != nil {
		t.Fatal(err)
	}
	if err := s.catchUp(nil); !errors.Is(err, writeErr) || !isAuthoritativeWriteError(err) {
		t.Fatalf("error %v", err)
	}
	if s.reader.cursor != "first" {
		t.Fatalf("cursor %s", s.reader.cursor)
	}
	writer.failAt = 0
	if err := s.catchUp(nil); err != nil {
		t.Fatal(err)
	}
	if heads != 1 || !reflect.DeepEqual(writer.frames, []readerFrame{{"first", "mutation"}, {"tail", "mutation"}}) {
		t.Fatalf("heads=%d frames=%v", heads, writer.frames)
	}
}

func TestFixedReplayFenceRequiresBoundedSource(t *testing.T) {
	headCalls := 0
	h := NewHandler(HandlerConfig{Hub: NewHub(), GetMutationPage: func(context.Context, string, string, int) (backend.MutationPage, error) {
		headCalls++
		return backend.MutationPage{Cursor: "head"}, nil
	}})
	writer := &readerTestWriter{}
	s := fenceSession(h, writer)
	if err := s.initialize("start"); err == nil {
		t.Fatal("accepted unbounded-only source")
	}
	if headCalls != 0 || len(writer.frames) != 0 {
		t.Fatal("read or wrote before checking bounded capability")
	}
}
