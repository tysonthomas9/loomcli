// Package stacklock serializes every publisher entry for one workspace stack.
package stacklock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

const (
	leaseTTL    = 5 * time.Second
	waitTimeout = 3 * time.Second
	pollDelay   = 25 * time.Millisecond
)

// With holds a durable, renewable lease while action runs. Every process uses
// the same host-local journal and the workspace/stack pair as its lease scope.
func With(ctx context.Context, workspace, stack string, action func(context.Context) error) error {
	if workspace == "" || stack == "" {
		return errors.New("workspace and stack are required")
	}
	dir := bootstrap.LoomDir()
	if dir == "" {
		return errors.New("loom directory is required for stack lock")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create stack journal directory: %w", err)
	}
	store, err := journal.OpenSQLite(filepath.Join(dir, "loomgit-journal.sqlite"))
	if err != nil {
		return fmt.Errorf("open stack journal: %w", err)
	}
	defer func() { _ = store.Close() }()
	return withStore(ctx, store, "stack:"+workspace+":"+stack, waitTimeout, leaseTTL, action)
}

func withStore(ctx context.Context, store loomgit.Store, scope string, timeout, ttl time.Duration, action func(context.Context) error) error {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	owner := hex.EncodeToString(id[:])
	lease, err := claim(ctx, store, scope, owner, timeout, ttl)
	if err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(ctx)
	type renewal struct {
		lease loomgit.Lease
		err   error
	}
	done := make(chan renewal, 1)
	go func() {
		ticker := time.NewTicker(ttl / 3)
		defer ticker.Stop()
		current := lease
		for {
			select {
			case <-runCtx.Done():
				done <- renewal{lease: current}
				return
			case <-ticker.C:
				next, err := store.RenewLease(runCtx, current, ttl)
				if err != nil {
					cancel()
					done <- renewal{lease: current, err: err}
					return
				}
				current = next
			}
		}
	}()
	actionErr := action(runCtx)
	cancel()
	last := <-done
	releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), ttl)
	defer releaseCancel()
	releaseErr := store.ReleaseLease(releaseCtx, last.lease)
	return errors.Join(actionErr, last.err, releaseErr)
}

func claim(ctx context.Context, store loomgit.Store, scope, owner string, timeout, ttl time.Duration) (loomgit.Lease, error) {
	waitCtx, stopWait := context.WithTimeout(ctx, timeout)
	defer stopWait()
	var lease loomgit.Lease
	for {
		var err error
		lease, err = store.ClaimLease(waitCtx, scope, owner, ttl)
		if err == nil {
			break
		}
		if !errors.Is(err, loomgit.ErrLeaseHeld) {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				return lease, loomgit.NewError(loomgit.StackLocked, "stack lock timed out", err)
			}
			return lease, err
		}
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return lease, ctx.Err()
			}
			return lease, loomgit.NewError(loomgit.StackLocked, "stack lock timed out", waitCtx.Err())
		case <-time.After(pollDelay):
		}
	}

	return lease, nil
}
