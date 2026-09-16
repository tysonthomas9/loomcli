// Package leadcontrol runs and supervises the lead agent's interactive
// runtime.
//
// A lead differs from a worker in that its conversation outlives any single
// task, so loom drives it as a long-running runtime rather than a one-shot
// process. RunHarnessLeadRuntime and RunCodexLeadRuntime are the two
// implementations; IsControlledLeadBackend reports whether a backend is driven
// this way at all, and HarnessNameForBackend maps a loom backend name to the
// harness identifier the runtime reports.
//
// Delivery to a live lead is tracked rather than assumed.
// MarkAssignmentDeliveryAttempt and MarkLeadMessageDeliveryAttempt record that
// loom tried to hand something to the lead, and MarkAssignmentDelivered
// records that it landed — the gap between the two is what lets a supervisor
// tell a lost message from an ignored one.
//
// The Codex path speaks to a Codex app-server over a client connection
// (DialCodexAppServer); UpdateCodexRuntimeMetadata and
// UpdateHarnessRuntimeMetadata write the resulting runtime facts back onto the
// session under the Metadata* keys declared here.
package leadcontrol
