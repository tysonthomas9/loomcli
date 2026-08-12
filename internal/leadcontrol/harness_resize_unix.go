//go:build unix

package leadcontrol

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

func subscribeHarnessResizeSignals() harnessResizeSignalSubscription {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGWINCH)
	var once sync.Once
	return harnessResizeSignalSubscription{
		signals: signals,
		stop: func() {
			once.Do(func() { signal.Stop(signals) })
		},
	}
}
