// Package runtypes holds the request and result types exchanged across the
// driver run boundary.
//
// RunRequest and RunResult live in their own leaf package so the driver, the
// sandbox launchers, and the task runners can all speak about a run without
// importing each other. Keeping them here is what lets sandbox depend on the
// shape of a run while driver depends on sandbox, with no cycle.
package runtypes
