package supervisor

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"time"
)

// dumpStackBufferSize is the buffer size for runtime.Stack(all=true). 1 MiB
// fits stacks for a daemon with hundreds of agents; anything larger gets
// truncated at the runtime boundary (preferable to OOMing the daemon while
// it's already in trouble).
const dumpStackBufferSize = 1 << 20

// DumpGoroutines writes a full goroutine stack trace (runtime.Stack with
// all=true) to w. Used by the SIGUSR1 handler for on-demand diagnostics and by
// the liveness watchdog before signaling fatal.
func DumpGoroutines(w io.Writer) (int, error) {
	buf := make([]byte, dumpStackBufferSize)
	n := runtime.Stack(buf, true)
	header := fmt.Sprintf("=== goroutine dump at %s (n=%d) ===\n",
		time.Now().Format(time.RFC3339Nano), runtime.NumGoroutine())
	if _, err := io.WriteString(w, header); err != nil {
		return 0, err
	}
	written, err := w.Write(buf[:n])
	if err != nil {
		return written, err
	}
	if _, err := io.WriteString(w, "\n=== end goroutine dump ===\n"); err != nil {
		return written, err
	}
	return written, nil
}

// DumpGoroutinesToLog writes a goroutine dump to stderr (so it lands in the
// daemon log file when stderr is redirected there) and also logs an "stuck
// daemon detected" record so users grepping for "FATAL" or "goroutine_dump"
// can find it.
func DumpGoroutinesToLog(reason string) {
	slog.Error("dumping all goroutines", "reason", reason, "num_goroutines", runtime.NumGoroutine())
	if _, err := DumpGoroutines(os.Stderr); err != nil {
		slog.Error("failed to write goroutine dump", "err", err)
	}
}

// goroutineDumpMinInterval is the minimum spacing between AUTOMATIC goroutine
// dumps. A dump is up to dumpStackBufferSize (1 MiB) of stderr, and the
// liveness watchdog can ask for one on every 10s scan, so unthrottled it
// floods the daemon log with near-identical stacks.
const goroutineDumpMinInterval = 5 * time.Minute

// dumpNow is time.Now, indirected so tests can drive the dump throttle without
// real-time waits. Production never reassigns it.
var dumpNow = time.Now

var (
	dumpThrottleMu sync.Mutex
	lastAutoDump   time.Time
)

// DumpGoroutinesToLogThrottled is DumpGoroutinesToLog rate-limited to one dump
// per goroutineDumpMinInterval, for callers that fire automatically. It reports
// whether the dump was written; a suppressed call logs the reason and the time
// until the next dump is allowed, so nothing is lost silently.
//
// The on-demand paths (SIGUSR1) deliberately keep calling DumpGoroutinesToLog:
// an operator who asks for a dump must always get one.
func DumpGoroutinesToLogThrottled(reason string) bool {
	now := dumpNow()

	dumpThrottleMu.Lock()
	if !lastAutoDump.IsZero() && now.Sub(lastAutoDump) < goroutineDumpMinInterval {
		wait := goroutineDumpMinInterval - now.Sub(lastAutoDump)
		dumpThrottleMu.Unlock()
		slog.Warn("goroutine dump suppressed by throttle",
			"reason", reason,
			"min_interval", goroutineDumpMinInterval,
			"next_dump_in", wait.Truncate(time.Second))
		return false
	}
	lastAutoDump = now
	dumpThrottleMu.Unlock()

	DumpGoroutinesToLog(reason)
	return true
}

// resetGoroutineDumpThrottleForTest clears the automatic-dump throttle so tests
// do not inherit each other's throttle state.
func resetGoroutineDumpThrottleForTest() {
	dumpThrottleMu.Lock()
	lastAutoDump = time.Time{}
	dumpThrottleMu.Unlock()
}
