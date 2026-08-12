package supervisor

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// ConcurrencyTracker enforces per-role concurrency limits in the daemon.
// Roles without a configured MaxConcurrency (nil or 0) have no limit.
// Thread-safe via sync.Mutex + sync.Cond for blocking acquisition.
type ConcurrencyTracker struct {
	mu     sync.Mutex
	cond   *sync.Cond
	counts map[string]int // role -> current active count
	limits map[string]int // role -> max concurrent (0 = unlimited)
	closed bool           // set on shutdown to unblock all waiters
}

// NewConcurrencyTracker builds a tracker from the role config map.
// Roles with nil or zero MaxConcurrency get limit 0 (unlimited).
func NewConcurrencyTracker(roles map[string]config.RoleConfig) *ConcurrencyTracker {
	ct := &ConcurrencyTracker{
		counts: make(map[string]int),
		limits: make(map[string]int),
	}
	ct.cond = sync.NewCond(&ct.mu)

	for name, rc := range roles {
		if rc.MaxConcurrency != nil && *rc.MaxConcurrency > 0 {
			ct.limits[name] = *rc.MaxConcurrency
		}
	}
	return ct
}

// Acquire blocks until a concurrency slot is available for the role.
// Returns true if acquired, false if the tracker was closed (shutdown).
// Roles with no limit (0) always acquire immediately. Safe on nil receiver.
func (ct *ConcurrencyTracker) Acquire(role string) bool {
	return ct.acquire(role, nil)
}

// AcquireUntil blocks until a concurrency slot is available or stop is
// closed. Unlike a plain Acquire, a per-agent Stop can therefore interrupt a
// worker queued behind another agent's role limit.
func (ct *ConcurrencyTracker) AcquireUntil(role string, stop <-chan struct{}) bool {
	return ct.acquire(role, stop)
}

func (ct *ConcurrencyTracker) acquire(role string, stop <-chan struct{}) bool {
	if ct == nil {
		return !channelClosed(stop)
	}
	ct.mu.Lock()
	defer ct.mu.Unlock()

	limit := ct.limits[role]
	if limit == 0 {
		if ct.closed || channelClosed(stop) {
			return false
		}
		ct.counts[role]++
		return true
	}

	// Wake the condition when this agent alone is stopped. Taking ct.mu before
	// Broadcast closes the check-to-Wait race: if stop closes after the loop
	// check, the notifier cannot broadcast until Cond.Wait atomically releases
	// the mutex.
	waitDone := make(chan struct{})
	if stop != nil {
		go func() {
			select {
			case <-stop:
				ct.mu.Lock()
				ct.cond.Broadcast()
				ct.mu.Unlock()
			case <-waitDone:
			}
		}()
		defer close(waitDone)
	}

	for ct.counts[role] >= limit && !ct.closed && !channelClosed(stop) {
		ct.cond.Wait()
	}

	if ct.closed || channelClosed(stop) {
		return false
	}

	ct.counts[role]++
	return true
}

func channelClosed(ch <-chan struct{}) bool {
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// TryAcquire attempts a non-blocking acquire. Returns false if at limit. Safe on nil receiver.
func (ct *ConcurrencyTracker) TryAcquire(role string) bool {
	if ct == nil {
		return true
	}
	ct.mu.Lock()
	defer ct.mu.Unlock()

	if ct.closed {
		return false
	}

	limit := ct.limits[role]
	if limit == 0 {
		ct.counts[role]++
		return true
	}

	if ct.counts[role] >= limit {
		return false
	}

	ct.counts[role]++
	return true
}

// Release decrements the active count for a role and wakes waiters. Safe on nil receiver.
func (ct *ConcurrencyTracker) Release(role string) {
	if ct == nil {
		return
	}
	ct.mu.Lock()
	defer ct.mu.Unlock()

	if ct.counts[role] <= 0 {
		log.Printf("[concurrency] Warning: Release called for role %q with count %d (bug?)", role, ct.counts[role])
		ct.counts[role] = 0
		return
	}

	ct.counts[role]--
	ct.cond.Broadcast()
}

// ActiveCount returns the current active count for a role. Safe on nil receiver.
func (ct *ConcurrencyTracker) ActiveCount(role string) int {
	if ct == nil {
		return 0
	}
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return ct.counts[role]
}

// Counts returns a copy of the counts map (for status/monitoring). Safe on nil receiver.
func (ct *ConcurrencyTracker) Counts() map[string]int {
	if ct == nil {
		return map[string]int{}
	}
	ct.mu.Lock()
	defer ct.mu.Unlock()

	result := make(map[string]int, len(ct.counts))
	for k, v := range ct.counts {
		result[k] = v
	}
	return result
}

// Close sets the tracker to closed state and wakes all blocked Acquire callers.
// Safe to call multiple times (idempotent) and on nil receiver.
func (ct *ConcurrencyTracker) Close() {
	if ct == nil {
		return
	}
	ct.mu.Lock()
	defer ct.mu.Unlock()

	if ct.closed {
		return
	}
	ct.closed = true
	ct.cond.Broadcast()
}

// --- Git diff capture ---

// captureGitDiff runs `git diff HEAD` and returns the output truncated.
func captureGitDiff(worktreePath string, maxBytes int) string {
	resolver := cli.GetDefaultResolver()
	if resolver.Mode == cli.ModeWorkspace {
		worktrees, err := resolver.DiscoverWorktrees()
		if err == nil && len(worktrees) > 0 {
			return captureMultiRepoDiff(worktrees, maxBytes)
		}
	}
	return captureSingleRepoDiff(worktreePath, maxBytes)
}

func captureSingleRepoDiff(repoPath string, maxBytes int) string {
	output, err := cli.RunGitCommand(repoPath, "diff", "HEAD")
	if err != nil {
		return ""
	}
	output = strings.TrimSpace(output)
	return config.TruncateDiff(output, maxBytes)
}

func captureMultiRepoDiff(worktrees []cli.WorktreeInfo, maxBytes int) string {
	var sb strings.Builder
	for _, wt := range worktrees {
		output, err := cli.RunGitCommand(wt.Path, "diff", "HEAD")
		if err != nil {
			continue
		}
		output = strings.TrimSpace(output)
		if output == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("--- repo: %s ---\n", wt.Name))
		sb.WriteString(output)
		sb.WriteString("\n")
	}
	return config.TruncateDiff(sb.String(), maxBytes)
}
