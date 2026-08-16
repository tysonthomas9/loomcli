package backendcheck

import (
	"time"

	"github.com/olesho/harness-wrapper/pkg/discovery"
)

// confirmAttempts is one initial probe plus len(confirmDelays) retries.
const confirmAttempts = 4

// confirmDelays are the waits *between* attempts, so len == confirmAttempts-1.
// They sum to 1.7s, deliberately longer than the ~1-2s window an in-place
// symlink swap leaves the binary missing, and far shorter than the 30s recheck
// a false negative would otherwise cost.
var confirmDelays = []time.Duration{200 * time.Millisecond, 500 * time.Millisecond, time.Second}

// ConfirmSleep is the sleep ConfirmBackend uses between attempts. Production
// never reassigns it; tests swap in a no-op so a negative-path case does not
// pay the retry budget in wall time. Mirrors the CheckBackend seam above.
var ConfirmSleep = time.Sleep

// ConfirmBackend reports whether a backend is installed, treating a *single*
// negative lookup as unconfirmed rather than authoritative.
//
// It exists because package managers replace a CLI in place: the claude-code
// auto-updater rewrites ~/.nvm/versions/node/<ver>/bin/claude, and for the
// 1-2s the symlink is being swapped exec.LookPath finds nothing. A caller that
// believes that momentary miss parks the agent, PATCHes the control plane, and
// re-checks 30s later — a flap measured 27 times in one day (PUPPET-54). The
// sleeps below are the whole point of this function; do not remove them.
//
// It returns the final discovery.Info, the number of consecutive misses
// observed before that result (0 when the first probe already said installed,
// confirmAttempts when every probe missed), and the lookup error.
//
// Only long-running supervisory callers should use this. CheckBackend stays
// single-shot and side-effect-free for CLI callers like `loom workspace ops
// diagnose`, which must not sleep. ConfirmBackend deliberately memoizes
// nothing: a cached path would pin the very inode the updater is deleting.
func ConfirmBackend(name string) (discovery.Info, int, error) {
	var info discovery.Info
	for attempt := 0; attempt < confirmAttempts; attempt++ {
		var err error
		info, err = CheckBackend(name)
		if err != nil {
			// A discovery-layer failure (unreadable embedded versions.json) is
			// not a PATH miss. Retrying it would sleep for a condition that
			// cannot resolve on its own, so surface it on first occurrence.
			return info, attempt, err
		}
		if info.Installed {
			return info, attempt, nil
		}
		if attempt < len(confirmDelays) {
			ConfirmSleep(confirmDelays[attempt])
		}
	}
	return info, confirmAttempts, nil
}
