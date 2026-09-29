// Package workspacemgr supplies the store-backed workspace mutations the
// service layer depends on.
//
// BuildStoreBackedCreateWorkspace and BuildStoreBackedAddRepos return the
// service.WorkspaceCreateFn and service.WorkspaceAddReposFn implementations
// bound to a real store. The service layer declares these as function types so
// it does not depend on storage directly; this package is where the server
// process supplies them.
package workspacemgr
