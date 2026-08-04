package coordinator

import (
	"testing"
)

func TestRegistrationContext_ProvideResolve(t *testing.T) {
	ctx := RegistrationContext{WorkspaceID: "ws-1", WorkspacePath: "/tmp/ws1"}

	// Resolve on empty bag returns false.
	_, ok := ctx.Resolve("missing")
	if ok {
		t.Fatal("expected Resolve on empty bag to return false")
	}

	// Provide then Resolve.
	ctx.Provide("key", "value")
	v, ok := ctx.Resolve("key")
	if !ok || v != "value" {
		t.Fatalf("expected (value, true), got (%v, %v)", v, ok)
	}

	// Missing key after Provide.
	_, ok = ctx.Resolve("other")
	if ok {
		t.Fatal("expected false for missing key")
	}
}

func TestRegistrationContext_ProvideOverwrite(t *testing.T) {
	ctx := RegistrationContext{WorkspaceID: "ws-1", WorkspacePath: "/tmp/ws1"}
	ctx.Provide("k", 1)
	ctx.Provide("k", 2)
	v, ok := ctx.Resolve("k")
	if !ok || v != 2 {
		t.Fatalf("expected overwritten value 2, got %v", v)
	}
}

func TestRegistrationContext_ProvideNilValue(t *testing.T) {
	ctx := RegistrationContext{WorkspaceID: "ws-1", WorkspacePath: "/tmp/ws1"}
	ctx.Provide("k", nil)
	v, ok := ctx.Resolve("k")
	if !ok {
		t.Fatal("expected true for nil value")
	}
	if v != nil {
		t.Fatalf("expected nil, got %v", v)
	}
}
