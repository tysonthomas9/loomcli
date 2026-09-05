package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

const authoritativeCursorHistory = 256

type authoritativeWriteError struct{ cause error }

func (e *authoritativeWriteError) Error() string { return e.cause.Error() }
func (e *authoritativeWriteError) Unwrap() error { return e.cause }
func isAuthoritativeWriteError(err error) bool {
	var writeErr *authoritativeWriteError
	return errors.As(err, &writeErr)
}

// authoritativeReader owns one connection's source checkpoint. Notifications
// only schedule reads; they never supply payloads or checkpoint values here.
// The caller serializes reads and owns polling, retries, and resync policy.
type authoritativeReader struct {
	workspace   string
	cursor      string
	sourceRepos []string
	getPage     mutationPageFn
	recent      []string
}

func newAuthoritativeReader(workspace, cursor string, sourceRepos []string, getPage mutationPageFn) (*authoritativeReader, error) {
	if workspace == "" || cursor == "" || getPage == nil {
		return nil, fmt.Errorf("authoritative reader requires workspace, starting cursor and page source")
	}
	if err := validateFrame(&cursor, nil, nil); err != nil {
		return nil, err
	}
	return &authoritativeReader{workspace: workspace, cursor: cursor, sourceRepos: slices.Clone(sourceRepos), getPage: getPage, recent: []string{cursor}}, nil
}

// readPage consumes exactly one bounded source page. The cursor changes only
// after a successful frame write; retry after a partial write resumes at that
// prefix. Backend/validation errors emit nothing and never authorize resync
// advancement. Opaque cursor equality supplies identity, never total ordering.
func (r *authoritativeReader) readPage(ctx context.Context, sw frameWriter, limit int) (bool, error) {
	if sw == nil || limit <= 0 || limit > 1000 {
		return false, fmt.Errorf("authoritative reader requires writer and page limit in [1,1000]")
	}
	page, err := r.getPage(ctx, r.workspace, r.cursor, limit)
	if err != nil {
		return false, fmt.Errorf("read authoritative mutation page: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := validateAuthoritativePage(r.cursor, page, limit); err != nil {
		return false, err
	}
	// Equality detects only cycles within this bounded window, not source order.
	// Keep it across idle pages/errors, and remember only completed pages so a
	// partial write can retry its remaining suffix without a false cycle.
	if page.Cursor != r.cursor && slices.Contains(r.recent, page.Cursor) {
		return false, fmt.Errorf("authoritative mutation page repeats recent cursor %q", page.Cursor)
	}
	if err := r.writePage(sw, page); err != nil {
		return false, err
	}
	r.rememberPage(page.Cursor)
	return page.HasMore, nil
}

func (r *authoritativeReader) writePage(sw frameWriter, page backend.MutationPage) error {
	for _, mutation := range page.Events {
		payload := BackendMutationToPayload(mutation, r.workspace)
		if !MatchesSourceRepoFilter(r.sourceRepos, payload.SourceRepo) {
			continue
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode authoritative mutation: %w", err)
		}
		if err := sw.WriteEventID(mutation.Cursor, "mutation", string(data)); err != nil {
			return &authoritativeWriteError{cause: err}
		}
		r.cursor = mutation.Cursor
	}
	// The source page's cursor also covers records excluded by this connection's
	// filter. This is a transport checkpoint, not a mutation or snapshot fence.
	if page.Cursor != r.cursor {
		if err := sw.WriteEventID(page.Cursor, "checkpoint", "{}"); err != nil {
			return &authoritativeWriteError{cause: err}
		}
		r.cursor = page.Cursor
	}
	return nil
}

func (r *authoritativeReader) rememberPage(cursor string) {
	if r.recent[len(r.recent)-1] != cursor {
		if len(r.recent) == authoritativeCursorHistory {
			copy(r.recent, r.recent[1:])
			r.recent = r.recent[:len(r.recent)-1]
		}
		r.recent = append(r.recent, cursor)
	}
}

func validateAuthoritativePage(previous string, page backend.MutationPage, limit int) error {
	if page.Cursor == "" {
		return fmt.Errorf("authoritative mutation page has no cursor")
	}
	if err := validateFrame(&page.Cursor, nil, nil); err != nil {
		return err
	}
	if len(page.Events) > limit {
		return fmt.Errorf("authoritative mutation page exceeds requested limit")
	}
	if (page.HasMore || len(page.Events) > 0) && page.Cursor == previous {
		return fmt.Errorf("authoritative mutation page did not advance")
	}
	seen := map[string]struct{}{previous: {}}
	for i, event := range page.Events {
		if event.Cursor == "" {
			return fmt.Errorf("authoritative mutation %d has no source cursor", i)
		}
		if err := validateFrame(&event.Cursor, nil, nil); err != nil {
			return err
		}
		if _, duplicate := seen[event.Cursor]; duplicate {
			return fmt.Errorf("authoritative mutation page repeats cursor %q", event.Cursor)
		}
		if event.Cursor == page.Cursor && i != len(page.Events)-1 {
			return fmt.Errorf("authoritative page cursor precedes a returned mutation")
		}
		seen[event.Cursor] = struct{}{}
	}
	return nil
}
