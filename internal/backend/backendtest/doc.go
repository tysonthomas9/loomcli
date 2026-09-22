// Package backendtest holds the conformance suite that backend.IssueBackend
// implementations are checked against.
//
// RunIssueBackendConformance drives one implementation, described by an
// IssueBackendSuiteConfig, through the behavior the interface promises. Loom
// has several backends and the interface is only trustworthy if they agree, so
// the contract is asserted once here rather than re-tested per implementation.
//
// The suite currently covers the fleet-db (local and cloud), direct fleet, and
// API-server backends. The agent-IPC backend is not yet exercised by it.
package backendtest
