package skillmat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	materializationLeaseTTL            = 15 * time.Second
	materializationLeaseRenewTimeout   = 2 * time.Second
	materializationLeaseReleaseTimeout = 2 * time.Second
	maxMaterializationLeaseHolderBytes = 256
)

var materializationLeaseBackoff = [...]time.Duration{
	100 * time.Millisecond,
	200 * time.Millisecond,
	400 * time.Millisecond,
	800 * time.Millisecond,
}

type leasedMaterializeDeps struct {
	hostname   func() (string, error)
	pid        func() int
	sleep      func(context.Context, time.Duration) error
	resolve    func(context.Context, store.Store, string, string) (*materializationPlan, error)
	apply      func(context.Context, store.Store, string, string, *materializationPlan, beforeMaterializationCommit) error
	renewEvery time.Duration
}

// MaterializeLeased freezes exact manifests, serializes materializers that
// target the same host-local directory, and keeps those manifests rooted until
// immediately before the atomic generation swap. Lease loss fails closed.
func MaterializeLeased(ctx context.Context, st store.Store, workspace, roleName, targetDir string) error {
	return materializeLeasedWith(ctx, st, workspace, roleName, targetDir, leasedMaterializeDeps{
		hostname:   os.Hostname,
		pid:        os.Getpid,
		sleep:      sleepMaterializationLeaseBackoff,
		renewEvery: materializationLeaseTTL / 3,
		resolve:    resolveMaterializationPlan,
		apply: func(ctx context.Context, st store.Store, workspace, targetDir string, plan *materializationPlan, beforeCommit beforeMaterializationCommit) error {
			return materializePlanWithRootOpener(ctx, st, workspace, targetDir, plan, openSecureRoot, beforeCommit)
		},
	})
}

func materializeLeasedWith(
	ctx context.Context,
	st store.Store,
	workspace string,
	roleName string,
	targetDir string,
	deps leasedMaterializeDeps,
) error {
	if st == nil {
		return fmt.Errorf("materialize skills: store is not configured")
	}
	if strings.TrimSpace(targetDir) == "" {
		return fmt.Errorf("materialize skills: target directory is required")
	}
	absoluteTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("resolve skill materialization target: %w", err)
	}
	hostname, err := deps.hostname()
	if err != nil {
		return fmt.Errorf("resolve skill materialization hostname: %w", err)
	}
	targetKey := skillMaterializationTargetKey(hostname, absoluteTarget)
	holder := skillMaterializationLeaseHolder(roleName, hostname, deps.pid())
	leases := st.SkillMaterializationLeases()
	if leases == nil {
		return &StoreUnavailableError{Err: domain.ErrSkillMaterializationLeaseStoreUnavailable}
	}

	lease, plan, err := acquireMaterializationLease(
		ctx, st, workspace, roleName, absoluteTarget, targetKey, holder, leases, deps,
	)
	if err != nil || lease == nil {
		return err
	}

	materializeErr := materializeWithLeaseRenewal(ctx, st, workspace, targetDir, targetKey, lease.Token, plan, leases, deps)
	releaseCtx, cancelRelease := context.WithTimeout(context.WithoutCancel(ctx), materializationLeaseReleaseTimeout)
	defer cancelRelease()
	if err := leases.Release(releaseCtx, workspace, targetKey, lease.Token); err != nil {
		slog.Warn("skill materialization lease release failed",
			"workspace", workspace, "role", roleName, "target", absoluteTarget, "err", err)
	}
	return materializeErr
}

func acquireMaterializationLease(
	ctx context.Context,
	st store.Store,
	workspace, roleName, absoluteTarget, targetKey, holder string,
	leases store.SkillMaterializationLeaseStore,
	deps leasedMaterializeDeps,
) (*domain.SkillMaterializationLease, *materializationPlan, error) {
	for attempt := 0; ; attempt++ {
		plan, err := deps.resolve(ctx, st, workspace, roleName)
		if err != nil {
			return nil, nil, err
		}
		lease, err := leases.Acquire(ctx, store.SkillMaterializationLeaseAcquire{
			WorkspaceKey: workspace, Holder: holder, TargetKey: targetKey,
			TreeRevisions: append([]string{}, plan.TreeRevisions...), TTL: materializationLeaseTTL,
		})
		if err == nil {
			return acceptMaterializationLease(ctx, workspace, roleName, absoluteTarget, targetKey, holder, leases, lease, plan)
		}
		var conflict *domain.SkillMaterializationLeaseConflictError
		if !errors.As(err, &conflict) {
			if errors.Is(err, domain.ErrSkillMaterializationLeaseStoreUnavailable) || isTransportError(err) {
				return nil, nil, &StoreUnavailableError{Err: fmt.Errorf("acquire skill materialization lease: %w", err)}
			}
			return nil, nil, fmt.Errorf("acquire skill materialization lease: %w", err)
		}
		if attempt == len(materializationLeaseBackoff) {
			slog.Info("skill materialization lease remains held; skipping this materialization",
				"workspace", workspace, "role", roleName, "target", absoluteTarget,
				"holder", conflict.Holder, "expires_at", conflict.ExpiresAt)
			return nil, nil, nil
		}
		if err := deps.sleep(ctx, materializationLeaseBackoff[attempt]); err != nil {
			return nil, nil, err
		}
	}
}

