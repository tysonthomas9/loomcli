package realtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

type selectedRegistrySource struct {
	ordinary int
	read     func(context.Context, string) (backend.IssueRecoverySnapshot, error)
}

func (s *selectedRegistrySource) ReadIssueRecovery(context.Context) (backend.IssueRecoverySnapshot, error) {
	s.ordinary++
	return registrySnapshot(), nil
}
func (s *selectedRegistrySource) ReadIssueRecoveryForIssue(ctx context.Context, id string) (backend.IssueRecoverySnapshot, error) {
	return s.read(ctx, id)
}

func TestRecoveryRegistrySelectedOwnership(t *testing.T) {
	for _, mode := range []string{"valid", "missing echo", "foreign echo", "cancel", "expiry", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			registry := NewRecoveryRegistry()
			defer registry.Close()
			started, release := make(chan struct{}), make(chan struct{})
			source := &selectedRegistrySource{read: func(ctx context.Context, id string) (backend.IssueRecoverySnapshot, error) {
				close(started)
				<-release // Deliberately ignore cancellation to test post-read fence.
				result := registrySnapshot()
				result.SelectedIssueID = id
				result.Document, _ = json.Marshal(map[string]any{"history": map[string]any{"issue_id": id}})
				if mode == "missing echo" {
					result.SelectedIssueID = ""
				}
				if mode == "foreign echo" {
					result.SelectedIssueID = "B"
				}
				return result, nil
			}}
			handle, err := registry.Register("alice", "WS", nil, source, "s1.Zml4dHVyZQ")
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				result, err := registry.ReadForIssue(ctx, "alice", "WS", handle.Handle, "A")
				if err != nil && len(result.Document) > 0 {
					t.Error("failed read leaked document")
				}
				done <- err
			}()
			registryWait(t, started)
			_, err = registry.ReadForIssue(t.Context(), "alice", "WS", handle.Handle, "B")
			require.ErrorIs(t, err, ErrRecoveryBusy)
			_, err = registry.Read(t.Context(), "alice", "WS", handle.Handle)
			require.ErrorIs(t, err, ErrRecoveryBusy)
			switch mode {
			case "cancel":
				cancel()
			case "expiry":
				registryClock(registry, handle.ExpiresAt.Add(time.Second))
			case "shutdown":
				registry.Close()
			}
			close(release)
			err = registryError(t, done)
			if mode == "valid" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Zero(t, source.ordinary)
		})
	}
}

func TestRecoveryRegistrySelectedDoesNotFallback(t *testing.T) {
	registry := NewRecoveryRegistry()
	defer registry.Close()
	calls := 0
	source := registryRecoverySource(func(context.Context) (backend.IssueRecoverySnapshot, error) { calls++; return registrySnapshot(), nil })
	handle, err := registry.Register("alice", "WS", nil, source, "s1.Zml4dHVyZQ")
	require.NoError(t, err)
	_, err = registry.ReadForIssue(t.Context(), "alice", "WS", handle.Handle, "A")
	require.Error(t, err)
	require.Zero(t, calls)
	_, err = registry.Read(t.Context(), "alice", "WS", handle.Handle)
	require.NoError(t, err)
	require.Equal(t, 1, calls, "failed selected read releases shared flight")
}
