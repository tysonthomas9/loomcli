package cmdstore

import (
	"context"
	"os"
	"os/signal"
	"sync"
)

var (
	signalsMu sync.Mutex
	taken     = map[os.Signal]bool{}
)

// Notify is signal.Notify for a command that handles sigs itself. The CLI
// root's trace-flush handler then leaves those signals to the command instead
// of re-raising them to kill the process mid-shutdown.
func Notify(c chan<- os.Signal, sigs ...os.Signal) {
	signal.Notify(c, sigs...)
	takeSignals(sigs)
}

// NotifyContext is signal.NotifyContext with the same hand-off as Notify.
func NotifyContext(parent context.Context, sigs ...os.Signal) (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(parent, sigs...)
	takeSignals(sigs)
	return ctx, stop
}

// SignalTaken reports whether a command took over sig through Notify or
// NotifyContext.
func SignalTaken(sig os.Signal) bool {
	signalsMu.Lock()
	defer signalsMu.Unlock()
	return taken[sig]
}

func takeSignals(sigs []os.Signal) {
	signalsMu.Lock()
	defer signalsMu.Unlock()
	for _, s := range sigs {
		taken[s] = true
	}
}
