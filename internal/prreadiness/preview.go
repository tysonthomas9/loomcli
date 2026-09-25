package prreadiness

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// Prefix position labels for PreviewMember.Position.
const (
	PositionMerged   = "merged"
	PositionInPrefix = "in_prefix"
	PositionStop     = "stop"
	PositionAfter    = "after_stop"
)

// PreviewMember is one ordered member of a read-only merge preview.
type PreviewMember struct {
	Index int  `json:"index"`
	View  View `json:"readiness"`
	// Position: merged | in_prefix | stop | after_stop.
	Position string `json:"position"`
	// Reasons explains a stop (the member's own reasons, or an edge rule).
	Reasons []string `json:"reasons"`
}

// PreviewStop names the member that ends the ready prefix and why.
type PreviewStop struct {
	PRKey   string   `json:"pr_key"`
	Verdict Verdict  `json:"verdict"`
	Reasons []string `json:"reasons"`
}

// Preview is the read-only ordered ready prefix for a list of PRs (index 0
// lands first). It performs and authorizes nothing; Fingerprint and
// ExpiresAt let a future merge step (STACKED-PRS-19) reject stale previews.
type Preview struct {
	Members    []PreviewMember `json:"members"`
	ReadyCount int             `json:"ready_count"`
	StoppedBy  *PreviewStop    `json:"stopped_by,omitempty"`
	// Fingerprint hashes the ordered keys and every prefix member's
	// snapshot fingerprint.
	Fingerprint string `json:"fingerprint"`
	// ExpiresAt is when the oldest prefix snapshot stops being fresh; nil
	// when the prefix is empty.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// BuildPreview computes the ready prefix over ordered views: merged members
// are skipped (they have landed), then members join while their current verdict is
// ready (fresh) and the edge rule allows landing them after their
// predecessor. The first failing member stops the prefix.
//
// Edge rule, applied between m and EVERY earlier unmerged prefix member p
// (not just the adjacent one: a PR from another repository between a
// stacked pair must not hide the pair's dependency):
//   - different repositories: included (execution must still re-read m);
//   - same repo, m based on p's head branch (Loom lineage): stop,
//     waiting(predecessor_retarget) — merging p retargets m;
//   - same repo, p based on m's head branch: stop, blocked(order_conflict);
//   - same repo and same base branch: stop, waiting(base_will_move) — the
//     base moves and strict required checks may go out of date
//     (STACKED-PRS-18 R5, fail closed while strictness is unread);
//   - same repo, unrelated bases: included.
func BuildPreview(views []View) Preview {
	p := Preview{Members: make([]PreviewMember, 0, len(views))}
	var prefix []View
	stopped := false
	var oldest time.Time
	fp := sha256.New()
	for i := range views {
		v := views[i]
		m := PreviewMember{Index: i, View: v, Reasons: []string{}}
		fp.Write([]byte(v.PRKey + "\n"))
		switch {
		case stopped:
			m.Position = PositionAfter
			m.Reasons = []string{ReasonAfterStop}
		case v.CurrentVerdict == VerdictMerged:
			m.Position = PositionMerged
		default:
			verdict, reasons := v.CurrentVerdict, v.CurrentReasons
			if verdict == VerdictReady {
				verdict, reasons = prefixEdge(prefix, v)
			}
			if verdict != VerdictReady {
				stopped = true
				m.Position = PositionStop
				m.Reasons = append([]string{}, reasons...)
				p.StoppedBy = &PreviewStop{PRKey: v.PRKey, Verdict: verdict, Reasons: m.Reasons}
				break
			}
			m.Position = PositionInPrefix
			p.ReadyCount++
			fp.Write([]byte("=" + v.Snapshot.Fingerprint + "\n"))
			if oldest.IsZero() || v.Snapshot.ObservedAt.Before(oldest) {
				oldest = v.Snapshot.ObservedAt
			}
			prefix = append(prefix, v)
		}
		p.Members = append(p.Members, m)
	}
	p.Fingerprint = hex.EncodeToString(fp.Sum(nil))
	if !oldest.IsZero() {
		expires := oldest.Add(FreshFor)
		p.ExpiresAt = &expires
	}
	return p
}

// edgeRanks orders edge-rule stops; the strongest reason wins.
var edgeRanks = map[string]int{
	ReasonOrderConflict:       4,
	ReasonPredecessorRetarget: 3,
	ReasonBaseWillMove:        2,
	reasonRefsUnknown:         1,
}

const reasonRefsUnknown = "refs_unknown"

// prefixEdge applies edgeRule between m and every earlier prefix member and
// returns the strongest stop, or ready when every edge allows m.
func prefixEdge(prefix []View, m View) (Verdict, []string) {
	verdict, reasons, rank := VerdictReady, []string(nil), 0
	for _, p := range prefix {
		v, r := edgeRule(p, m)
		if v != VerdictReady && edgeRanks[r[0]] > rank {
			verdict, reasons, rank = v, r, edgeRanks[r[0]]
		}
	}
	return verdict, reasons
}

// edgeRule decides whether m may land in the same prefix as its unmerged
// predecessor p. Both are ready, so both carry a snapshot.
func edgeRule(p, m View) (Verdict, []string) {
	ps, ms := p.Snapshot, m.Snapshot
	if repoOf(p.PRKey) != repoOf(m.PRKey) {
		return VerdictReady, nil
	}
	switch {
	case ps.HeadRef == "" || ms.HeadRef == "" || ps.BaseRef == "" || ms.BaseRef == "":
		return VerdictUnknown, []string{reasonRefsUnknown}
	case ms.BaseRef == ps.HeadRef:
		return VerdictWaiting, []string{ReasonPredecessorRetarget}
	case ps.BaseRef == ms.HeadRef:
		return VerdictBlocked, []string{ReasonOrderConflict}
	case ms.BaseRef == ps.BaseRef:
		return VerdictWaiting, []string{ReasonBaseWillMove}
	default:
		return VerdictReady, nil
	}
}

// repoOf returns the "provider:owner/repo" part of a PR key.
func repoOf(prKey string) string {
	repo, _, _ := strings.Cut(prKey, "#")
	return repo
}
