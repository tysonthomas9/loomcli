// Package agentcontrol exposes the operator's controls over running agents:
// start, stop, and restart; the pending-input surface; and the workspace claim
// hold.
//
// The module holds functions rather than a daemon — an AgentControlFn, an
// AgentInputFn, and a ClaimHoldFn — so control operations are supplied by
// whatever supervises agents in this deployment, and the handlers stay
// testable without one. The input and claim-hold functions may be nil, which
// leaves those routes unregistered (an older daemon, or remote mode).
//
// PendingInputView and PendingInputOption describe an agent that has stopped
// to ask something — the UI renders the options and posts the answer back
// through the input function, which is what lets an interactive agent block on
// a human without holding a terminal open.
//
// A claim hold is a workspace-level refusal to start new work that leaves runs
// already in flight untouched. These routes are thin proxies over the daemon
// control socket: the daemon owns the hold, its persistence, and its ownership
// rules. A hold is owned, so every route resolves an actor, and releasing
// someone else's hold without force is refused. Store-backed servers, which
// use a different agent lifecycle module, still mount ClaimHoldModule because
// the hold lives with the local daemon.
package agentcontrol
