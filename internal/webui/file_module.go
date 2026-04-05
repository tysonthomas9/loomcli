package webui

import "net/http"

// FileModule registers the 3 workspace-scoped file operation routes on a
// [*http.ServeMux].
//
// The module is only constructed when fileSvc is non-nil.
// All routes are unconditional within this module.
type FileModule struct {
	fileSvc FileService
}

// NewFileModule returns a FileModule that will register routes using the
// given file service.
func NewFileModule(fileSvc FileService) *FileModule {
	return &FileModule{fileSvc: fileSvc}
}

// Register implements [Module] by registering 3 file operation routes.
func (m *FileModule) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/files/tree", handleFileTree(m.fileSvc))
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/files", handleFileRead(m.fileSvc))
	mux.HandleFunc("PUT /api/workspaces/{ws}/agents/{name}/files", handleFileWrite(m.fileSvc))
}
