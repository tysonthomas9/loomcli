// Package workspace serves workspace selection, membership, and per-workspace
// backend configuration.
//
// Most loom state is workspace-scoped, so these endpoints decide the scope
// every other endpoint then operates in: listing workspaces, reading one,
// setting or clearing the default, and reporting the active one.
//
// Adding repositories can be slow enough to outlive a request, so it is
// modeled as a job — HandleAddWorkspaceRepos starts the work and
// HandleGetWorkspaceJob reports on it, rather than holding a connection open
// while repositories are cloned.
package workspace
