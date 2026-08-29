package supervisor

import (
	"sort"
	"sync"
	"time"
)

// ProfileDrift is one observed manifest-vs-binary version mismatch that was
// allowed to proceed. The supervisor records it so `loom daemon status` and
// the state file can show "running unverified" without an operator having to
// read the daemon log.
type ProfileDrift struct {
	Dir      string    `json:"dir"`
	Binary   string    `json:"binary"`
	Manifest string    `json:"manifest_version"` // version the manifest pins
	Observed string    `json:"observed_version"` // version the binary reports
	FirstAt  time.Time `json:"first_at"`
	Count    int       `json:"count"` // spawns that proceeded under this drift
}

// Package-level, guarded by a mutex, matching the harnessVersionMu /
// harnessVersionCache idiom in spawn.go: the drift is a property of the host's
// harness binaries against the workspace's profiles, not of any one Supervisor
// instance, and the same check runs from `loom lead`, which has no Supervisor
// at all.
var (
	profileDriftMu sync.Mutex
	profileDrifts  = map[string]ProfileDrift{} // keyed by profile dir
)

// recordProfileDrift records the drift and reports whether this is the first
// observation of this (dir, manifest->observed) triple, so the WARN is logged
// once per drift rather than once per spawn. Twelve agents in a restart storm
// must produce one warning line, not one per boot.
//
// A drift whose versions changed is a NEW observation: the operator needs to
// see the second upgrade too, and its count starts over so the number always
// describes the drift the line names.
func recordProfileDrift(dir, binary, manifest, observed string) (first bool) {
	profileDriftMu.Lock()
	defer profileDriftMu.Unlock()

	if d, ok := profileDrifts[dir]; ok && d.Manifest == manifest && d.Observed == observed {
		d.Count++
		profileDrifts[dir] = d
		return false
	}
	profileDrifts[dir] = ProfileDrift{
		Dir:      dir,
		Binary:   binary,
		Manifest: manifest,
		Observed: observed,
		FirstAt:  time.Now(),
		Count:    1,
	}
	return true
}

// clearProfileDrift drops dir's recorded drift. Called whenever a verification
// for dir SUCCEEDS: after `loom doctor --fix` re-blesses the pin, the recorded
// drift describes a condition that no longer exists, and a status line that
// outlives its condition is worse than no line at all.
func clearProfileDrift(dir string) {
	profileDriftMu.Lock()
	delete(profileDrifts, dir)
	profileDriftMu.Unlock()
}

// ProfileDrifts returns a snapshot of the recorded drifts, newest first.
func ProfileDrifts() []ProfileDrift {
	profileDriftMu.Lock()
	out := make([]ProfileDrift, 0, len(profileDrifts))
	for _, d := range profileDrifts {
		out = append(out, d)
	}
	profileDriftMu.Unlock()

	sort.Slice(out, func(i, j int) bool {
		if !out[i].FirstAt.Equal(out[j].FirstAt) {
			return out[i].FirstAt.After(out[j].FirstAt)
		}
		return out[i].Dir < out[j].Dir // stable for drifts recorded in the same instant
	})
	return out
}

// ResetProfileDrifts drops the record. For testing only.
func ResetProfileDrifts() {
	profileDriftMu.Lock()
	profileDrifts = map[string]ProfileDrift{}
	profileDriftMu.Unlock()
}
