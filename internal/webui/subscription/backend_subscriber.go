package subscription

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

const (
	backendWaitTimeout     = 10 * time.Second
	backendRetryDelay      = 2 * time.Second
	backendEmptyPollDelay  = time.Second
	mutationPageLimit      = 100
	defaultDrainMaxPages   = 200
	defaultDrainMaxEvents  = 20_000
	defaultDrainMaxElapsed = 30 * time.Second
)

type subscriberBudgets struct {
	maxDrainPages  int
	maxDrainEvents int
	drainTimeout   time.Duration
}

func defaultSubscriberBudgets() subscriberBudgets {
	return subscriberBudgets{
		maxDrainPages:  defaultDrainMaxPages,
		maxDrainEvents: defaultDrainMaxEvents,
		drainTimeout:   defaultDrainMaxElapsed,
	}
}

type drainCapError struct {
	pages  int
	events int
	cause  string
}

func (e *drainCapError) Error() string {
	return fmt.Sprintf("subscriber head drain exceeded %s after %d pages and %d events", e.cause, e.pages, e.events)
}

// BackendMutationSubscriber sources mutation pages from a backend and bridges
// live events onto the shared realtime Hub. Start is asynchronous; callers use
// Ready before relying on its head cursor.
type BackendMutationSubscriber struct {
	backend     backend.IssueBackend
	hub         *realtime.Hub
	workspaceID string
	initialHead string
	budgets     subscriberBudgets

	wg sync.WaitGroup

	mu         sync.RWMutex
	lastSince  int64
	lastCursor string

	// Owned by the polling goroutine; bounded recent history survives retries.
	// It detects short cycles, not arbitrary historical cursor regressions.
	livePageCursors map[string]struct{}
	livePageOrder   []string

	readyOnce sync.Once
	ready     chan struct{}
	readyMu   sync.RWMutex
	readyHead string
	readyErr  error

	lifecycleMu sync.Mutex
	startOnce   sync.Once
	stopOnce    sync.Once

	ctx    context.Context
	cancel context.CancelFunc
}

// NewBackendMutationSubscriber creates a subscriber with production drain
// budgets. The first unsupported-probe activation drains from cursor "0".
func NewBackendMutationSubscriber(b backend.IssueBackend, hub *realtime.Hub, workspaceID string) *BackendMutationSubscriber {
	return newBackendMutationSubscriber(b, hub, workspaceID, "0", defaultSubscriberBudgets())
}

func newBackendMutationSubscriber(
	b backend.IssueBackend,
	hub *realtime.Hub,
	workspaceID string,
	initialHead string,
	budgets subscriberBudgets,
) *BackendMutationSubscriber {
	if initialHead == "" {
		initialHead = "0"
	}
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // Stop owns the subscriber lifetime.
	return &BackendMutationSubscriber{
		backend:     b,
		hub:         hub,
		workspaceID: workspaceID,
		initialHead: initialHead,
		budgets:     budgets,
		ready:       make(chan struct{}),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start begins head discovery and then live long-polling in a background
// goroutine. It returns immediately and is safe to call more than once.
func (s *BackendMutationSubscriber) Start() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.ctx.Err() != nil {
		s.reportReady("", context.Canceled)
		return
	}
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go s.loop()
	})
}

// Ready waits until Start has found a complete head or failed. The caller's
// context only bounds this wait; it does not cancel shared subscriber startup.
func (s *BackendMutationSubscriber) Ready(ctx context.Context) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-s.ready:
		s.readyMu.RLock()
		defer s.readyMu.RUnlock()
		return s.readyHead, s.readyErr
	default:
	}
	select {
	case <-s.ready:
		s.readyMu.RLock()
		defer s.readyMu.RUnlock()
		return s.readyHead, s.readyErr
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Head returns the subscriber's latest fully consumed cursor. Before
// readiness it returns the configured fallback starting cursor.
func (s *BackendMutationSubscriber) Head() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastCursor != "" {
		return s.lastCursor
	}
	return s.initialHead
}

// Stop cancels discovery or polling, releases readiness waiters, and waits for
// the subscriber goroutine. It is safe before Start and safe to call repeatedly.
func (s *BackendMutationSubscriber) Stop() {
	s.stopOnce.Do(func() {
		s.lifecycleMu.Lock()
		s.cancel()
		s.reportReady("", context.Canceled)
		s.lifecycleMu.Unlock()
		s.wg.Wait()
		slog.Info("backend mutation subscription stopped", "workspace", s.workspaceID)
	})
}

