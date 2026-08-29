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
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	materializationLeaseTTL            = 15 * time.Second
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
	hostname    func() (string, error)
	pid         func() int
	sleep       func(context.Context, time.Duration) error
	materialize func(context.Context, store.Store, string, string, string) error
}

// MaterializeLeased serializes materializers that target the same host-local
// directory, degrading to the existing unlocked behavior when the ephemeral
// lease service is unavailable.
func MaterializeLeased(ctx context.Context, st store.Store, workspace, roleName, targetDir string) error {
	return materializeLeasedWith(ctx, st, workspace, roleName, targetDir, leasedMaterializeDeps{
		hostname:    os.Hostname,
		pid:         os.Getpid,
		sleep:       sleepMaterializationLeaseBackoff,
		materialize: materialize,
	})
}

//nolint:funlen // Acquire, bounded retry, degrade, and release read as one protocol.
func materializeLeasedWith(
	ctx context.Context,
	st store.Store,
	workspace string,
	roleName string,
	targetDir string,
	deps leasedMaterializeDeps,
) error {
	if st == nil {
		return deps.materialize(ctx, st, workspace, roleName, targetDir)
	}
	if strings.TrimSpace(targetDir) == "" {
		return deps.materialize(ctx, st, workspace, roleName, targetDir)
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
		slog.Warn("skill materialization lease store is not configured; continuing without lease",
			"workspace", workspace, "role", roleName, "target", absoluteTarget)
		return deps.materialize(ctx, st, workspace, roleName, targetDir)
	}

	var conflict *domain.SkillMaterializationLeaseConflictError
	var lease *domain.SkillMaterializationLease
	for attempt := 0; ; attempt++ {
		lease, err = leases.Acquire(ctx, store.SkillMaterializationLeaseAcquire{
			WorkspaceKey: workspace,
			Holder:       holder,
			TargetKey:    targetKey,
			TTL:          materializationLeaseTTL,
		})
		if err == nil {
			if lease == nil || strings.TrimSpace(lease.Token) == "" {
				return fmt.Errorf("acquire skill materialization lease: fleet-db returned an empty lease")
			}
			break
		}
		if !errors.As(err, &conflict) {
			if errors.Is(err, domain.ErrSkillMaterializationLeaseStoreUnavailable) || isTransportError(err) {
				slog.Warn("skill materialization lease unavailable; continuing without lease",
					"workspace", workspace, "role", roleName, "target", absoluteTarget, "err", err)
				return deps.materialize(ctx, st, workspace, roleName, targetDir)
			}
			return fmt.Errorf("acquire skill materialization lease: %w", err)
		}
		if attempt == len(materializationLeaseBackoff) {
			slog.Info("skill materialization lease remains held; skipping this materialization",
				"workspace", workspace, "role", roleName, "target", absoluteTarget,
				"holder", conflict.Holder, "expires_at", conflict.ExpiresAt)
			return nil
		}
		if err := deps.sleep(ctx, materializationLeaseBackoff[attempt]); err != nil {
			return err
		}
		conflict = nil
	}

	materializeErr := deps.materialize(ctx, st, workspace, roleName, targetDir)
	releaseCtx, cancelRelease := context.WithTimeout(context.WithoutCancel(ctx), materializationLeaseReleaseTimeout)
	defer cancelRelease()
	if err := leases.Release(releaseCtx, workspace, targetKey, lease.Token); err != nil {
		slog.Warn("skill materialization lease release failed",
			"workspace", workspace, "role", roleName, "target", absoluteTarget, "err", err)
	}
	return materializeErr
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
