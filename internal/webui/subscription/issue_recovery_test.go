package subscription

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

type recoverySubscriber struct {
	sourceBindingSubscriber
	recover func(context.Context) (backend.IssueRecoverySnapshot, error)
}

func (s *recoverySubscriber) ReadIssueRecovery(ctx context.Context) (backend.IssueRecoverySnapshot, error) {
	return s.recover(ctx)
}

func TestRecoveryUsesCapturedSource(t *testing.T) {
	for _, mode := range []string{"valid", "replaced", "closed", "wrong workspace", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			sub := &recoverySubscriber{recover: func(context.Context) (backend.IssueRecoverySnapshot, error) {
				calls++
				ws := "ws"
				if mode == "wrong workspace" {
					ws = "other"
				}
				return backend.IssueRecoverySnapshot{Workspace: ws, Through: "same", Document: []byte(`{"proof":true}`)}, nil
			}}
			m := &MultiWorkspaceSubscriber{subscribers: map[string]*subscriberEntry{"ws": {sub: sub}}}
			source, err := m.OpenMutationSource(t.Context(), "ws")
			if err != nil {
				t.Fatal(err)
			}
			reader := source.(backend.IssueRecoveryBackend)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "replaced":
				m.subscribers["ws"] = &subscriberEntry{sub: sub}
			case "closed":
				m.closed = true
			case "canceled":
				cancel()
			}
			got, err := reader.ReadIssueRecovery(ctx)
			if mode == "valid" {
				if err != nil || len(got.Document) == 0 || calls != 1 {
					t.Fatalf("result=%+v calls=%d err=%v", got, calls, err)
				}
				return
			}
			if err == nil || len(got.Document) != 0 {
				t.Fatalf("invalid result accepted: %+v %v", got, err)
			}
			if mode != "wrong workspace" && calls != 0 {
				t.Fatal("invalid source reached reader")
			}
		})
	}
}

func TestRecoveryRejectsInFlightRetirement(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	sub := &recoverySubscriber{recover: func(context.Context) (backend.IssueRecoverySnapshot, error) {
		close(started)
		<-release
		return backend.IssueRecoverySnapshot{Workspace: "ws", Through: "same", Document: []byte(`{}`)}, nil
	}}
	m := &MultiWorkspaceSubscriber{subscribers: map[string]*subscriberEntry{"ws": {sub: sub}}}
	source, err := m.OpenMutationSource(t.Context(), "ws")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		got, err := source.(backend.IssueRecoveryBackend).ReadIssueRecovery(t.Context())
		if len(got.Document) != 0 {
			done <- errors.New("retired document leaked")
			return
		}
		if err == nil {
			done <- errors.New("retired read succeeded")
			return
		}
		done <- nil
	}()
	waitBoundedSignal(t, started)
	m.mu.Lock()
	m.subscribers["ws"] = &subscriberEntry{sub: sub}
	m.mu.Unlock()
	close(release)
	if err := waitBoundedError(t, done); err != nil {
		t.Fatal(err)
	}
}

type recoveryTestBackend struct {
	backend.IssueBackend
	recover func(context.Context) (backend.IssueRecoverySnapshot, error)
}

func (b *recoveryTestBackend) ReadIssueRecovery(ctx context.Context) (backend.IssueRecoverySnapshot, error) {
	return b.recover(ctx)
}

func TestRecoverySubscriberLifetime(t *testing.T) {
	for _, mode := range []string{"stop", "caller"} {
		t.Run(mode, func(t *testing.T) {
			started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			b := &recoveryTestBackend{IssueBackend: newFakeBackend(), recover: func(ctx context.Context) (backend.IssueRecoverySnapshot, error) {
				close(started)
				<-ctx.Done()
				close(canceled)
				<-release
				return backend.IssueRecoverySnapshot{Workspace: "ws", Document: []byte(`{}`)}, nil
			}}
			sub := NewBackendMutationSubscriber(b, nil, "ws")
			defer sub.Stop()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				got, err := sub.ReadIssueRecovery(ctx)
				if len(got.Document) != 0 {
					done <- errors.New("late document leaked")
					return
				}
				done <- err
			}()
			waitBoundedSignal(t, started)
			if mode == "stop" {
				sub.Stop()
			} else {
				cancel()
			}
			waitBoundedSignal(t, canceled)
			close(release)
			if err := waitBoundedError(t, done); !errors.Is(err, context.Canceled) {
				t.Fatalf("late success: %v", err)
			}
		})
	}
	legacy := NewBackendMutationSubscriber(newFakeBackend(), nil, "ws")
	defer legacy.Stop()
	if _, err := legacy.ReadIssueRecovery(t.Context()); err == nil {
		t.Fatal("legacy recovery accepted")
	}
}
