package svcimpl

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// TermConfigPoolAdapter wraps daemon.Pool to implement terminal.ConfigConnectionGetter.
type TermConfigPoolAdapter struct {
	Pool daemon.Pool
}

func (a *TermConfigPoolAdapter) Get(ctx context.Context) (terminal.ConfigClient, error) {
	return a.Pool.Get(ctx)
}

func (a *TermConfigPoolAdapter) Put(client terminal.ConfigClient) {
	if c, ok := client.(*rpc.Client); ok {
		a.Pool.Put(c)
	}
}

func (a *TermConfigPoolAdapter) Discard(client terminal.ConfigClient) {
	if c, ok := client.(*rpc.Client); ok {
		a.Pool.Discard(c)
	}
}
