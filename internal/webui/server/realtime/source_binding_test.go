package realtime

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

type bindingTestSource struct {
	head func(context.Context) (backend.MutationPage, error)
	page func(context.Context, string, string, int) (backend.MutationPage, error)
}

func (s *bindingTestSource) ReadHead(ctx context.Context) (backend.MutationPage, error) {
	return s.head(ctx)
}
func (s *bindingTestSource) ReadPage(ctx context.Context, since, through string, limit int) (backend.MutationPage, error) {
	return s.page(ctx, since, through, limit)
}

func TestMutationSourceBindsAcrossPagesAndLivePasses(t *testing.T) {
	opens, heads, pages := 0, 0, 0
	retired := false
	retireErr := errors.New("source retired")
	old := &bindingTestSource{head: func(context.Context) (backend.MutationPage, error) {
		heads++
		if retired {
			return backend.MutationPage{}, retireErr
		}
		return backend.MutationPage{Cursor: "old-tail"}, nil
	}, page: func(_ context.Context, since, through string, _ int) (backend.MutationPage, error) {
		pages++
		if retired {
			return backend.MutationPage{}, retireErr
		}
		if through != "old-tail" {
			t.Fatalf("wrong fence %s", through)
		}
		if since == "start" {
			return backend.MutationPage{Cursor: "middle", HasMore: true, Events: []backend.MutationData{readerMutation("middle", "wanted")}}, nil
		}
		return backend.MutationPage{Cursor: through, Events: []backend.MutationData{readerMutation(through, "wanted")}}, nil
	}}
	replacement := &bindingTestSource{head: func(context.Context) (backend.MutationPage, error) {
		t.Fatal("replacement head used by established connection")
		return backend.MutationPage{}, nil
	}, page: func(context.Context, string, string, int) (backend.MutationPage, error) {
		t.Fatal("replacement page used")
		return backend.MutationPage{}, nil
	}}
	var current MutationSource = old
	h := NewHandler(HandlerConfig{Hub: NewHub(), OpenMutationSource: func(_ context.Context, ws string) (MutationSource, error) {
		opens++
		if ws != "ws" {
			t.Fatal(ws)
		}
		return current, nil
	}})
	writer := &readerTestWriter{}
	s := fenceSession(h, writer)
	if err := s.initialize("start"); err != nil {
		t.Fatal(err)
	}
	current = replacement
	if err := s.catchUp(nil); err != nil {
		t.Fatal(err)
	}
	if opens != 1 || heads != 1 || pages != 2 || s.reader.cursor != "old-tail" {
		t.Fatalf("opens=%d heads=%d pages=%d cursor=%s", opens, heads, pages, s.reader.cursor)
	}
	retired = true
	if err := s.catchUp(nil); !errors.Is(err, retireErr) {
		t.Fatalf("error %v", err)
	}
	if opens != 1 || pages != 2 || s.reader.cursor != "old-tail" || len(writer.frames) != 2 {
		t.Fatal("retirement replaced source or advanced checkpoint")
	}
}

func TestMutationSourceOpenFailureNeverReadsOrWrites(t *testing.T) {
	openErr := errors.New("cannot bind source")
	for _, mode := range []string{"error", "nil", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h := NewHandler(HandlerConfig{Hub: NewHub(), OpenMutationSource: func(context.Context, string) (MutationSource, error) {
				switch mode {
				case "error":
					return nil, openErr
				case "canceled":
					cancel()
				}
				return nil, nil
			}})
			writer := &readerTestWriter{}
			s := fenceSession(h, writer)
			s.ctx = ctx
			err := s.initialize("accepted")
			if err == nil {
				t.Fatal("accepted failed source binding")
			}
			if mode == "error" && !errors.Is(err, openErr) {
				t.Fatal(err)
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if len(writer.frames) != 0 || s.reader != nil {
				t.Fatal("advanced after failed source binding")
			}
		})
	}
}
