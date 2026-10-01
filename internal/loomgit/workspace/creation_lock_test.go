package workspace

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenCreationsSkipsActiveCreation(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	ctx := context.Background()
	session, err := BeginCloneRequest(ctx, "active", "active", "request", "main", filepath.Join(t.TempDir(), "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	recoveries, err := OpenCreations(ctx)
	if err != nil || len(recoveries) != 0 {
		t.Fatalf("active creation recovered: %d, %v", len(recoveries), err)
	}
	if err := session.PlanClones(ctx, []Source{{Name: "repo", Path: filepath.Join(t.TempDir(), "repo")}}); err != nil {
		t.Fatalf("active journal lost ownership: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	recoveries, err = OpenCreations(ctx)
	if err != nil || len(recoveries) != 1 {
		t.Fatalf("interrupted creation recoveries = %d, %v", len(recoveries), err)
	}
	if err := recoveries[0].Close(); err != nil {
		t.Fatal(err)
	}
}