// GetMutationPage returns one bounded catch-up page and propagates backend
// errors so the HTTP handshake can fail before opening the SSE stream.
func (s *BackendMutationSubscriber) GetMutationPage(ctx context.Context, since string, limit int) (backend.MutationPage, error) {
	return s.getMutationPage(ctx, since, limit)
}

// GetMutationHead requires an authoritative cursor backend and binds the read
// to both the caller and this subscriber's lifetime.
func (s *BackendMutationSubscriber) GetMutationHead(ctx context.Context) (backend.MutationPage, error) {
	reader, ok := s.backend.(backend.CursorMutationBackend)
	if !ok {
		return backend.MutationPage{}, fmt.Errorf("backend does not support authoritative mutation head")
	}
	return withSubscriberLifetime(ctx, s.ctx, func(ctx context.Context) (backend.MutationPage, error) { return reader.GetMutationsAfter(ctx, "$", 1) })
}

// GetMutationPageThrough reads a fixed interval without falling back to an
// unbounded backend. Both caller cancellation and subscriber retirement apply.
func (s *BackendMutationSubscriber) GetMutationPageThrough(ctx context.Context, since, through string, limit int) (backend.MutationPage, error) {
	reader, ok := s.backend.(backend.BoundedCursorMutationBackend)
	if !ok {
		return backend.MutationPage{}, fmt.Errorf("backend does not support bounded mutation replay")
	}
	return withSubscriberLifetime(ctx, s.ctx, func(ctx context.Context) (backend.MutationPage, error) {
		return reader.GetMutationsThrough(ctx, since, through, limit)
	})
}

// ReadIssueRecovery uses the same backend object as authoritative mutation
// reads. Retirement invalidates even a successful late HTTP response.
func (s *BackendMutationSubscriber) ReadIssueRecovery(ctx context.Context) (backend.IssueRecoverySnapshot, error) {
	reader, ok := s.backend.(backend.IssueRecoveryBackend)
	if !ok {
		return backend.IssueRecoverySnapshot{}, fmt.Errorf("backend does not support certified issue recovery")
	}
	return withSubscriberLifetime(ctx, s.ctx, reader.ReadIssueRecovery)
}

// ReadIssueRecoveryForIssue uses the same captured backend and retirement fence.
func (s *BackendMutationSubscriber) ReadIssueRecoveryForIssue(ctx context.Context, issueID string) (backend.IssueRecoverySnapshot, error) {
	if !backend.ValidRecoveryIssueSelection(issueID) {
		return backend.IssueRecoverySnapshot{}, fmt.Errorf("invalid selected issue")
	}
	reader, ok := s.backend.(backend.IssueRecoverySelectedBackend)
	if !ok {
		return backend.IssueRecoverySnapshot{}, fmt.Errorf("backend does not support selected issue recovery")
	}
	return withSubscriberLifetime(ctx, s.ctx, func(ctx context.Context) (backend.IssueRecoverySnapshot, error) {
		return reader.ReadIssueRecoveryForIssue(ctx, issueID)
	})
}

// withSubscriberLifetime is the shared cancellation boundary for authoritative
// pages and certified reads; neither may return success after retirement.
func withSubscriberLifetime[T any](ctx, lifetime context.Context, read func(context.Context) (T, error)) (T, error) {
	var zero T
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(lifetime, cancel)
	defer stop()
	if err := lifetime.Err(); err != nil {
		return zero, err
	}
	if err := requestCtx.Err(); err != nil {
		return zero, err
	}
	result, err := read(requestCtx)
	if err != nil {
		return zero, err
	}
	if err := lifetime.Err(); err != nil {
		return zero, err
	}
	if err := requestCtx.Err(); err != nil {
		return zero, err
	}
	return result, nil
}

