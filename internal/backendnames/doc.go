// Package backendnames holds canonical identifiers for agent backends.
//
// The intent is that these strings, which cross process, storage, and API
// boundaries, are spelled in one place rather than as literals at each use
// site. Adoption is partial: only Codex's implementation routes its Name()
// through this package. Claude's Name() still returns a hardcoded literal, and
// Cursor, Gemini, and OpenCode have no constant here at all.
//
// Keeping it a dependency-free leaf package lets any layer name a backend
// without importing the backend implementations.
package backendnames
