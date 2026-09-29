// Package logrouter writes agent output to per-agent, per-task log files with
// size-based rotation.
//
// LogRouter fans a running agent's output to the right file under baseDir,
// rotating to numbered backups (.1, .2) once a file passes maxLogSize and
// disabling rotation entirely when maxLogSize is not positive. Agent and task
// identifiers are validated against a strict character set before they reach
// a path, so a hostile name cannot escape baseDir via traversal.
//
// LockWatcher observes the agent lock file and reports the AgentLockInfo of
// whichever agent currently holds it, letting the router follow ownership as
// agents start and stop.
package logrouter
