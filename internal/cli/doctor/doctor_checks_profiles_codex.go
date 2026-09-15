package doctor

// The cross-profile half of the codex identity check. checkProfileCredential
// answers "does this root have a login?" one root at a time; sharing is a
// property of a PAIR of roots, so it needs a pass that sees all of them.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/agentprofile"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

// errProfileCodexAuthShared is its own sentinel rather than a reuse of
// supervisor.ErrProfileCodexAuthMissing: the fault is different (the login
// exists and works — it just is not this profile's) and, more practically,
// faultReason and faultRepair branch on the sentinel, so sharing it would
// print the wrong repair line for one of the two faults.
//
// It lives in doctor, not supervisor, because no boot path may ever refuse on
// it: each of two roots sharing a credential is individually valid, and
// refusing on a NEIGHBOUR's file would let one bad profile ground another
// agent. Only the report catches this.
var errProfileCodexAuthShared = errors.New("codex credential shared")

// codexAuthIdentity is one login file that was successfully read, kept with a
// label for the report. profile is nil for the operator's own ~/.codex, which
// participates in the comparison but is never itself reported as a fault —
// it is the source the copies were made FROM, and it is not loom's to repair.
type codexAuthIdentity struct {
	profile     *agentprofile.Profile
	label       string
	fingerprint string
}

// codexAuthSharingFaults reports every codex profile whose refresh_token is
// byte-identical to another root's.
//
// Why refresh_token and never account_id: two independent `codex login`s
// against the same ChatGPT account produce the SAME account_id and DIFFERENT
// refresh tokens, and that is precisely the healthy end state this ticket is
// driving the fleet towards. Bucketing on account_id would make `loom doctor`
// fail permanently on a correctly provisioned fleet — verified on this machine,
// where ~/.codex and lead's codex root share an account_id by design.
//
// A shared refresh_token is the opposite: it can only come from copying
// auth.json. codex rotates that token as it refreshes, so the first root to
// refresh invalidates the copy and the other one starts 401ing with
// refresh_token_reused on a schedule nobody controls.
//
// Profiles whose login cannot be read at all are skipped in silence:
// checkProfileCredential already reported each of those with its own repair,
// and reporting the same root twice in one report is noise, not rigor.
func codexAuthSharingFaults(profiles []agentprofile.Profile) []profileFault {
	identities := collectCodexIdentities(profiles)
	if len(identities) < 2 {
		return nil
	}

	buckets := make(map[string][]codexAuthIdentity, len(identities))
	for _, id := range identities {
		buckets[id.fingerprint] = append(buckets[id.fingerprint], id)
	}

	var faults []profileFault
	for _, bucket := range buckets {
		if len(bucket) < 2 {
			continue
		}
		for _, id := range bucket {
			if id.profile == nil {
				continue // the operator's own file: a peer, never a fault
			}
			faults = append(faults, profileFault{
				profile: *id.profile,
				err: fmt.Errorf("%w: codex credential is shared with %s (identical refresh_token) — "+
					"one of these will 401 with refresh_token_reused as soon as both refresh",
					errProfileCodexAuthShared, strings.Join(peerLabels(bucket, id), ", ")),
			})
		}
	}
	// Bucket iteration is map order; sort so one fleet always produces one
	// report. faultLines sorts for display, but broken is also counted and
	// concatenated with other buckets before it gets there.
	sort.Slice(faults, func(i, j int) bool {
		if faults[i].profile.Agent != faults[j].profile.Agent {
			return faults[i].profile.Agent < faults[j].profile.Agent
		}
		return faults[i].profile.Dir < faults[j].profile.Dir
	})
	return faults
}

// collectCodexIdentities reads every readable codex login in the fleet, plus
// the operator's own ~/.codex/auth.json when it exists — that file is the
// source a copied credential was copied from, so leaving it out would leave
// the most likely sharing invisible.
func collectCodexIdentities(profiles []agentprofile.Profile) []codexAuthIdentity {
	var out []codexAuthIdentity
	seen := make(map[string]bool, len(profiles)+1)
	for i := range profiles {
		p := profiles[i]
		path := supervisor.ProfileAuthPath(p.Dir, p.Harness)
		if path == "" {
			continue // this harness owns no login file
		}
		_, fingerprint, err := supervisor.ProfileAuthIdentity(path)
		if err != nil {
			continue // already reported by checkProfileCredential
		}
		seen[path] = true
		out = append(out, codexAuthIdentity{profile: &p, label: displayDir(p.Dir), fingerprint: fingerprint})
	}

	home, err := os.UserHomeDir()
	if err != nil {
		// No home to resolve is not a doctor failure: the fleet's own profiles
		// are still compared against each other.
		return out
	}
	path := filepath.Join(home, ".codex", "auth.json")
	if seen[path] {
		return out // an operator who pointed a profile at ~/.codex is not sharing with themselves
	}
	if _, fingerprint, err := supervisor.ProfileAuthIdentity(path); err == nil {
		out = append(out, codexAuthIdentity{label: "~/.codex", fingerprint: fingerprint})
	}
	return out
}

// peerLabels names the other members of a bucket, so the report says which
// roots to look at rather than only that some sharing exists.
func peerLabels(bucket []codexAuthIdentity, self codexAuthIdentity) []string {
	var out []string
	for _, id := range bucket {
		if id.label == self.label {
			continue
		}
		out = append(out, id.label)
	}
	sort.Strings(out)
	return out
}
