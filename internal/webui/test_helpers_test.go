package webui

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// stubPool is a minimal daemon.Pool for handler tests.
type stubPool struct{}

func (s *stubPool) Get(_ context.Context) (*rpc.Client, error) { return &rpc.Client{}, nil }
func (s *stubPool) Put(_ *rpc.Client)                          {}
func (s *stubPool) PutAfterError(_ *rpc.Client)                {}
func (s *stubPool) Discard(_ *rpc.Client)                      {}
func (s *stubPool) Stats() daemon.PoolStats {
	return daemon.PoolStats{Size: 10, Created: 2, Active: 1, Available: 1}
}
func (s *stubPool) Close() error { return nil }
