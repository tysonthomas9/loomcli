package memstore

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/store/storetest"
)

func TestMemstoreWorkspaceFileConformance(t *testing.T) {
	storetest.RunWorkspaceFileConformance(t, func(testing.TB) *storetest.WorkspaceFileHarness {
		s := New()
		return &storetest.WorkspaceFileHarness{
			Store:    s.WorkspaceFiles(),
			SetActor: s.SetWorkspaceFileActor,
			Corrupt: func(t testing.TB, workspaceKey, revision, path string, replacement []byte) {
				t.Helper()
				if err := s.files.corrupt(workspaceKey, revision, path, replacement); err != nil {
					t.Fatalf("corrupt workspace file: %v", err)
				}
			},
		}
	})
}
