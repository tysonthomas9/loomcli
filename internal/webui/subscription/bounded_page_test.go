package subscription

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

type boundedBackend struct {
	*fakeBackend
	read func(context.Context, string, string, int) (backend.MutationPage, error)
}

func (b *boundedBackend) GetMutationsThrough(ctx context.Context, since, through string, limit int) (backend.MutationPage, error) {
	return b.read(ctx, since, through, limit)
}

func TestBoundedPageCapabilityAndContext(t *testing.T) {
	legacy := newFakeBackend()
	sub := NewBackendMutationSubscriber(legacy, nil, "ws")
	defer sub.Stop()
	if _, err := sub.GetMutationPageThrough(t.Context(), "opaque-a", "opaque-b", 3); err == nil {
		t.Fatal("missing bounded capability succeeded")
	}
	if legacy.getCalls.Load() != 0 {
		t.Fatal("fell back to unbounded read")
	}
	sentinel := errors.New("fence expired")
	type contextKey struct{}
	ctx := context.WithValue(t.Context(), contextKey{}, "caller")
	b := &boundedBackend{fakeBackend: newFakeBackend(), read: func(got context.Context, since, through string, limit int) (backend.MutationPage, error) {
		if got.Value(contextKey{}) != "caller" || since != "opaque-a" || through != "opaque-b" || limit != 3 {
			t.Error("request identity changed")
		}
		return backend.MutationPage{}, sentinel
	}}
	sub2 := NewBackendMutationSubscriber(b, nil, "ws")
	defer sub2.Stop()
	if _, err := sub2.GetMutationPageThrough(ctx, "opaque-a", "opaque-b", 3); !errors.Is(err, sentinel) {
		t.Fatalf("error lost: %v", err)
	}
}

func TestBoundedPageRejectsLateResultAfterRetirement(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	b := &boundedBackend{fakeBackend: newFakeBackend(), read: func(context.Context, string, string, int) (backend.MutationPage, error) {
		close(started)
		<-release
		return backend.MutationPage{Cursor: "opaque-b"}, nil
	}}
	sub := NewBackendMutationSubscriber(b, nil, "ws")
	done := make(chan error, 1)
	go func() { _, err := sub.GetMutationPageThrough(t.Context(), "opaque-a", "opaque-b", 3); done <- err }()
	waitBoundedSignal(t, started)
	sub.Stop()
	close(release)
	if err := waitBoundedError(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("retired result accepted: %v", err)
	}
}

type boundedWorkspace struct {
	trackingWorkspaceSubscriber
	read func(context.Context, string, string, int) (backend.MutationPage, error)
}

func (s *boundedWorkspace) GetMutationPageThrough(ctx context.Context, since, through string, limit int) (backend.MutationPage, error) {
	return s.read(ctx, since, through, limit)
}

func TestBoundedMultiRejectsWorkspaceReplacement(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	sub := &boundedWorkspace{read: func(context.Context, string, string, int) (backend.MutationPage, error) {
		close(started)
		<-release
		return backend.MutationPage{Cursor: "fence"}, nil
	}}
	multi := &MultiWorkspaceSubscriber{subscribers: map[string]*subscriberEntry{"ws": {sub: sub}}}
	done := make(chan error, 1)
	go func() {
		_, err := multi.GetMutationPageThroughForWorkspace(t.Context(), "ws", "0", "fence", 1)
		done <- err
	}()
	waitBoundedSignal(t, started)
	multi.mu.Lock()
	multi.subscribers["ws"] = &subscriberEntry{sub: &trackingWorkspaceSubscriber{}}
	multi.mu.Unlock()
	close(release)
	if err := waitBoundedError(t, done); err == nil {
		t.Fatal("replacement accepted old result")
	}
	if _, err := multi.GetMutationPageThroughForWorkspace(t.Context(), "missing", "0", "fence", 1); err == nil {
		t.Fatal("missing workspace accepted")
	}
}

func waitBoundedSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("bounded request did not start")
	}
}
func waitBoundedError(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("bounded request did not finish")
		return nil
	}
}

func TestBoundedPageRejectsCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	b := &boundedBackend{fakeBackend: newFakeBackend(), read: func(got context.Context, _, _ string, _ int) (backend.MutationPage, error) {
		cancel()
		<-got.Done()
		return backend.MutationPage{Cursor: "fence"}, nil
	}}
	sub := NewBackendMutationSubscriber(b, nil, "ws")
	defer sub.Stop()
	if _, err := sub.GetMutationPageThrough(ctx, "0", "fence", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller accepted result: %v", err)
	}
}