func (s *BackendMutationSubscriber) loop() {
	defer s.wg.Done()

	head, err := s.discoverHead()
	if err != nil {
		var capErr *drainCapError
		if errors.As(err, &capErr) {
			slog.Warn("backend mutation subscriber head drain capped",
				"workspace", s.workspaceID, "pages", capErr.pages, "events", capErr.events, "err", err)
		} else if !errors.Is(err, context.Canceled) {
			slog.Error("backend mutation subscriber head discovery failed", "workspace", s.workspaceID, "err", err)
		}
		s.reportReady("", err)
		return
	}
	s.setCursor(head, nil)
	s.reportReady(head, nil)
	slog.Info("backend mutation subscription started at head", "workspace", s.workspaceID, "cursor", head)

	timeoutMs := int64(backendWaitTimeout / time.Millisecond)
	for {
		if s.ctx.Err() != nil {
			return
		}
		cursor := s.Head()
		page, err := s.waitForMutationPage(cursor, timeoutMs)
		if err != nil {
			if errors.Is(err, context.Canceled) || s.ctx.Err() != nil {
				return
			}
			slog.Error("backend WaitForMutations error", "workspace", s.workspaceID, "err", err)
			s.waitWithCancel(backendRetryDelay)
			timeoutMs = int64(backendWaitTimeout / time.Millisecond)
			continue
		}

		timeoutMs, err = s.handlePage(cursor, page)
		if err != nil {
			slog.Error("backend mutation page rejected", "workspace", s.workspaceID, "cursor", cursor, "err", err)
			s.waitWithCancel(backendRetryDelay)
			timeoutMs = int64(backendWaitTimeout / time.Millisecond)
		}
	}
}

func (s *BackendMutationSubscriber) handlePage(cursor string, page backend.MutationPage) (int64, error) {
	if s.livePageCursors == nil {
		s.livePageCursors = map[string]struct{}{cursor: {}}
		s.livePageOrder = []string{cursor}
	}
	if err := validateMutationPageProgress(cursor, page, s.livePageCursors); err != nil {
		return 0, err
	}
	// Live traffic may never reach an idle page. Bound cycle history instead
	// of imposing the startup drain cap on a healthy sustained stream.
	window := s.budgets.maxDrainPages
	if window <= 0 {
		window = defaultDrainMaxPages
	}
	if len(s.livePageOrder) >= window {
		delete(s.livePageCursors, s.livePageOrder[0])
		s.livePageOrder = s.livePageOrder[1:]
	}
	s.livePageCursors[page.Cursor] = struct{}{}
	s.livePageOrder = append(s.livePageOrder, page.Cursor)
	if !page.HasMore {
		s.livePageCursors = nil
		s.livePageOrder = nil
	}
	if page.Cursor == "" {
		page.Cursor = cursor
	}
	s.setCursor(page.Cursor, page.Events)
	for _, mutation := range page.Events {
		if s.hub != nil {
			s.hub.Broadcast(realtime.BackendMutationToPayload(mutation, s.workspaceID))
		}
	}
	if len(page.Events) > 0 {
		slog.Info("broadcast backend mutations to SSE clients",
			"workspace", s.workspaceID, "count", len(page.Events), "clients", s.clientCount())
	}

	if page.HasMore {
		return 0, nil
	}
	if len(page.Events) == 0 {
		s.waitWithCancel(backendEmptyPollDelay)
	}
	return int64(backendWaitTimeout / time.Millisecond), nil
}

func (s *BackendMutationSubscriber) discoverHead() (string, error) {
	if s.backend == nil {
		return "", fmt.Errorf("discover subscriber head: backend is nil")
	}
	if cursorBackend, ok := s.backend.(backend.CursorMutationBackend); ok {
		head, supported, err := cursorBackend.ProbeHead(s.ctx)
		if err != nil {
			return "", err
		}
		if supported {
			if head == "" {
				return "", fmt.Errorf("discover subscriber head: probe returned an empty cursor")
			}
			return head, nil
		}
	}
	return s.drainToHead(s.initialHead)
}

