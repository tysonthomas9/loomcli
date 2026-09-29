// Package workflows serves the read and control surface for workflow
// definitions and their runs.
//
// The module is built over the store alone: workflow execution belongs to
// internal/driver, and building or registering a bundle belongs to
// internal/workflows. What is left for the handler layer is showing operators
// what exists and what it is doing.
//
// The builtin workflow names are re-exported from the workflow definitions
// package so the UI can special-case them without importing the builder.
package workflows
