//go:build !unix

package leadcontrol

import (
	"context"
	"os/exec"
)

func startInOwnProcessGroup(*exec.Cmd) {}

func killProcessGroup(int) error { return nil }

func cancelOnHangup(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(ctx)
}

// ReapOrphanedCodexAppServers is a no-op off unix.
func ReapOrphanedCodexAppServers() []int { return nil }
