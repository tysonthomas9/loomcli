// Package agenterr classifies agent subprocess failures into the single
// outcome vocabulary shared by loom's retry and supervision layers.
//
// Classification takes a finished process's log file (ClassifyFromLog) or its
// captured output (ClassifyFromOutput) and returns an AgentError carrying an
// Outcome, the backend that produced it, and a RetryAfter for rate limits.
// Outcome unifies two sources: error classes the harness wrapper already
// recognizes (OutcomeFromHarness) and loom-domain outcomes the harness cannot
// see (OutcomeFromDomain).
//
// Marker constants such as AuthRequiredMarker exist so an unambiguous signal
// beats prose pattern-matching. The regex table is only the residual path, for
// output that carries no marker.
//
// This package decides what happened, never what to do about it. Retry,
// quarantine, and supervision consequences belong to internal/agentpolicy.
package agenterr
