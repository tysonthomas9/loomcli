package handlermux

import (
	"net/http"
	"testing"
)

func TestWorkspaceHandlerReexportsReturnHandlers(t *testing.T) {
	svc := &mockWorkspaceService{}
	handlers := []http.HandlerFunc{
		HandleWorkspaceCreate(svc),
		HandleListWorkspaces(svc),
		HandleGetWorkspace(svc),
		HandleListWorkspaceRepos(svc),
		HandleAddWorkspaceRepos(svc),
		HandleGetWorkspaceJob(svc),
		HandleWorkspaceReorder(svc),
		HandleSetDefaultWorkspace(svc),
		HandleClearDefaultWorkspace(svc),
		HandleWorkspaceDelete(svc),
		HandleWorkspaceRename(svc),
		HandleWorkspaceBackendGet(svc),
		HandleWorkspaceBackendPatch(svc),
		HandleActiveWorkspace(svc),
	}
	for i, h := range handlers {
		if h == nil {
			t.Fatalf("handler %d is nil", i)
		}
	}
}
