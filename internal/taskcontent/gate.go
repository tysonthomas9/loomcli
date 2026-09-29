// Package taskcontent holds the dispatch-time content invariant: a task with
// no description, no acceptance criteria and no design carries no work, and
// must never be handed to a worker role.
//
// The invariant is enforced at CLAIM time rather than at selection time for a
// structural reason: backend.IssueData — the slim projection returned by Ready
// and List — carries neither Description nor AcceptanceCriteria. Those fields
// exist only on backend.IssueDetailData, returned by Get. So the gate cannot be
// a predicate in internal/cli/taskfilter.go; it is a single detail fetch on the
// one candidate that is about to be claimed, memoized on (id, UpdatedAt) so a
// poll loop does not re-read the same bodyless row every cycle.
//
// The package is a leaf: stdlib plus internal/backend for the wire types.
package taskcontent

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// ErrNoContent is the sentinel for a dispatch refused because the task is
// bodyless. Callers wrap it with the path-specific detail.
var ErrNoContent = errors.New("task has no description and no acceptance criteria")

// EnvKillSwitch names the environment variable that disables the gate.
// Set it to off/0/false to restore the pre-gate dispatch behavior exactly.
const EnvKillSwitch = "LOOM_DISPATCH_CONTENT_GATE"

const (
	// refusalTTL bounds how long a refusal memo is trusted. UpdatedAt already
	// invalidates the memo whenever the row is edited; the TTL is the backstop
	// for backends whose timestamps are too coarse to move on a small edit.
	refusalTTL = 15 * time.Minute

	// refusalCacheCap bounds the memo. The supervisor polls forever, so an
	// unbounded map is a slow leak; past the cap the oldest entries are swept.
	refusalCacheCap = 1024
)

// Detailer is the narrow slice of backend.IssueBackend the gate needs.
// backend.IssueBackend satisfies it.
type Detailer interface {
	Get(ctx context.Context, id string) (*backend.IssueDetailData, error)
}

// Enabled reports whether the dispatch content gate is active. It is on by
// default; LOOM_DISPATCH_CONTENT_GATE=off|0|false turns it off.
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvKillSwitch))) {
	case "off", "0", "false", "no":
		return false
	default:
		return true
	}
}

// HasContent reports whether a detail projection carries work.
//
// A title alone is NOT content — that is exactly the shape of the scratch rows
// this gate exists to refuse. Notes are NOT content either: they are ops
// chatter, and a refusal note must never be able to self-heal the row.
//
// A design counts even when the body was later emptied: an implementer must
// still receive planned work. The three-way test mirrors cli.HasDesign.
func HasContent(d *backend.IssueDetailData) bool {
	if d == nil {
		// Nothing was read, so there is no positive evidence of emptiness.
		// Fail open — see Allow.
		return true
	}
	if strings.TrimSpace(d.Description) != "" {
		return true
	}
	if strings.TrimSpace(d.AcceptanceCriteria) != "" {
		return true
	}
	return d.HasDesign || strings.TrimSpace(d.Design) != "" || strings.TrimSpace(d.DesignArtifactID) != ""
}

type refusal struct {
	updatedAt time.Time
	at        time.Time
}

// Gate decides whether a task may be dispatched to a worker role, memoizing
// refusals so a poll loop costs one Get per bodyless row per edit, not one per
// cycle. The zero value is not usable; construct with NewGate. A nil *Gate
// allows everything, so composite literals that leave the field unset keep the
// pre-gate behavior.
type Gate struct {
	mu       sync.Mutex
	refusals map[string]refusal

	// now is injectable so the TTL is testable without sleeping.
	now func() time.Time
}

// NewGate returns a ready gate with an empty refusal memo.
func NewGate() *Gate {
	return &Gate{refusals: make(map[string]refusal), now: time.Now}
}

func (g *Gate) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

// Allow reports whether the task may be dispatched to a worker role.
//
// slim may be the zero backend.IssueData when the caller holds only an ID;
// when it carries UpdatedAt, a refusal memo is invalidated for free the moment
// somebody fills the row in.
//
// It FAILS OPEN. A refusal requires positive evidence of emptiness: if the
// detail read errors, times out, or returns nil, Allow returns (true, nil). A
// guard that could not read the row must never be the reason a fleet stops
// working.
//
// On refusal it returns (false, ErrNoContent) — wrapped by the caller with the
// path-specific message.
func (g *Gate) Allow(ctx context.Context, d Detailer, slim backend.IssueData, id string) (bool, error) {
	if g == nil || d == nil || strings.TrimSpace(id) == "" || !Enabled() {
		return true, nil
	}
	if g.memoizedRefusal(id, slim.UpdatedAt) {
		return false, ErrNoContent
	}
	detail, err := d.Get(ctx, id)
	if err != nil || detail == nil {
		g.forget(id)
		return true, nil
	}
	if HasContent(detail) {
		g.forget(id)
		return true, nil
	}
	// Prefer the authoritative timestamp from the detail read when the caller
	// had no slim row to hand.
	updatedAt := slim.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = detail.UpdatedAt
	}
	g.remember(id, updatedAt)
	return false, ErrNoContent
}

// memoizedRefusal reports whether this exact (id, updatedAt) was refused
// recently. A differing UpdatedAt is a miss: the row changed, so re-read it.
func (g *Gate) memoizedRefusal(id string, updatedAt time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, ok := g.refusals[id]
	if !ok {
		return false
	}
	if !entry.updatedAt.Equal(updatedAt) || g.clock().Sub(entry.at) >= refusalTTL {
		delete(g.refusals, id)
		return false
	}
	return true
}

func (g *Gate) remember(id string, updatedAt time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.refusals == nil {
		g.refusals = make(map[string]refusal)
	}
	if len(g.refusals) >= refusalCacheCap {
		g.sweepLocked()
	}
	g.refusals[id] = refusal{updatedAt: updatedAt, at: g.clock()}
}

func (g *Gate) forget(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.refusals, id)
}

// sweepLocked drops expired entries, and if that was not enough, the oldest
// entries, until the memo is back under the cap. Caller holds g.mu.
func (g *Gate) sweepLocked() {
	now := g.clock()
	for id, entry := range g.refusals {
		if now.Sub(entry.at) >= refusalTTL {
			delete(g.refusals, id)
		}
	}
	for len(g.refusals) >= refusalCacheCap {
		oldestID := ""
		var oldestAt time.Time
		for id, entry := range g.refusals {
			if oldestID == "" || entry.at.Before(oldestAt) {
				oldestID, oldestAt = id, entry.at
			}
		}
		if oldestID == "" {
			return
		}
		delete(g.refusals, oldestID)
	}
}
