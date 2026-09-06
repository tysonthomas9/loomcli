package realtime

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestBoundMutationSourceRejectsLegacyCursorBeforeCheckpoint(t *testing.T) {
	for _, mode := range []string{"head", "page", "event"} {
		t.Run(mode, func(t *testing.T) {
			start, end := testScopedCursor("start"), testScopedCursor("end")
			source := &bindingTestSource{
				head: func(context.Context) (backend.MutationPage, error) {
					cursor := end
					if mode == "head" {
						cursor = "c1.ZW5k"
					}
					return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: cursor}, nil
				},
				page: func(context.Context, string, string, int) (backend.MutationPage, error) {
					cursor, eventCursor := end, end
					if mode == "page" {
						cursor = "c1.ZW5k"
					}
					if mode == "event" {
						eventCursor = "c1.ZW5k"
					}
					return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: cursor, Events: []backend.MutationData{readerMutation(eventCursor, "wanted")}}, nil
				},
			}
			h := NewHandler(HandlerConfig{Hub: NewHub(), OpenMutationSource: func(context.Context, string) (MutationSource, error) { return source, nil }})
			writer := &readerTestWriter{}
			session := fenceSession(h, writer)
			err := session.initialize(start)
			if err == nil {
				err = session.catchUp(nil)
			}
			if err == nil {
				t.Fatal("legacy cursor accepted")
			}
			if len(writer.frames) != 0 {
				t.Fatalf("invalid source emitted frames: %v", writer.frames)
			}
			if session.reader != nil && session.reader.cursor != start {
				t.Fatalf("checkpoint advanced to %s", session.reader.cursor)
			}
		})
	}
}
