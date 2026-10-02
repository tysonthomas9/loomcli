package loomharness

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// stub is a recording fake that proves one type can satisfy the whole port.
// Like the real fake, it ignores Launch apart from echoing Root.
type stub struct {
	ref    NativeRef
	purged []NativeRef
}

func (*stub) Name() string                            { return "stub" }
func (*stub) Models(context.Context) ([]Model, error) { return nil, nil }
func (*stub) Health(context.Context) (Health, error)  { return Health{OK: true}, nil }
func (*stub) Open(_ context.Context, s OpenSpec) (NativeRef, error) {
	return NativeRef{Root: s.Launch.Root, NativeID: "ses_" + s.Key}, nil
}
func (*stub) Session(ref NativeRef) Session      { return &stub{ref: ref} }
func (*stub) Feed(context.Context) (Feed, error) { return nil, ErrUnavailable }
func (s *stub) Purge(_ context.Context, owned []NativeRef) error {
	s.purged = append(s.purged, owned...)
	return nil
}
func (*stub) Restart(context.Context) error { return nil }
func (s *stub) Resume(_ context.Context, l Launch) (NativeRef, error) {
	return NativeRef{Root: l.Root, NativeID: s.ref.NativeID}, nil
}
func (*stub) Prompt(context.Context, Input) error              { return ErrBusy }
func (*stub) Interrupt(context.Context) (bool, error)          { return false, nil }
func (*stub) Reply(context.Context, string, Reply) error       { return nil }
func (*stub) HasInput(context.Context, string) (Landed, error) { return LandedUnknown, nil }
func (*stub) Messages(context.Context, string, int) (MessagePage, error) {
	return MessagePage{}, nil
}
func (*stub) Status(context.Context) (Status, error) { return Status{}, nil }
func (*stub) SetModel(context.Context, string) error { return nil }
func (*stub) Move(context.Context, string) error     { return nil }
func (*stub) Unload(context.Context) error           { return nil }
func (*stub) Close(context.Context) error            { return nil }

var (
	_ Harness = (*stub)(nil)
	_ Session = (*stub)(nil)
)

func TestPortStubSatisfiesInterfaces(t *testing.T) {
	ctx := context.Background()
	var h Harness = &stub{}
	ref, err := h.Open(ctx, OpenSpec{Key: "agt_1", Launch: Launch{Root: "/p/a", Env: map[string]string{"K": "v"}}})
	if err != nil || ref != (NativeRef{Root: "/p/a", NativeID: "ses_agt_1"}) {
		t.Fatalf("Open = %v, %v", ref, err)
	}
	s := h.Session(ref)
	if got, err := s.Resume(ctx, Launch{Root: "/p/a"}); err != nil || got != ref {
		t.Fatalf("Resume = %v, %v; want %v", got, err, ref)
	}
	if got, _ := s.HasInput(ctx, "k"); got != LandedUnknown {
		t.Fatalf("HasInput = %q", got)
	}
	if err := s.Prompt(ctx, Input{Key: "k"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("Prompt err = %v", err)
	}
}

func TestPortPurgeGetsRecordedRefs(t *testing.T) {
	h := &stub{}
	recorded := []NativeRef{{Root: "/p/a", NativeID: "ses_1"}, {Root: "/p/b", NativeID: "ses_2"}}
	if err := h.Purge(context.Background(), recorded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.purged, recorded) {
		t.Fatalf("Purge got %v; want exactly %v", h.purged, recorded)
	}
}
