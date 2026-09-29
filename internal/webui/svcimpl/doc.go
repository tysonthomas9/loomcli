// Package svcimpl constructs loom's service layer implementations.
//
// The handlers in internal/webui/handlers are meant to depend on the
// interfaces declared in internal/webui/service — AgentService, DiffService,
// FileService, SessionService — rather than on the daemon, backend, store, or
// notify packages directly. This package is where those interfaces are bound
// to real collaborators, so that dependency is concentrated in one place.
//
// That boundary is a convention, not a guarantee: the depguard rule intended
// to enforce it does not currently match the handler tree, and a number of
// handlers do import storage directly. See docs/arch/module-layering.md for
// the details before assuming the rule protects you.
//
// WorkspaceJobStore tracks in-flight workspace jobs for handlers that start
// long-running work and report on it later.
package svcimpl
