// Package doctor implements `loom doctor`, the preflight that reports whether
// this machine can actually run loom.
//
// Each check contributes a CheckResult with a CheckStatus — pass, or a
// degraded or failing state — and the run is summarized into a DoctorSummary
// and rendered as DoctorOutput. The output is structured rather than printed
// ad hoc so the same run can be shown to a human and consumed by tooling.
//
// A check reports; it does not repair. Diagnosis is kept separate from
// remediation so that running doctor is always safe on a working machine.
package doctor
