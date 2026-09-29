// Package worker holds the pieces a loom worker needs when it runs remotely
// from the control plane.
//
// LogForwarder ships a worker's logs to the control plane over HTTP,
// authenticated with the worker's own token and tagged with its worker ID. A
// worker executing off-host has no shared filesystem with the server, so
// forwarding is the only way its output reaches the operator watching the UI.
package worker
