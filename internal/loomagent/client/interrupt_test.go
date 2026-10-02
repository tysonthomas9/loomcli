package client

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
)

// TestClientInterrupt: Interrupt is Send with delivery interrupt over the
// real route on a fake harness. Empty text stops the running turn; text is
// handed over first when the turn ends; a retry returns the first result;
// with no turn running it is no_op.
func TestClientInterrupt(t *testing.T) {
	ctx := context.Background()
	srv, fh := newServer(t)
	c := newClient(srv, "ws", "")
	a, err := c.Create(ctx, "c1", lead("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	id := a.AgentID
	eventually(t, "idle", func() bool { got, err := c.Get(ctx, id); return err == nil && got.State == loomagent.StateIdle })
	if r, err := c.Interrupt(ctx, "n1", id, ""); err != nil || r.State != loomagent.StateNoOp || r.Interrupted == nil || *r.Interrupted {
		t.Fatalf("idle Interrupt = %+v, %v; want no_op", r, err)
	}

	ask := func(id string) fake.Turn { return fake.Turn{Steps: []fake.Step{{Delta: id}, {Ask: id}}} }
	fh.Script(id, ask("k1"), ask("k2"))
	if _, err := c.Send(ctx, "s1", id, "hi"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "ask k1", func() bool { got, err := c.Get(ctx, id); return err == nil && len(got.OpenAsks) == 1 })
	stop, err := c.Interrupt(ctx, "st1", id, "")
	if err != nil || stop.Interrupted == nil || !*stop.Interrupted {
		t.Fatalf("Interrupt = %+v, %v; want interrupted", stop, err)
	}
	eventually(t, "turn stopped", func() bool { got, err := c.Get(ctx, id); return err == nil && got.State == loomagent.StateIdle })
	if again, err := c.Interrupt(ctx, "st1", id, ""); err != nil || again.Interrupted == nil || !*again.Interrupted {
		t.Fatalf("Interrupt retry = %+v, %v; want the first result", again, err)
	}

	if _, err := c.Send(ctx, "s2", id, "again"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "ask k2", func() bool {
		got, err := c.Get(ctx, id)
		return err == nil && len(got.OpenAsks) == 1 && got.OpenAsks[0].ID == "k2"
	})
	r, err := c.Interrupt(ctx, "i1", id, "instead")
	if err != nil || r.Interrupted == nil || !*r.Interrupted || r.State != "waiting" {
		t.Fatalf("Interrupt with text = %+v, %v", r, err)
	}
	eventually(t, "instead ran", func() bool {
		got, err := c.Get(ctx, id)
		return err == nil && got.State == loomagent.StateIdle && len(got.WaitingMessages) == 0
	})
}