func acceptMaterializationLease(
	ctx context.Context,
	workspace, roleName, absoluteTarget, targetKey, holder string,
	leases store.SkillMaterializationLeaseStore,
	lease *domain.SkillMaterializationLease,
	plan *materializationPlan,
) (*domain.SkillMaterializationLease, *materializationPlan, error) {
	if lease == nil || strings.TrimSpace(lease.Token) == "" {
		return nil, nil, fmt.Errorf("acquire skill materialization lease: fleet-db returned an empty lease")
	}
	validationErr := validateAcquiredMaterializationLease(lease, targetKey, holder, plan.TreeRevisions, time.Now())
	if validationErr == nil {
		return lease, plan, nil
	}
	releaseCtx, cancelRelease := context.WithTimeout(context.WithoutCancel(ctx), materializationLeaseReleaseTimeout)
	releaseErr := leases.Release(releaseCtx, workspace, targetKey, lease.Token)
	cancelRelease()
	if releaseErr != nil {
		slog.Warn("rejected skill materialization lease release failed",
			"workspace", workspace, "role", roleName, "target", absoluteTarget, "err", releaseErr)
	}
	return nil, nil, validationErr
}

func validateAcquiredMaterializationLease(
	lease *domain.SkillMaterializationLease,
	targetKey, holder string,
	treeRevisions []string,
	now time.Time,
) error {
	switch {
	case lease.TargetKey != targetKey:
		return fmt.Errorf("acquire skill materialization lease: fleet-db returned target %q, want %q: %w", lease.TargetKey, targetKey, domain.ErrIntegrity)
	case lease.Holder != holder:
		return fmt.Errorf("acquire skill materialization lease: fleet-db returned holder %q, want %q: %w", lease.Holder, holder, domain.ErrIntegrity)
	case !lease.ExpiresAt.After(now):
		return fmt.Errorf("acquire skill materialization lease: fleet-db returned a missing or expired expiry: %w", domain.ErrIntegrity)
	case lease.TreeRevisions == nil:
		return fmt.Errorf("acquire skill materialization lease: fleet-db omitted tree_revisions: %w", domain.ErrIntegrity)
	case !slices.Equal(lease.TreeRevisions, treeRevisions):
		return fmt.Errorf("acquire skill materialization lease: fleet-db returned a different tree revision set: %w", domain.ErrIntegrity)
	default:
		return nil
	}
}

func materializeWithLeaseRenewal(
	ctx context.Context,
	st store.Store,
	workspace, targetDir, targetKey, token string,
	plan *materializationPlan,
	leases store.SkillMaterializationLeaseStore,
	deps leasedMaterializeDeps,
) error {
	renew := func(renewCtx context.Context) error {
		return renewMaterializationLease(renewCtx, leases, workspace, targetKey, token)
	}
	operationCtx, cancel, stopRenewal := startMaterializationLeaseRenewal(ctx, deps.renewEvery, renew)
	defer cancel()
	applyErr := deps.apply(operationCtx, st, workspace, targetDir, plan, func(commitCtx context.Context) error {
		if err := stopRenewal(); err != nil {
			return err
		}
		return renew(commitCtx)
	})
	return errors.Join(applyErr, stopRenewal())
}

func startMaterializationLeaseRenewal(
	ctx context.Context,
	renewEvery time.Duration,
	renew func(context.Context) error,
) (context.Context, context.CancelFunc, func() error) {
	if renewEvery <= 0 || renewEvery >= materializationLeaseTTL {
		renewEvery = materializationLeaseTTL / 3
	}
	operationCtx, cancel := context.WithCancel(ctx)
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(renewEvery)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				done <- nil
				return
			case <-operationCtx.Done():
				done <- operationCtx.Err()
				return
			case <-ticker.C:
				if err := renew(operationCtx); err != nil {
					done <- err
					cancel()
					return
				}
			}
		}
	}()

	var stopOnce sync.Once
	var backgroundErr error
	stopRenewal := func() error {
		stopOnce.Do(func() {
			close(stop)
			backgroundErr = <-done
		})
		return backgroundErr
	}
	return operationCtx, cancel, stopRenewal
}

func renewMaterializationLease(ctx context.Context, leases store.SkillMaterializationLeaseStore, workspace, targetKey, token string) error {
	renewCtx, cancel := context.WithTimeout(ctx, materializationLeaseRenewTimeout)
	defer cancel()
	_, err := leases.Renew(renewCtx, workspace, targetKey, token, materializationLeaseTTL)
	if err != nil && ctx.Err() == nil && (errors.Is(err, domain.ErrSkillMaterializationLeaseStoreUnavailable) || isTransportError(err)) {
		return &StoreUnavailableError{Err: fmt.Errorf("renew skill materialization lease: %w", err)}
	}
	return err
}

// skillMaterializationTargetKey is hex(sha256(hostname + "\x00" +
// absoluteTargetDir)). Every materialization writer must use this exact format
// so processes on one host agree on the lease for a target directory.
func skillMaterializationTargetKey(hostname, absoluteTargetDir string) string {
	sum := sha256.Sum256([]byte(hostname + "\x00" + absoluteTargetDir))
	return hex.EncodeToString(sum[:])
}

func skillMaterializationLeaseHolder(roleName, hostname string, pid int) string {
	roleName = strings.TrimSpace(roleName)
	if roleName == "" {
		roleName = "agent"
	}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		hostname = "unknown-host"
	}
	suffix := "#" + strconv.Itoa(pid)
	budget := maxMaterializationLeaseHolderBytes - len("@") - len(suffix)
	roleBudget := budget / 2
	hostBudget := budget - roleBudget
	return truncateUTF8Bytes(roleName, roleBudget) + "@" + truncateUTF8Bytes(hostname, hostBudget) + suffix
}

func truncateUTF8Bytes(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	for maxBytes > 0 && !utf8.ValidString(value[:maxBytes]) {
		maxBytes--
	}
	return value[:maxBytes]
}

func sleepMaterializationLeaseBackoff(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
