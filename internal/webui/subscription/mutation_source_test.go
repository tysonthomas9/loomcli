package subscription

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

type sourceBindingSubscriber struct {
	trackingWorkspaceSubscriber
	head func(context.Context) (backend.MutationPage, error)
	page func(context.Context, string, string, int) (backend.MutationPage, error)
}

func (s *sourceBindingSubscriber) GetMutationHead(ctx context.Context) (backend.MutationPage, error) {
	return s.head(ctx)
}
func (s *sourceBindingSubscriber) GetMutationPageThrough(ctx context.Context, a, b string, n int) (backend.MutationPage, error) {
	return s.page(ctx, a, b, n)
}

func TestMutationSourcePinsAcrossReads(t *testing.T) {
	for _, between := range []string{"head and page", "live passes"} {
		t.Run(between, func(t *testing.T) {
			oldCalls, newCalls := 0, 0
			old := &sourceBindingSubscriber{head: func(context.Context) (backend.MutationPage, error) {
				oldCalls++
				return backend.MutationPage{Cursor: "same"}, nil
			}, page: func(context.Context, string, string, int) (backend.MutationPage, error) {
				oldCalls++
				return backend.MutationPage{Cursor: "same"}, nil
			}}
			replacement := &sourceBindingSubscriber{head: func(context.Context) (backend.MutationPage, error) {
				newCalls++
				return backend.MutationPage{Cursor: "same"}, nil
			}, page: func(context.Context, string, string, int) (backend.MutationPage, error) {
				newCalls++
				return backend.MutationPage{Cursor: "same"}, nil
			}}
			m := &MultiWorkspaceSubscriber{subscribers: map[string]*subscriberEntry{"ws": {sub: old}}}
			openCtx, cancel := context.WithCancel(t.Context())
			source, err := m.OpenMutationSource(openCtx, "ws")
			if err != nil {
				t.Fatal(err)
			}
			cancel() // factory setup deadline is not the source lifetime
			if _, err := source.ReadHead(t.Context()); err != nil {
				t.Fatal(err)
			}
			if between == "live passes" {
				if _, err := source.ReadPage(t.Context(), "same", "same", 1); err != nil {
					t.Fatal(err)
				}
			}
			m.mu.Lock()
			m.subscribers["ws"] = &subscriberEntry{sub: replacement}
			m.mu.Unlock()
			if _, err := source.ReadPage(t.Context(), "same", "same", 1); err == nil {
				t.Fatal("replacement page accepted")
			}
			if _, err := source.ReadHead(t.Context()); err == nil {
				t.Fatal("replacement head accepted")
			}
			if newCalls != 0 || oldCalls == 0 {
				t.Fatalf("calls old=%d replacement=%d", oldCalls, newCalls)
			}
		})
	}
}

func TestMutationSourceRejectsInFlightReplacement(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	old := &sourceBindingSubscriber{head: func(context.Context) (backend.MutationPage, error) {
		close(started)
		<-release
		return backend.MutationPage{Cursor: "same"}, nil
	}}
	m := &MultiWorkspaceSubscriber{subscribers: map[string]*subscriberEntry{"ws": {sub: old}}}
	source, err := m.OpenMutationSource(t.Context(), "ws")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := source.ReadHead(t.Context()); done <- err }()
	waitBoundedSignal(t, started)
	m.mu.Lock()
	m.subscribers["ws"] = &subscriberEntry{sub: &trackingWorkspaceSubscriber{}}
	m.mu.Unlock()
	close(release)
	if err := waitBoundedError(t, done); err == nil {
		t.Fatal("retired head accepted")
	}
}

func TestMutationSourceUnavailableAndCallerCanceled(t *testing.T) {
	m := &MultiWorkspaceSubscriber{subscribers: map[string]*subscriberEntry{}}
	if _, err := m.OpenMutationSource(t.Context(), "absent"); err == nil {
		t.Fatal("absent source opened")
	}
	sub := &trackingWorkspaceSubscriber{}
	m.subscribers["ws"] = &subscriberEntry{sub: sub}
	source, err := m.OpenMutationSource(t.Context(), "ws")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := source.ReadHead(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled head: %v", err)
	}
	if sub.getCalls.Load() != 0 {
		t.Fatal("canceled call reached backend")
	}
	m.closed = true
	if _, err := m.OpenMutationSource(t.Context(), "ws"); err == nil {
		t.Fatal("closed manager opened source")
	}
	if _, err := source.ReadPage(t.Context(), "0", "same", 1); err == nil {
		t.Fatal("closed manager read source")
	}
}

func TestMutationHeadRejectsLegacyAndRetiredBackend(t *testing.T) {
	legacy := NewBackendMutationSubscriber(newFakeBackend(), nil, "ws")
	defer legacy.Stop()
	if _, err := legacy.GetMutationHead(t.Context()); err == nil {
		t.Fatal("legacy head fallback accepted")
	}
	started, release := make(chan struct{}), make(chan struct{})
	b := &scriptedCursorBackend{fakeBackend: newFakeBackend()}
	b.getPageFn = func(ctx context.Context, since string, limit int) (backend.MutationPage, error) {
		if since != "$" || limit != 1 {
			t.Error("wrong head arguments")
		}
		close(started)
		<-release
		return backend.MutationPage{Cursor: "same"}, nil
	}
	sub := NewBackendMutationSubscriber(b, nil, "ws")
	done := make(chan error, 1)
	go func() { _, err := sub.GetMutationHead(t.Context()); done <- err }()
	waitBoundedSignal(t, started)
	sub.Stop()
	close(release)
	if err := waitBoundedError(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("retired head: %v", err)
	}
}
