package workspace

import (
	"context"
	"errors"
	"testing"
)

func TestTracingHelpers(t *testing.T) {
	ctx, span := startSpan(context.Background(), "service.Workspace.Test",
		attrLoomWorkspace("WS"),
		attrLoomRepo("api"),
		attrResultCount(2),
	)
	if ctx == nil {
		t.Fatal("startSpan returned nil context")
	}
	recordErr(span, nil)
	recordErr(span, errors.New("boom"))
	span.End()
}
