package agentmetrics

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	// maxSeries bounds label cardinality. Past it, new label combinations fold
	// into one catch-all series rather than growing the registry without limit.
	maxSeries = 500
	// otherLabel is that catch-all.
	otherLabel = "_other_"
	// unknownPhase is what an empty phase becomes: a label value must not be
	// empty for a dimension the dashboards group by.
	unknownPhase = "unknown"
	// maxInitialBytes caps the very first read. A workspace that has been
	// running for months can hold a large index; blocking a scrape to walk all
	// of it is worse than starting the counters at the current end of file.
	maxInitialBytes = 64 << 20
	// recentIDCapacity is the size of the duplicate-suppression window. Its
	// only documented job is absorbing a retried finalize writing a second
	// terminal row for the same session.
	recentIDCapacity = 10000
)

// durationBuckets covers 10s to ~1.4h. prometheus.DefBuckets tops out at 10s,
// which would put every real agent session in +Inf.
var durationBuckets = prometheus.ExponentialBuckets(10, 2, 10)

type sessionKey struct {
	role   string
	phase  string
	status string
}

type histKey struct {
	role  string
	phase string
}

// bucketState is one histogram's accumulated state. counts[i] is the
// (non-cumulative) number of observations that fell in durationBuckets[i];
// observations above the last bound land in overflow.
type bucketState struct {
	count    uint64
	sum      float64
	counts   []uint64
	overflow uint64
}

func (b *bucketState) observe(v float64) {
	b.count++
	b.sum += v
	idx := sort.SearchFloat64s(durationBuckets, v)
	// SearchFloat64s finds the first bound >= v; Prometheus buckets are
	// "less than or equal", so that index is the right one.
	if idx >= len(durationBuckets) {
		b.overflow++
		return
	}
	b.counts[idx]++
}

// cumulative renders the state in the le-keyed cumulative form
// MustNewConstHistogram expects.
func (b *bucketState) cumulative() map[float64]uint64 {
	out := make(map[float64]uint64, len(durationBuckets))
	var running uint64
	for i, bound := range durationBuckets {
		running += b.counts[i]
		out[bound] = running
	}
	return out
}

// sessionRecord is the subset of internal/sessions.SessionRecord this package
// reads. It is declared locally, and deliberately: the collector must not pull
// the sessions package (and its store, locking and runtime context) into the
// serve binary's metrics path, and a tolerant local struct also survives the
// field being added by a later change — `role` is written by the daemon only
// from PUPPET-252 onward, which is why AgentName is the documented fallback.
type sessionRecord struct {
	SessionID string     `json:"session_id"`
	AgentName string     `json:"agent_name"`
	Role      string     `json:"role"`
	Phase     string     `json:"phase"`
	Status    string     `json:"status"`
	DurationS float64    `json:"duration_s"`
	EndedAt   *time.Time `json:"ended_at"`
}

// sessionTailer accumulates session counters by incrementally reading
// sessions/index.jsonl. Sessions are finalized in more than one process, so no
// snapshot file can own them; the index is the one artifact every finalizer
// appends to.
type sessionTailer struct {
	path   string
	offset int64
	info   os.FileInfo // previous stat, for rotation detection via os.SameFile

	counts map[sessionKey]uint64
	hist   map[histKey]*bucketState
	seen   *recentIDSet

	budgetWarned bool
	cardWarned   bool
}

func newSessionTailer(path string) *sessionTailer {
	return &sessionTailer{
		path:   path,
		counts: make(map[sessionKey]uint64),
		hist:   make(map[histKey]*bucketState),
		seen:   newRecentIDSet(recentIDCapacity),
	}
}

// reset drops every accumulated series. Called when the file is replaced or
// truncated — `loom cleanup` compacts index.jsonl — because the counts then
// describe rows that are no longer in the file.
func (t *sessionTailer) reset() {
	t.offset = 0
	t.counts = make(map[sessionKey]uint64)
	t.hist = make(map[histKey]*bucketState)
	t.seen = newRecentIDSet(recentIDCapacity)
}

type logFunc func(msg string, args ...any)

