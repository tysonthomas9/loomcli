// Package subscription connects loom's mutation sources to the realtime hub.
//
// BackendMutationSubscriber watches one issue backend and republishes its
// mutations to the hub for a workspace; MultiWorkspaceSubscriber does the same
// across many, which is what a fleet-mode server needs. Module is the
// assembled unit the server installs, given a hub and a way to fetch mutations
// since a known point — the parameter that lets a reconnecting client catch up
// rather than only receiving what arrives after it connects.
//
// HandleSSEToken issues the short-lived tokens an EventSource authenticates
// with. HandleSSETokenWithActivation additionally records why a stream was
// activated (ActivationReason), so an operator can tell a stream opened by a
// user's browser from one opened by another part of the system.
package subscription
