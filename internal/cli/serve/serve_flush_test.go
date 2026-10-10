package serve

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestFlushAfterCreate(t *testing.T) {
	create := func(context.Context, service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
		return service.WorkspaceCreateResult{WorkspaceID: "WS"}, nil
	}
	flushed := 0
	ok := flushAfterCreate(create, func(context.Context) error { flushed++; return nil })
	ctx := service.WithCreateWarnings(context.Background())
	if res, err := ok(ctx, service.WorkspaceCreateRequest{}); err != nil || res.WorkspaceID != "WS" || flushed != 1 {
		t.Fatalf("got %+v, %v, flushed=%d", res, err, flushed)
	}
	if w := service.GetCreateWarnings(ctx); w != nil {
		t.Fatalf("unexpected warnings %v", w)
	}

	failing := flushAfterCreate(create, func(context.Context) error { return errors.New("disk full") })
	ctx = service.WithCreateWarnings(context.Background())
	if res, err := failing(ctx, service.WorkspaceCreateRequest{}); err != nil || res.WorkspaceID != "WS" {
		t.Fatalf("got %+v, %v", res, err)
	}
	if w := service.GetCreateWarnings(ctx); len(w) != 1 {
		t.Fatalf("want one warning for the failed flush, got %v", w)
	}

	expired, cancel := context.WithCancel(service.WithCreateWarnings(context.Background()))
	cancel()
	timedOut := flushAfterCreate(create, func(ctx context.Context) error { return ctx.Err() })
	if _, err := timedOut(expired, service.WorkspaceCreateRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("want the request's context error from an expired flush, got %v", err)
	}
}
