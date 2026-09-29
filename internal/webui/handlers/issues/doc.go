// Package issues serves the issue CRUD, comment, dependency, and claim
// endpoints.
//
// Every handler is built over service.IssueService, so the same routes serve
// whichever backend a workspace is configured for — local, fleet, or the
// daemon's IPC socket — without the handlers knowing which.
//
// Claiming is a mutation, not a read: HandleClaimIssue locks the issue as
// claimed, which is what prevents two agents from starting the same task. The
// claim call itself records no actor. MaxExcludeStatuses bounds the status filter a client
// may send, so a hostile or careless query cannot expand without limit.
package issues
