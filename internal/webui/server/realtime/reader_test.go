package realtime

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

type readerFrame struct{ id, event string }
type readerTestWriter struct {
	frames     []readerFrame
	failAt     int
	writeError error
}

func (w *readerTestWriter) WriteEventID(id, event, _ string) error {
	if w.failAt > 0 && len(w.frames)+1 == w.failAt {
		return w.writeError
	}
	w.frames = append(w.frames, readerFrame{id, event})
	return nil
}
func (w *readerTestWriter) WriteEventNoID(string, string) error {
	panic("authoritative reader must use source IDs")
}
func (w *readerTestWriter) WriteRetry(int) error             { panic("scheduler owns retry") }
func (w *readerTestWriter) WriteResync(string, string) error { panic("reader cannot advance resync") }
func (w *readerTestWriter) WriteComment(string) error        { panic("scheduler owns heartbeat") }
func readerMutation(cursor, repo string) backend.MutationData {
	return backend.MutationData{Cursor: cursor, SourceRepo: repo, Type: "update"}
}

func TestAuthoritativeReaderWritesSourceOrderAndFilterCheckpoint(t *testing.T) {
	repos := []string{"wanted"}
	reader, err := newAuthoritativeReader("WS", "start", repos, func(_ context.Context, ws, since string, limit int) (backend.MutationPage, error) {
		if ws != "WS" || since != "start" || limit != 5 {
			t.Fatalf("read args %s/%s/%d", ws, since, limit)
		}
		return backend.MutationPage{Events: []backend.MutationData{readerMutation("opaque-z", "wanted"), readerMutation("opaque-a", "wanted"), readerMutation("opaque-tail", "excluded")}, Cursor: "opaque-tail", HasMore: true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	repos[0] = "excluded" // caller cannot mutate a connection's established scope.
	writer := &readerTestWriter{}
	more, err := reader.readPage(context.Background(), writer, 5)
	if err != nil || !more {
		t.Fatalf("more=%v err=%v", more, err)
	}
	want := []readerFrame{{"opaque-z", "mutation"}, {"opaque-a", "mutation"}, {"opaque-tail", "checkpoint"}}
	if !reflect.DeepEqual(writer.frames, want) || reader.cursor != "opaque-tail" {
		t.Fatalf("frames=%v cursor=%s", writer.frames, reader.cursor)
	}
}

func TestAuthoritativeReaderWriterFailureResumesSuccessfulPrefix(t *testing.T) {
	writeErr := errors.New("wire failed")
	for _, failAt := range []int{1, 2, 3} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			reader, err := newAuthoritativeReader("WS", "start", []string{"wanted"}, func(_ context.Context, _ string, since string, _ int) (backend.MutationPage, error) {
				events := []backend.MutationData{readerMutation("z", "wanted"), readerMutation("a", "wanted"), readerMutation("tail", "excluded")}
				if since == "z" {
					events = events[1:]
				} else if since == "a" {
					events = events[2:]
				} else if since != "start" {
					t.Fatalf("unexpected retry cursor %q", since)
				}
				return backend.MutationPage{Events: events, Cursor: "tail"}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			writer := &readerTestWriter{failAt: failAt, writeError: writeErr}
			if _, err := reader.readPage(context.Background(), writer, 10); !errors.Is(err, writeErr) || !isAuthoritativeWriteError(err) {
				t.Fatalf("error=%v", err)
			}
			want := []string{"start", "z", "a"}[failAt-1]
			if reader.cursor != want {
				t.Fatalf("failure cursor=%s want%s", reader.cursor, want)
			}
			writer.failAt = 0
			if _, err := reader.readPage(context.Background(), writer, 10); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(writer.frames, []readerFrame{{"z", "mutation"}, {"a", "mutation"}, {"tail", "checkpoint"}}) {
				t.Fatalf("duplicate/missing retry frames: %v", writer.frames)
			}
		})
	}
}

func TestAuthoritativeReaderRejectsPageBeforeAnyWrite(t *testing.T) {
	cases := map[string]backend.MutationPage{
		"missing page cursor":     {},
		"missing event cursor":    {Cursor: "end", Events: []backend.MutationData{readerMutation("valid", ""), readerMutation("", "")}},
		"stalled":                 {Cursor: "start", HasMore: true},
		"repeated input":          {Cursor: "end", Events: []backend.MutationData{readerMutation("start", "")}},
		"duplicate event":         {Cursor: "end", Events: []backend.MutationData{readerMutation("same", ""), readerMutation("same", "")}},
		"page ends before events": {Cursor: "one", Events: []backend.MutationData{readerMutation("one", ""), readerMutation("two", "")}},
		"oversized":               {Cursor: "end", Events: []backend.MutationData{readerMutation("one", ""), readerMutation("two", ""), readerMutation("end", "")}},
	}
	for name, page := range cases {
		t.Run(name, func(t *testing.T) {
			reader, _ := newAuthoritativeReader("WS", "start", nil, func(context.Context, string, string, int) (backend.MutationPage, error) { return page, nil })
			writer := &readerTestWriter{}
			if _, err := reader.readPage(context.Background(), writer, 2); err == nil {
				t.Fatal("invalid page accepted")
			}
			if reader.cursor != "start" || len(writer.frames) > 0 {
				t.Fatalf("invalid page moved cursor/emitted frames: %s/%v", reader.cursor, writer.frames)
			}
		})
	}
}

func TestAuthoritativeReaderBackendFailureAndIdleDoNotAdvance(t *testing.T) {
	backendErr := errors.New("source unavailable")
	reader, _ := newAuthoritativeReader("WS", "start", nil, func(context.Context, string, string, int) (backend.MutationPage, error) {
		return backend.MutationPage{Cursor: "unsafe", Events: []backend.MutationData{readerMutation("unsafe", "")}}, backendErr
	})
	writer := &readerTestWriter{}
	if _, err := reader.readPage(context.Background(), writer, 2); !errors.Is(err, backendErr) {
		t.Fatalf("error=%v", err)
	}
	if reader.cursor != "start" || len(writer.frames) > 0 {
		t.Fatal("backend failure advanced checkpoint")
	}
	reader.getPage = func(context.Context, string, string, int) (backend.MutationPage, error) {
		return backend.MutationPage{Cursor: "start"}, nil
	}
	more, err := reader.readPage(context.Background(), writer, 2)
	if err != nil || more || len(writer.frames) > 0 || reader.cursor != "start" {
		t.Fatalf("idle result %v/%v frames%v cursor%s", more, err, writer.frames, reader.cursor)
	}
}

func TestAuthoritativeReaderRejectsRecentPageCycle(t *testing.T) {
	calls := 0
	reader, _ := newAuthoritativeReader("WS", "A", nil, func(context.Context, string, string, int) (backend.MutationPage, error) {
		calls++
		cursor := "B"
		if calls > 1 {
			cursor = "A"
		}
		return backend.MutationPage{Cursor: cursor, HasMore: true}, nil
	})
	writer := &readerTestWriter{}
	if _, err := reader.readPage(context.Background(), writer, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.readPage(context.Background(), writer, 1); err == nil {
		t.Fatal("cycle accepted")
	}
	if reader.cursor != "B" || len(writer.frames) != 1 {
		t.Fatalf("cycle advanced: %s/%v", reader.cursor, writer.frames)
	}
}

func TestAuthoritativeReaderHealthyPagesOutliveBoundedHistory(t *testing.T) {
	calls := 0
	reader, _ := newAuthoritativeReader("WS", "start", nil, func(context.Context, string, string, int) (backend.MutationPage, error) {
		calls++
		return backend.MutationPage{Cursor: fmt.Sprintf("opaque-%d", calls), HasMore: true}, nil
	})
	writer := &readerTestWriter{}
	for range authoritativeCursorHistory * 2 {
		if _, err := reader.readPage(context.Background(), writer, 1); err != nil {
			t.Fatal(err)
		}
	}
	if len(reader.recent) != authoritativeCursorHistory {
		t.Fatalf("history length %d", len(reader.recent))
	}
}
