// Package workflows turns workflow source into a registered, runnable driver
// bundle.
//
// The pipeline is source in, driver ID out. ReadLocalSource loads a workflow
// from a directory and ValidateWorkflowFiles / ValidateWorkflowEntrypoint
// reject anything malformed before it is built. BuildAndRegister then produces
// a flue bundle and registers it, returning the driver ID that ResolveDriverID
// can later look up by workspace and name.
//
// Builtin workflows ship with loom rather than living in a user's repo.
// BuiltinWorkflowNames lists them, BuiltinWorkflow returns a Spec,
// CloneBuiltinSource copies one out for inspection or modification, and
// EnsureBuiltinWorkflow registers it into a workspace if it is missing.
//
// Provenance is tracked so a running workflow can be traced back to the exact
// source that produced it: SourceDigest hashes the file set and
// SourceManifestProvenance renders a SourceManifest as metadata carried
// alongside the registration. RedactBuildDiagnostics strips secrets from build
// output before it is surfaced to a user.
package workflows
