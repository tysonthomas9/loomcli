package app

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func testWorkspaceResolver(allowed ...string) middleware.WorkspaceResolveFn {
	return func(_ context.Context, id string) (middleware.WorkspaceRef, bool) {
		if len(allowed) == 0 {
			return middleware.WorkspaceRef{RequestedID: id, CanonicalID: id}, true
		}
		for _, candidate := range allowed {
			if id == candidate {
				return middleware.WorkspaceRef{RequestedID: id, CanonicalID: id}, true
			}
		}
		return middleware.WorkspaceRef{}, false
	}
}
