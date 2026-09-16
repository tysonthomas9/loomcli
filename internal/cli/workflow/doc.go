// Package workflow registers the `loom workflow` command tree — the operator
// surface for a workflow's lifecycle.
//
// The commands follow a workflow from source to running version: clone a
// builtin's source out for editing, build a bundle from a source directory,
// approve a built version, activate it, and run it against an epic with
// supplied inputs. Approval and activation are separate steps because building
// a version and choosing to run it are different decisions, usually made by
// different people.
//
// The heavy lifting belongs elsewhere: internal/workflows builds and registers
// bundles, internal/driver runs them. This package is argument parsing, output
// formatting, and the preflight that refuses to run when the runtime cannot
// support the workflow.
package workflow
