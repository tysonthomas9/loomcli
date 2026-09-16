// Package workspace normalizes workspace identifiers.
//
// ResolveWorkspaceID accepts the forms a workspace may be named by and returns
// the canonical identifier; ShortWorkspaceID returns the abbreviated form used
// in display and in paths that must stay short. Both live in a small leaf
// package so every layer abbreviates and canonicalizes identically.
package workspace