func (s *BackendMutationSubscriber) drainToHead(start string) (string, error) {
	ctx, cancel := context.WithTimeout(s.ctx, s.budgets.drainTimeout)
	defer cancel()
	cursor := start
	seen := map[string]struct{}{start: {}}
	pages := 0
	events := 0
	for {
		page, err := s.getMutationPage(ctx, cursor, mutationPageLimit)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return "", &drainCapError{pages: pages, events: events, cause: "time budget"}
			}
			return "", err
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", &drainCapError{pages: pages, events: events, cause: "time budget"}
		}
		if err := validateMutationPageProgress(cursor, page, seen); err != nil {
			return "", fmt.Errorf("discover subscriber head: %w", err)
		}
		seen[page.Cursor] = struct{}{}
		pages++
		events += len(page.Events)
		if page.Cursor != "" {
			cursor = page.Cursor
		}
		if events > s.budgets.maxDrainEvents {
			return "", &drainCapError{pages: pages, events: events, cause: "event budget"}
		}
		if !page.HasMore {
			return cursor, nil
		}
		if pages >= s.budgets.maxDrainPages {
			return "", &drainCapError{pages: pages, events: events, cause: "page budget"}
		}
		if events >= s.budgets.maxDrainEvents {
			return "", &drainCapError{pages: pages, events: events, cause: "event budget"}
		}
		select {
		case <-ctx.Done():
			return "", &drainCapError{pages: pages, events: events, cause: "time budget"}
		default:
		}
	}
}

func (s *BackendMutationSubscriber) getMutationPage(ctx context.Context, since string, limit int) (backend.MutationPage, error) {
	if s.backend == nil {
		return backend.MutationPage{}, fmt.Errorf("get mutation page: backend is nil")
	}
	if cursorBackend, ok := s.backend.(backend.CursorMutationBackend); ok {
		return cursorBackend.GetMutationsAfter(ctx, since, limit)
	}
	events, err := s.backend.GetMutations(ctx, parseCursorMillis(since))
	if err != nil {
		return backend.MutationPage{}, err
	}
	return legacyMutationPage(since, events), nil
}

func (s *BackendMutationSubscriber) waitForMutationPage(cursor string, timeoutMs int64) (backend.MutationPage, error) {
	reqCtx, reqCancel := context.WithTimeout(s.ctx, backendWaitTimeout+10*time.Second)
	defer reqCancel()
	if cursorBackend, ok := s.backend.(backend.CursorMutationBackend); ok {
		return cursorBackend.WaitForMutationsAfter(reqCtx, cursor, timeoutMs, mutationPageLimit)
	}
	events, err := s.backend.WaitForMutations(reqCtx, parseCursorMillis(cursor), timeoutMs)
	if err != nil {
		return backend.MutationPage{}, err
	}
	return legacyMutationPage(cursor, events), nil
}

func legacyMutationPage(previous string, events []backend.MutationData) backend.MutationPage {
	cursor := previous
	var maxMs int64
	for _, event := range events {
		if event.Cursor != "" {
			cursor = event.Cursor
		}
		if ms := event.Timestamp.UnixMilli(); ms > maxMs {
			maxMs = ms
		}
	}
	if cursor == previous && maxMs > 0 {
		cursor = fmt.Sprintf("%d", maxMs)
	}
	if events == nil {
		events = []backend.MutationData{}
	}
	return backend.MutationPage{Events: events, Cursor: cursor}
}

func (s *BackendMutationSubscriber) setCursor(cursor string, events []backend.MutationData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cursor != "" {
		s.lastCursor = cursor
	}
	for _, event := range events {
		if ms := event.Timestamp.UnixMilli(); ms > s.lastSince {
			s.lastSince = ms
		}
	}
}

func (s *BackendMutationSubscriber) reportReady(head string, err error) {
	s.readyOnce.Do(func() {
		s.readyMu.Lock()
		s.readyHead = head
		s.readyErr = err
		s.readyMu.Unlock()
		close(s.ready)
	})
}

func (s *BackendMutationSubscriber) clientCount() int {
	if s.hub == nil {
		return 0
	}
	return s.hub.ClientCount()
}

func (s *BackendMutationSubscriber) waitWithCancel(d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-s.ctx.Done():
	case <-timer.C:
	}
}

// Cursors are opaque identities: reject cycles without interpreting timestamps
// or lexical order. An empty terminal idle page may legitimately retain its
// input cursor. A page claiming records or more work must advance explicitly.
func validateMutationPageProgress(previous string, page backend.MutationPage, seen map[string]struct{}) error {
	requiresProgress := page.HasMore || len(page.Events) > 0
	if requiresProgress && (page.Cursor == "" || page.Cursor == previous) {
		return fmt.Errorf("mutation pagination did not advance from %q", previous)
	}
	if page.Cursor != "" && page.Cursor != previous {
		if _, duplicate := seen[page.Cursor]; duplicate {
			return fmt.Errorf("mutation pagination repeated cursor %q", page.Cursor)
		}
	}
	return nil
}
