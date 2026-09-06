package realtime

import (
	"context"
	"encoding/base64"
	"errors"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// The fixture binds both explicit endpoint fakes to the same workspace once.
// This adapter is test-only; production source capabilities are indivisible.
func openFixtureMutationSource(head func(context.Context, string, string, int) (backend.MutationPage, error), page func(context.Context, string, string, string, int) (backend.MutationPage, error)) func(context.Context, string) (MutationSource, error) {
	return func(_ context.Context, workspace string) (MutationSource, error) {
		if head == nil || page == nil {
			return nil, errors.New("incomplete fixture mutation source")
		}
		return fixtureMutationSource{workspace: workspace, head: head, page: page}, nil
	}
}

type fixtureMutationSource struct {
	workspace string
	head      func(context.Context, string, string, int) (backend.MutationPage, error)
	page      func(context.Context, string, string, string, int) (backend.MutationPage, error)
}

func (s fixtureMutationSource) ReadHead(ctx context.Context) (backend.MutationPage, error) {
	return s.head(ctx, s.workspace, "$", 1)
}
func (s fixtureMutationSource) ReadPage(ctx context.Context, since, through string, limit int) (backend.MutationPage, error) {
	return s.page(ctx, s.workspace, since, through, limit)
}

func testScopedCursor(label string) string {
	return "c2." + base64.RawURLEncoding.EncodeToString([]byte(label))
}
