package coordinator

import (
	"testing"
)

func TestNewWorkspaceHandle_Basic(t *testing.T) {
	resources := map[string]any{
		ResourceKeyPool: "pool-value",
	}
	h := NewWorkspaceHandle("ws-1", "/tmp/ws1", resources)

	if got := h.ID(); got != "ws-1" {
		t.Fatalf("ID(): got %q, want %q", got, "ws-1")
	}
	if got := h.Path(); got != "/tmp/ws1" {
		t.Fatalf("Path(): got %q, want %q", got, "/tmp/ws1")
	}
	v, ok := h.Resource(ResourceKeyPool)
	if !ok || v != "pool-value" {
		t.Fatalf("Resource(%q): got (%v, %v), want (%q, true)", ResourceKeyPool, v, ok, "pool-value")
	}
}

func TestWorkspaceHandle_Resource_Exists(t *testing.T) {
	type sentinel struct{ name string }

	resources := map[string]any{
		ResourceKeyPool:       &sentinel{name: "pool"},
		ResourceKeySubscriber: &sentinel{name: "subscriber"},
		ResourceKeyTerminal:   &sentinel{name: "terminal"},
		ResourceKeyFleetStore: &sentinel{name: "fleet"},
	}
	h := NewWorkspaceHandle("ws-1", "/tmp/ws1", resources)

	tests := []struct {
		key      string
		wantName string
	}{
		{ResourceKeyPool, "pool"},
		{ResourceKeySubscriber, "subscriber"},
		{ResourceKeyTerminal, "terminal"},
		{ResourceKeyFleetStore, "fleet"},
	}
	for _, tt := range tests {
		v, ok := h.Resource(tt.key)
		if !ok {
			t.Fatalf("Resource(%q): expected ok=true, got false", tt.key)
		}
		s, sOK := v.(*sentinel)
		if !sOK {
			t.Fatalf("Resource(%q): expected *sentinel, got %T", tt.key, v)
		}
		if s.name != tt.wantName {
			t.Fatalf("Resource(%q): got name %q, want %q", tt.key, s.name, tt.wantName)
		}
	}
}

func TestWorkspaceHandle_Resource_Missing(t *testing.T) {
	h := NewWorkspaceHandle("ws-1", "/tmp/ws1", map[string]any{
		ResourceKeyPool: 42,
	})

	v, ok := h.Resource("nonexistent")
	if ok {
		t.Fatalf("Resource(nonexistent): expected ok=false, got true (value=%v)", v)
	}
	if v != nil {
		t.Fatalf("Resource(nonexistent): expected nil value, got %v", v)
	}
}

func TestWorkspaceHandle_NilSafe(t *testing.T) {
	var h *WorkspaceHandle

	if got := h.ID(); got != "" {
		t.Fatalf("nil.ID(): got %q, want empty string", got)
	}
	if got := h.Path(); got != "" {
		t.Fatalf("nil.Path(): got %q, want empty string", got)
	}
	v, ok := h.Resource("x")
	if ok {
		t.Fatal("nil.Resource(x): expected ok=false, got true")
	}
	if v != nil {
		t.Fatalf("nil.Resource(x): expected nil value, got %v", v)
	}
}

func TestWorkspaceHandle_ImmutableResources(t *testing.T) {
	original := map[string]any{
		"keep":   "original",
		"change": "before",
		"delete": "present",
	}
	h := NewWorkspaceHandle("ws-1", "/tmp/ws1", original)

	// Mutate the original map after construction.
	original["added"] = "new"
	delete(original, "delete")
	original["change"] = "after"

	// Verify handle is unaffected by mutations.
	tests := []struct {
		key    string
		want   any
		wantOK bool
	}{
		{"keep", "original", true},
		{"change", "before", true},
		{"delete", "present", true},
		{"added", nil, false},
	}
	for _, tt := range tests {
		v, ok := h.Resource(tt.key)
		if ok != tt.wantOK {
			t.Fatalf("Resource(%q): ok=%v, want %v", tt.key, ok, tt.wantOK)
		}
		if v != tt.want {
			t.Fatalf("Resource(%q): got %v, want %v", tt.key, v, tt.want)
		}
	}
}

func TestWorkspaceHandle_EmptyResources(t *testing.T) {
	h := NewWorkspaceHandle("ws-1", "/tmp/ws1", map[string]any{})

	v, ok := h.Resource(ResourceKeyPool)
	if ok {
		t.Fatalf("Resource on empty map: expected ok=false, got true (value=%v)", v)
	}
	if v != nil {
		t.Fatalf("Resource on empty map: expected nil value, got %v", v)
	}
}

func TestNewWorkspaceHandle_NilResourcesMap(t *testing.T) {
	h := NewWorkspaceHandle("ws-1", "/tmp/ws1", nil)

	if got := h.ID(); got != "ws-1" {
		t.Fatalf("ID(): got %q, want %q", got, "ws-1")
	}
	if got := h.Path(); got != "/tmp/ws1" {
		t.Fatalf("Path(): got %q, want %q", got, "/tmp/ws1")
	}

	v, ok := h.Resource(ResourceKeyPool)
	if ok {
		t.Fatalf("Resource on nil map: expected ok=false, got true (value=%v)", v)
	}
	if v != nil {
		t.Fatalf("Resource on nil map: expected nil value, got %v", v)
	}
}

func TestResourceKeyConstants(t *testing.T) {
	keys := []struct {
		name  string
		value string
	}{
		{"ResourceKeyPool", ResourceKeyPool},
		{"ResourceKeySubscriber", ResourceKeySubscriber},
		{"ResourceKeyTerminal", ResourceKeyTerminal},
		{"ResourceKeyFleetStore", ResourceKeyFleetStore},
	}

	// Verify all constants are non-empty.
	for _, k := range keys {
		if k.value == "" {
			t.Fatalf("%s is empty", k.name)
		}
	}

	// Verify all constants are distinct.
	seen := make(map[string]string, len(keys))
	for _, k := range keys {
		if prev, dup := seen[k.value]; dup {
			t.Fatalf("%s and %s have the same value %q", prev, k.name, k.value)
		}
		seen[k.value] = k.name
	}
}
