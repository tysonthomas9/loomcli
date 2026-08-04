package sessions

import (
	"os"
	"syscall"
	"testing"
)

func TestMain(m *testing.M) {
	old := syscall.Umask(0)
	code := m.Run()
	syscall.Umask(old)
	os.Exit(code)
}
