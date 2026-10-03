package loomstore

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

// MCS3: custom model ids are kept per workspace and harness, in order, and
// a set replaces them.
func TestCustomModels(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.CustomModels(ctx, "ws", "opencode"); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("none = %#v, %v", got, err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.SetCustomModels(ctx, "ws", "opencode", []string{"b/z", "a/y"}))
	must(s.SetCustomModels(ctx, "ws", "codex", []string{"gpt-x"}))
	must(s.SetCustomModels(ctx, "other", "opencode", []string{"c/w"}))
	if got, _ := s.CustomModels(ctx, "ws", "opencode"); !reflect.DeepEqual(got, []string{"b/z", "a/y"}) {
		t.Fatalf("ws/opencode = %v", got)
	}
	must(s.SetCustomModels(ctx, "ws", "opencode", []string{"a/y"}))
	if got, _ := s.CustomModels(ctx, "ws", "opencode"); !reflect.DeepEqual(got, []string{"a/y"}) {
		t.Fatalf("after replace = %v", got)
	}
	if got, _ := s.CustomModels(ctx, "ws", "codex"); !reflect.DeepEqual(got, []string{"gpt-x"}) {
		t.Fatalf("ws/codex = %v", got)
	}
}
