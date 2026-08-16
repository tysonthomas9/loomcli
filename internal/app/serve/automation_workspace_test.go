package serve

import (
	"context"
	"errors"
	"reflect"
	"testing"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
)

type automationWorkspaceStoreStub struct {
	workspaceowner.WorkspaceStore
	values []*workspaceowner.Workspace
	err    error
}

func (stub *automationWorkspaceStoreStub) List(context.Context) ([]*workspaceowner.Workspace, error) {
	return stub.values, stub.err
}

func TestAutomationWorkspaceListerReturnsSortedCurrentKeys(t *testing.T) {
	stub := &automationWorkspaceStoreStub{values: []*workspaceowner.Workspace{{Key: "ZED"}, {Key: "ALPHA"}}}
	keys, err := newAutomationWorkspaceLister(stub).ListWorkspaceKeys(t.Context())
	if err != nil || !reflect.DeepEqual(keys, []string{"ALPHA", "ZED"}) {
		t.Fatalf("ListWorkspaceKeys = %v, %v", keys, err)
	}
}

func TestAutomationWorkspaceListerFailsClosedOnInvalidState(t *testing.T) {
	for _, values := range [][]*workspaceowner.Workspace{
		{nil},
		{{Key: ""}},
		{{Key: " DUP "}},
		{{Key: "DUP"}, {Key: "DUP"}},
	} {
		_, err := newAutomationWorkspaceLister(&automationWorkspaceStoreStub{values: values}).ListWorkspaceKeys(t.Context())
		if !errors.Is(err, automation.ErrInvalidPersistedState) {
			t.Fatalf("values=%v error=%v, want invalid persisted state", values, err)
		}
	}
	if got := newAutomationWorkspaceLister(nil); got != nil {
		t.Fatalf("nil workspace store lister = %#v", got)
	}
}
