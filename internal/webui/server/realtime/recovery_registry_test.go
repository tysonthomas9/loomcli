package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

type registryRecoverySource func(context.Context) (backend.IssueRecoverySnapshot, error)

func (s registryRecoverySource) ReadIssueRecovery(ctx context.Context) (backend.IssueRecoverySnapshot, error) {
	return s(ctx)
}
func registrySnapshot() backend.IssueRecoverySnapshot {
	return backend.IssueRecoverySnapshot{SourceIdentity: "s1.Zml4dHVyZQ", Manifest: issueRecoveryManifest, Workspace: "WS", Through: "c2.MTAtMA", Document: []byte(`{"native":"preserved"}`)}
}
func registrySource() registryRecoverySource {
	return func(context.Context) (backend.IssueRecoverySnapshot, error) { return registrySnapshot(), nil }
}
func registryClock(r *RecoveryRegistry, now time.Time) {
	r.mu.Lock()
	r.now = func() time.Time { return now }
	r.mu.Unlock()
}
func registryWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for controlled source")
	}
}
func registryError(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for recovery read")
		return nil
	}
}

func TestRecoveryRegistryScopeRetryAndCopies(t *testing.T) {
	r := NewRecoveryRegistry()
	defer r.Close()
	now := time.Now().In(time.FixedZone("test-local", -7*60*60))
	registryClock(r, now)
	repos := []string{"repo"}
	handle, err := r.Register("alice", "WS", repos, registrySource(), "s1.Zml4dHVyZQ")
	require.NoError(t, err)
	require.True(t, ValidRecoveryHandle(handle.Handle))
	require.Equal(t, now.Add(time.Minute).UTC(), handle.ExpiresAt)
	wire, err := json.Marshal(handle)
	require.NoError(t, err)
	require.Contains(t, string(wire), `"expires_at":"`+now.Add(time.Minute).UTC().Format(time.RFC3339Nano)+`"`)
	repos[0] = "changed"
	handle.SourceRepos[0] = "also changed"
	require.Equal(t, []string{"repo"}, r.entries[handle.Handle].handle.SourceRepos)
	for _, tc := range []struct {
		principal, workspace, handle string
		want                         error
	}{
		{"bob", "WS", handle.Handle, ErrRecoveryDenied}, {"alice", "OTHER", handle.Handle, ErrRecoveryDenied}, {"alice", "WS", "missing", ErrRecoveryUnavailable},
	} {
		result, err := r.Read(context.Background(), tc.principal, tc.workspace, tc.handle)
		require.ErrorIs(t, err, tc.want)
		require.Empty(t, result.Document)
	}
	for range 2 {
		result, err := r.Read(context.Background(), "alice", "WS", handle.Handle)
		require.NoError(t, err)
		require.Equal(t, registrySnapshot(), result)
	}
	registryClock(r, handle.ExpiresAt)
	result, err := r.Read(context.Background(), "alice", "WS", handle.Handle)
	require.ErrorIs(t, err, ErrRecoveryUnavailable)
	require.Empty(t, result.Document)
}
func TestRecoveryRegistryCapacityAndLazyExpiry(t *testing.T) {
	r := NewRecoveryRegistry()
	defer r.Close()
	now := time.Now()
	registryClock(r, now)
	for i := 0; i < recoveryPrincipalCapacity; i++ {
		_, err := r.Register("alice", "WS", nil, registrySource(), "s1.Zml4dHVyZQ")
		require.NoError(t, err)
	}
	_, err := r.Register("alice", "OTHER", nil, registrySource(), "s1.Zml4dHVyZQ")
	require.ErrorIs(t, err, ErrRecoveryUnavailable)
	for i := recoveryPrincipalCapacity; i < recoveryHandleCapacity; i++ {
		_, err := r.Register(fmt.Sprintf("owner-%d", i), "WS", nil, registrySource(), "s1.Zml4dHVyZQ")
		require.NoError(t, err)
	}
	_, err = r.Register("new-owner", "WS", nil, registrySource(), "s1.Zml4dHVyZQ")
	require.ErrorIs(t, err, ErrRecoveryUnavailable)
	require.Len(t, r.entries, recoveryHandleCapacity)
	registryClock(r, now.Add(time.Minute))
	_, err = r.Register("alice", "WS", nil, registrySource(), "s1.Zml4dHVyZQ")
	require.NoError(t, err)
	require.Len(t, r.entries, 1)
}
func TestRecoveryRegistryBusyDoesNotHoldRegistryLock(t *testing.T) {
	r := NewRecoveryRegistry()
	defer r.Close()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	source := registryRecoverySource(func(context.Context) (backend.IssueRecoverySnapshot, error) {
		calls.Add(1)
		close(started)
		<-release
		return registrySnapshot(), nil
	})
	handle, err := r.Register("alice", "WS", nil, source, "s1.Zml4dHVyZQ")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { _, err := r.Read(context.Background(), "alice", "WS", handle.Handle); done <- err }()
	registryWait(t, started)
	result, err := r.Read(context.Background(), "alice", "WS", handle.Handle)
	require.ErrorIs(t, err, ErrRecoveryBusy)
	require.Empty(t, result.Document)
	other, err := r.Register("bob", "WS", nil, registrySource(), "s1.Zml4dHVyZQ")
	require.NoError(t, err)
	_, err = r.Read(context.Background(), "bob", "WS", other.Handle)
	require.NoError(t, err)
	close(release)
	require.NoError(t, registryError(t, done))
	require.Equal(t, int32(1), calls.Load())
}
func TestRecoveryRegistryDiscardsLateSuccess(t *testing.T) {
	for _, mode := range []string{"cancel", "expiry", "close"} {
		t.Run(mode, func(t *testing.T) {
			r := NewRecoveryRegistry()
			defer r.Close()
			now := time.Now()
			registryClock(r, now)
			started := make(chan struct{})
			release := make(chan struct{})
			sourceCanceled := make(chan struct{})
			source := registryRecoverySource(func(ctx context.Context) (backend.IssueRecoverySnapshot, error) {
				close(started)
				if mode != "expiry" {
					<-ctx.Done()
					close(sourceCanceled)
				}
				<-release // Deliberately return success after cancellation.
				return registrySnapshot(), nil
			})
			handle, err := r.Register("alice", "WS", nil, source, "s1.Zml4dHVyZQ")
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				result, err := r.Read(ctx, "alice", "WS", handle.Handle)
				if len(result.Document) != 0 {
					done <- errors.New("late document exposed")
					return
				}
				done <- err
			}()
			registryWait(t, started)
			switch mode {
			case "cancel":
				cancel()
				registryWait(t, sourceCanceled)
			case "close":
				r.Close()
				registryWait(t, sourceCanceled)
			case "expiry":
				registryClock(r, handle.ExpiresAt)
			}
			close(release)
			err = registryError(t, done)
			if mode == "expiry" {
				require.ErrorIs(t, err, ErrRecoveryUnavailable)
			} else {
				require.ErrorIs(t, err, context.Canceled)
			}
		})
	}
}
func TestRecoveryRegistryResultAndSourceErrors(t *testing.T) {
	for _, mode := range []string{"workspace", "manifest", "legacy-v1", "legacy-cursor", "source-identity", "document", "source"} {
		t.Run(mode, func(t *testing.T) {
			r := NewRecoveryRegistry()
			defer r.Close()
			sourceErr := errors.New("read failed")
			source := registryRecoverySource(func(context.Context) (backend.IssueRecoverySnapshot, error) {
				result := registrySnapshot()
				switch mode {
				case "workspace":
					result.Workspace = "OTHER"
				case "manifest":
					result.Manifest = "other"
				case "legacy-v1":
					result.Manifest = "fleet.issue-workspace.v1"
				case "legacy-cursor":
					result.Through = "c1.MTAtMA"
				case "source-identity":
					result.SourceIdentity = "s1.b3RoZXI"
				case "document":
					result.Document = nil
				case "source":
					return result, sourceErr
				}
				return result, nil
			})
			handle, err := r.Register("alice", "WS", nil, source, "s1.Zml4dHVyZQ")
			require.NoError(t, err)
			result, err := r.Read(context.Background(), "alice", "WS", handle.Handle)
			require.Empty(t, result.Document)
			if mode == "source" {
				require.ErrorIs(t, err, sourceErr)
			} else {
				require.ErrorIs(t, err, ErrRecoveryUnavailable)
			}
			require.False(t, r.entries[handle.Handle].busy)
		})
	}
	r := NewRecoveryRegistry()
	handle, err := r.Register("alice", "WS", nil, registrySource(), "s1.Zml4dHVyZQ")
	require.NoError(t, err)
	r.Close()
	r.Close()
	_, err = r.Register("alice", "WS", nil, registrySource(), "s1.Zml4dHVyZQ")
	require.ErrorIs(t, err, ErrRecoveryUnavailable)
	_, err = r.Read(context.Background(), "alice", "WS", handle.Handle)
	require.ErrorIs(t, err, ErrRecoveryUnavailable)
	require.Empty(t, r.entries)
}
func TestRecoveryRegistryDeadlineIsMintBound(t *testing.T) {
	r := NewRecoveryRegistry()
	defer r.Close()
	var deadline time.Time
	source := registryRecoverySource(func(ctx context.Context) (backend.IssueRecoverySnapshot, error) {
		deadline, _ = ctx.Deadline()
		return registrySnapshot(), nil
	})
	handle, err := r.Register("alice", "WS", nil, source, "s1.Zml4dHVyZQ")
	require.NoError(t, err)
	_, err = r.Read(context.Background(), "alice", "WS", handle.Handle)
	require.NoError(t, err)
	require.Equal(t, handle.ExpiresAt, deadline)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = r.Read(ctx, "alice", "WS", handle.Handle)
	require.NoError(t, err)
	callerDeadline, _ := ctx.Deadline()
	require.Equal(t, callerDeadline, deadline)
}

