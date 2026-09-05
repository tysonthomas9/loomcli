package realtime

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestReplayReaderRejectsNonAdvancingPages(t *testing.T) {
	cases := map[string][]backend.MutationPage{
		"missing": {{HasMore: true}}, "stalled": {{Cursor: "c1.start", HasMore: true}},
		"cycle":          {{Cursor: "c1.next", HasMore: true}, {Cursor: "c1.start", HasMore: true}},
		"terminal cycle": {{Cursor: "c1.next", HasMore: true}, {Cursor: "c1.start"}},
	}
	for name, pages := range cases {
		t.Run(name, func(t *testing.T) {
			calls := 0
			reader, err := newAuthoritativeReader("WS", "c1.start", nil, func(context.Context, string, string, int) (backend.MutationPage, error) {
				p := pages[calls]
				calls++
				return p, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			writer := newRecordingFrameWriter()
			for range pages {
				previous := reader.cursor
				_, err = reader.readPage(context.Background(), writer, 100)
				if err != nil {
					if reader.cursor != previous {
						t.Fatal("invalid page advanced checkpoint")
					}
					break
				}
			}
			if err == nil {
				t.Fatal("accepted invalid page sequence")
			}
		})
	}
}

func TestReplayReaderCheckpointCoversFilteredTail(t *testing.T) {
	for _, allFiltered := range []bool{false, true} {
		t.Run(map[bool]string{false: "mixed", true: "all filtered"}[allFiltered], func(t *testing.T) {
			repo := "wanted"
			if allFiltered {
				repo = "other"
			}
			calls := 0
			reader, err := newAuthoritativeReader("WS", "c1.start", []string{"wanted"}, func(context.Context, string, string, int) (backend.MutationPage, error) {
				calls++
				if calls == 1 {
					return backend.MutationPage{Events: []backend.MutationData{{Cursor: "c1.visible", Type: "update", SourceRepo: repo}}, Cursor: "c1.visible", HasMore: true}, nil
				}
				return backend.MutationPage{Events: []backend.MutationData{{Cursor: "c1.tail", Type: "update", SourceRepo: "other"}}, Cursor: "c1.tail"}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			writer := newRecordingFrameWriter()
			for range 2 {
				if _, err = reader.readPage(context.Background(), writer, 100); err != nil {
					t.Fatal(err)
				}
			}
			frames := writer.snapshot()
			if len(frames) != 2 {
				t.Fatalf("frames=%+v", frames)
			}
			firstKind := "mutation"
			if allFiltered {
				firstKind = "checkpoint"
			}
			if frames[0].event != firstKind {
				t.Fatalf("first=%+v", frames[0])
			}
			if frames[1] != (recordedFrame{id: "c1.tail", event: "checkpoint", data: "{}"}) || reader.cursor != "c1.tail" {
				t.Fatalf("frames=%+v cursor=%q", frames, reader.cursor)
			}
		})
	}
}

func TestReplayReaderIdlePageDoesNotInventCheckpoint(t *testing.T) {
	reader, err := newAuthoritativeReader("WS", "c1.start", nil, func(context.Context, string, string, int) (backend.MutationPage, error) {
		return backend.MutationPage{Cursor: "c1.start"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	writer := newRecordingFrameWriter()
	more, err := reader.readPage(context.Background(), writer, 100)
	if err != nil || more || len(writer.snapshot()) != 0 || reader.cursor != "c1.start" {
		t.Fatalf("more=%v err=%v cursor=%q frames=%+v", more, err, reader.cursor, writer.snapshot())
	}
}
