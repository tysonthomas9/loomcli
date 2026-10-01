package loomharness

import (
	"context"
	"errors"
	"testing"
)

// stub proves one type can satisfy the whole port.
type stub struct{}

func (stub) Name() string                            { return "stub" }
func (stub) Models(context.Context) ([]Model, error) { return nil, nil }
func (stub) Health(context.Context) (Health, error)  { return Health{OK: true}, nil }
func (stub) Open(_ context.Context, s OpenSpec) (SessionRef, error) {
	return SessionRef{"stub", s.Key}, nil
}
func (s stub) Session(SessionRef) Session                     { return s }
func (stub) Feed(context.Context) (Feed, error)               { return nil, ErrUnavailable }
func (stub) Purge(context.Context, []SessionRef) error        { return nil }
func (stub) Restart(context.Context) error                    { return nil }
func (stub) Resume(context.Context) error                     { return nil }
func (stub) Prompt(context.Context, Input) error              { return ErrBusy }
func (stub) Interrupt(context.Context) (bool, error)          { return false, nil }
func (stub) Reply(context.Context, string, Reply) error       { return nil }
func (stub) HasInput(context.Context, string) (Landed, error) { return LandedUnknown, nil }
func (stub) Messages(context.Context, string, int) (MessagePage, error) {
	return MessagePage{}, nil
}
func (stub) Status(context.Context) (Status, error) { return Status{}, nil }
func (stub) SetModel(context.Context, string) error { return nil }
func (stub) Move(context.Context, string) error     { return nil }
func (stub) Unload(context.Context) error           { return nil }
func (stub) Close(context.Context) error            { return nil }

var (
	_ Harness = stub{}
	_ Session = stub{}
)

func TestPortStubSatisfiesInterfaces(t *testing.T) {
	ctx := context.Background()
	var h Harness = stub{}
	ref, err := h.Open(ctx, OpenSpec{Key: "agt_1"})
	if err != nil || ref.ID != "agt_1" {
		t.Fatalf("Open = %v, %v", ref, err)
	}
	s := h.Session(ref)
	if got, _ := s.HasInput(ctx, "k"); got != LandedUnknown {
		t.Fatalf("HasInput = %q", got)
	}
	if err := s.Prompt(ctx, Input{Key: "k"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("Prompt err = %v", err)
	}
}