func TestRecoveryRegistryCanceledCallerDoesNotReadSource(t *testing.T) {
	r := NewRecoveryRegistry()
	defer r.Close()
	source := registryRecoverySource(func(context.Context) (backend.IssueRecoverySnapshot, error) {
		t.Fatal("canceled caller reached source")
		return registrySnapshot(), nil
	})
	handle, err := r.Register("alice", "WS", nil, source, "s1.Zml4dHVyZQ")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := r.Read(ctx, "alice", "WS", handle.Handle)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, result.Document)
	require.False(t, r.entries[handle.Handle].busy)
}

func TestRecoveryRegistryBoundShutdownIsSynchronous(t *testing.T) {
	r := NewRecoveryRegistry()
	defer r.Close()
	shutdown := make(chan struct{})
	r.bindShutdown(shutdown)
	started := make(chan struct{})
	release := make(chan struct{})
	source := registryRecoverySource(func(context.Context) (backend.IssueRecoverySnapshot, error) {
		close(started)
		<-release
		return registrySnapshot(), nil
	})
	handle, err := r.Register("alice", "WS", nil, source, "s1.Zml4dHVyZQ")
	require.NoError(t, err)
	other, err := r.Register("bob", "WS", nil, registrySource(), "s1.Zml4dHVyZQ")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		result, err := r.Read(context.Background(), "alice", "WS", handle.Handle)
		if len(result.Document) != 0 {
			done <- errors.New("shutdown returned document")
			return
		}
		done <- err
	}()
	registryWait(t, started)
	close(shutdown) // No Close watcher has run: every decision must inspect done.
	require.False(t, r.closed)
	_, err = r.Register("carol", "WS", nil, registrySource(), "s1.Zml4dHVyZQ")
	require.ErrorIs(t, err, ErrRecoveryUnavailable)
	result, err := r.Read(context.Background(), "bob", "WS", other.Handle)
	require.ErrorIs(t, err, ErrRecoveryUnavailable)
	require.Empty(t, result.Document)
	close(release)
	require.ErrorIs(t, registryError(t, done), ErrRecoveryUnavailable)
}
