// Package agentinbox enqueues messages addressed to a running agent.
//
// Enqueue validates the workspace, target agent, and body, then writes an
// AgentInboxMessage to the store in the queued state. Delivery deduplication
// is opt-in and producer-driven: a message collapses onto an earlier one only
// when the producer supplies MessageOptions.DedupeKey. ContentDedupeKey
// derives such a key by hashing the message content, so a producer that may
// retry can make repeated sends idempotent.
package agentinbox
