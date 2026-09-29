// Package editor opens a file in the operator's local editor from the web UI.
//
// Registry lists the editors loom knows how to launch, each with a
// PlatformConfig describing how it is invoked per operating system.
// DetectedEditors returns those actually installed on this machine, and
// DetectedEditorsForOS does the same for a named GOOS so the detection logic
// stays testable off-platform. LaunchEditor opens the requested targets in a
// chosen DetectedEditor.
//
// This only makes sense when the browser and the server share a machine: the
// server launches the editor, so a remotely hosted loom has nothing to open.
package editor
