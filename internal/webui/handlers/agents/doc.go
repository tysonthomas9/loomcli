// Package agents serves the agent lifecycle endpoints: list, create, delete,
// and the start, stop, and restart transitions.
//
// Every mutating handler takes the realtime hub alongside the service, because
// an agent's state is displayed on screens that did not initiate the change —
// the write is only half the job, and broadcasting it is the other half.
//
// HandleInteractivePrompts serves the prompts available to interactive agents.
// HandleQueueUnsupported answers the queue endpoint for fleet-db-backed
// workspaces, which keep no per-agent scored work queue, so a client gets an
// explicit unsupported response rather than a confusing empty one.
package agents
