package workspace

import "os"

var exitProcess = os.Exit

func testingSetExitProcess(t interface {
	Helper()
	Cleanup(func())
}, fn func(int)) {
	t.Helper()
	old := exitProcess
	exitProcess = fn
	t.Cleanup(func() { exitProcess = old })
}
