package memstore

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/store/storetest"
)

func TestMemstoreArtifactCreateRetryConformance(t *testing.T) {
	storetest.RunArtifactCreateRetryConformance(t, func(testing.TB) *storetest.ArtifactHarness {
		st := New()
		return &storetest.ArtifactHarness{Workspace: "WS", Artifacts: st.Artifacts()}
	})
}
