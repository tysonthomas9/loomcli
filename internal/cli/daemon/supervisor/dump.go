package supervisor

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
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
