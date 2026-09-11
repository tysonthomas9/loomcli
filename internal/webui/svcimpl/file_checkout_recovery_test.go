package svcimpl

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

func TestCheckoutRecoveryDistinguishesMissingFromFailedInspection(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "checkout")
	svc := NewFileService(scopedMockFileOps{wsRoot: root, wsData: &ops.WorkspaceData{ID: "ws", Path: root, Repos: []ops.WorkspaceRepo{{Name: "repo", Path: path}}}})
	missing, err := svc.ListFileCheckouts(context.Background(), "ws")
	if err != nil || missing.Partial || len(missing.Checkouts) != 1 || missing.Checkouts[0].Exists {
		t.Fatalf("valid absence: %+v, %v", missing, err)
	}
	if err := os.WriteFile(path, []byte("not a checkout directory"), 0600); err != nil {
		t.Fatal(err)
	}
	invalid, err := svc.ListFileCheckouts(context.Background(), "ws")
	if err != nil {
		t.Fatal(err)
	}
	if !invalid.Partial || len(invalid.Errors) != 1 || !invalid.Checkouts[0].StatusError {
		t.Fatalf("failed inspection acknowledged absence: %+v", invalid)
	}
}

func TestCheckoutRecoveryDoesNotOmitUnsafeDeclaredPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "checkout")
	if err := os.Symlink(t.TempDir(), path); err != nil {
		t.Fatal(err)
	}
	svc := NewFileService(scopedMockFileOps{wsRoot: root, wsData: &ops.WorkspaceData{ID: "ws", Path: root, Repos: []ops.WorkspaceRepo{{Name: "repo", Path: path}}}})
	result, err := svc.ListFileCheckouts(context.Background(), "ws")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Checkouts) != 1 || !result.Partial || !result.Checkouts[0].StatusError || result.Checkouts[0].Exists || len(result.Errors) != 1 {
		t.Fatalf("unsafe declared checkout omitted: %+v", result)
	}
}
