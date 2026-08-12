//go:build darwin

package lockfile

import (
	"os"
	"os/exec"
	"strconv"
	"testing"
)

func TestMappedExecutableMatchesCurrentProcess(t *testing.T) {
	t.Parallel()

	output, err := exec.Command( //nolint:norawexec,gosec // fixed binary; pid is an integer
		"/usr/sbin/lsof",
		"-a",
		"-p",
		strconv.Itoa(os.Getpid()),
		"-d",
		"txt",
		"-Fn",
	).Output()
	if err != nil {
		t.Fatalf("inspect current process executable: %v", err)
	}
	if !mappedExecutableMatchesCurrent(output) {
		t.Fatalf("current executable was not found in lsof mappings:\n%s", output)
	}
}
