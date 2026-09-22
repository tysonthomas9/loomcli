// Package backends implements the agent harnesses loom can drive and the
// capability negotiation around them.
//
// ClaudeBackend, CodexBackend, CursorBackend, GeminiBackend, OpenCodeBackend,
// and ExternalBackend are the implementations; DiscoverExternalBackends finds
// harnesses installed on the host that loom does not ship knowledge of.
// CheckBackendHealth probes one for readiness within VersionProbeTimeout.
//
// Backends differ in what they can enforce, and ValidateSafetyKnobs is where
// that difference is resolved. The two knobs are treated differently on
// purpose: a tool restriction the backend cannot enforce is refused outright
// (fail-closed), while read_only on a backend with no hard mechanism degrades
// to a prompt preamble and returns a warning rather than an error. The
// degradation is deliberate — the built-in plan role sets ReadOnly on every
// workspace, so failing closed there would refuse to spawn any planner on any
// backend lacking hard read-only. SupportsToolControl and SupportsHardReadOnly
// report which case applies, and InspectCapabilities returns the fuller
// BackendCapabilities.
//
// Session identity is threaded through package-level state because a harness
// reports its own session ID asynchronously, after loom has already launched
// it: SetLastCapturedSessionID and SetResumeSessionID record what the harness
// told us, and the matching getters and Clear* functions are how a run picks
// it up and resets between invocations. ReadHarnessUsage and
// SessionTokensFromHarnessUsage recover token counts from the harness's own
// transcript, which is the only place they exist.
//
// RunControlledLeadRuntime and RunCodexLeadRuntime are the entry points for
// leads driven as long-running runtimes rather than one-shot invocations.
package backends
