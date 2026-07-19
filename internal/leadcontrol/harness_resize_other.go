//go:build !unix

package leadcontrol

func subscribeHarnessResizeSignals() harnessResizeSignalSubscription {
	return harnessResizeSignalSubscription{stop: func() {}}
}