// advance reads whatever has been appended since the last call and folds it
// into the counters. Every failure is soft: a scrape reports what it has.
func (t *sessionTailer) advance(logf logFunc) {
	info, err := os.Stat(t.path)
	if err != nil {
		if !os.IsNotExist(err) {
			logf("agentmetrics: cannot stat session index", "path", t.path, "error", err)
		}
		return
	}

	// Shrunk, or a different file at the same path: the offset is meaningless
	// and the accumulated counts describe rows that are gone.
	if info.Size() < t.offset || (t.info != nil && !os.SameFile(t.info, info)) {
		t.reset()
	}
	t.info = info

	if t.offset == 0 && info.Size() > maxInitialBytes {
		// Start from the end rather than blocking the scrape on a huge file.
		t.offset = info.Size()
		if !t.budgetWarned {
			t.budgetWarned = true
			logf("agentmetrics: session index exceeds the initial read budget; counting from end of file",
				"path", t.path, "size", info.Size(), "budget", int64(maxInitialBytes))
		}
		return
	}
	if info.Size() == t.offset {
		return
	}

	consumed := t.readAppended(logf)
	for _, line := range bytes.Split(consumed, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec sessionRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		t.admit(rec, logf)
	}
}

// readAppended returns the whole lines appended since the last call and moves
// the offset past exactly those bytes. A trailing partial line is a concurrent
// append caught mid-write; leaving the offset short of it means it is read
// once, later, in full.
func (t *sessionTailer) readAppended(logf logFunc) []byte {
	f, err := os.Open(t.path) //nolint:gosec // path is derived from the workspace runtime dir by New
	if err != nil {
		logf("agentmetrics: cannot open session index", "path", t.path, "error", err)
		return nil
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		logf("agentmetrics: cannot seek session index", "path", t.path, "error", err)
		return nil
	}
	data, err := io.ReadAll(f)
	if err != nil {
		logf("agentmetrics: cannot read session index", "path", t.path, "error", err)
		return nil
	}

	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		return nil
	}
	consumed := data[:end+1]
	t.offset += int64(len(consumed))
	return consumed
}

// admit applies the three-step rule that keeps these counters monotonic:
// filter, then dedupe, then count.
func (t *sessionTailer) admit(rec sessionRecord, logf logFunc) {
	// 1. Filter. CreateSession appends a status:"running" row and Finalize
	//    appends the terminal row, so every session appears twice. The
	//    whole-file "keep the last record" rule the query path uses is
	//    unimplementable incrementally — a counter already incremented for the
	//    running row cannot be retracted — so the running row is never counted.
	//    That also removes "no EndedAt" from the histogram's input.
	if rec.EndedAt == nil {
		return
	}

	// 2. Dedupe: a retried finalize can write a second terminal row.
	if rec.SessionID != "" && !t.seen.add(rec.SessionID) {
		return
	}

	// 3. Count.
	role := rec.Role
	if role == "" {
		role = rec.AgentName
	}
	phase := rec.Phase
	if phase == "" {
		phase = unknownPhase
	}
	key := sessionKey{role: role, phase: phase, status: rec.Status}
	hk := histKey{role: role, phase: phase}

	if _, ok := t.counts[key]; !ok && len(t.counts) >= maxSeries {
		if !t.cardWarned {
			t.cardWarned = true
			logf("agentmetrics: session label cardinality cap reached; folding new series into "+otherLabel,
				"cap", maxSeries)
		}
		key = sessionKey{role: otherLabel, phase: otherLabel, status: otherLabel}
		hk = histKey{role: otherLabel, phase: otherLabel}
	}

	t.counts[key]++
	b, ok := t.hist[hk]
	if !ok {
		b = &bucketState{counts: make([]uint64, len(durationBuckets))}
		t.hist[hk] = b
	}
	b.observe(rec.DurationS)
}

// emit sends the accumulated session series. Called with the collector's lock
// held, immediately after advance.
func (t *sessionTailer) emit(ch chan<- prometheus.Metric) {
	for k, v := range t.counts {
		ch <- prometheus.MustNewConstMetric(
			sessionsDesc, prometheus.CounterValue, float64(v),
			k.role, k.phase, k.status,
		)
	}
	for k, b := range t.hist {
		ch <- prometheus.MustNewConstHistogram(
			sessionDurationDesc, b.count, b.sum, b.cumulative(),
			k.role, k.phase,
		)
	}
}

// recentIDSet is a bounded FIFO of recently seen session IDs.
type recentIDSet struct {
	capacity int
	members  map[string]struct{}
	order    []string
	next     int
}

func newRecentIDSet(capacity int) *recentIDSet {
	return &recentIDSet{
		capacity: capacity,
		members:  make(map[string]struct{}),
		order:    make([]string, 0, capacity),
	}
}

// add records id and reports whether it was new.
func (s *recentIDSet) add(id string) bool {
	if _, ok := s.members[id]; ok {
		return false
	}
	s.members[id] = struct{}{}
	if len(s.order) < s.capacity {
		s.order = append(s.order, id)
		return true
	}
	delete(s.members, s.order[s.next])
	s.order[s.next] = id
	s.next = (s.next + 1) % s.capacity
	return true
}
