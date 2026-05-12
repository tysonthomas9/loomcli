package runtimectx

import (
	"context"
	"testing"
)

func TestRootContext_DefaultBackground(t *testing.T) {
	// When SetRootContext hasn't been called (or has only been called with
	// nil), RootContext returns context.Background-equivalent (non-nil,
	// no deadline).
	if got := RootContext(); got == nil {
		t.Fatal("RootContext() returned nil")
	}
}

func TestSetRootContext_Stores(t *testing.T) {
	type ctxKey struct{}
	want := context.WithValue(context.Background(), ctxKey{}, "marker")
	SetRootContext(want)
	got := RootContext()
	if v := got.Value(ctxKey{}); v != "marker" {
		t.Errorf("SetRootContext value not retained: got value %v", v)
	}
	// Reset so other tests in this package start clean.
	t.Cleanup(func() { SetRootContext(context.Background()) })
}

func TestSetRootContext_NilIsNoop(t *testing.T) {
	// A nil arg must not overwrite the existing root context. We pass nil
	// via a typed variable so staticcheck's SA1012 (no nil context
	// literal) doesn't fire — we're deliberately probing the guard.
	before := RootContext()
	var nilCtx context.Context
	SetRootContext(nilCtx)
	after := RootContext()
	if before != after {
		t.Error("SetRootContext(nil) replaced the existing context")
	}
}
