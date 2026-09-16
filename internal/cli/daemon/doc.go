// Package daemon implements the long-running loom supervisor process.
//
// Daemon owns the agent fleet for one project: it starts, watches, restarts,
// and stops agent subprocesses according to the daemon config, emitting domain
// events as it goes. Supervision proper lives in the supervisor subpackage;
// this package is the process, its configuration, and its control surface.
//
// Two sockets, two audiences. DaemonControlRequest and DaemonControlResponse
// carry operator commands: agent start, stop, restart, and listing, answers to
// an agent's pending input, and the workspace claim hold. AgentIPCRequest
// and AgentIPCResponse carry calls from the agents themselves, which is how a
// running agent claims and mutates tasks without holding storage credentials
// of its own.
//
// The claim hold is the one piece of daemon state built to outlive the
// process. It is recorded in claim-hold.json beside daemon.pid, and unlike the
// PID and state files it is not removed on shutdown, so a workspace quiesced
// before a restart stays quiesced after it.
//
// DaemonState is the on-disk view of a live daemon, readable by other
// processes via ReadStateFile; IsLoomDaemonRunning answers the narrower
// question of liveness from a PID file. ValidateDaemonPaths and
// ResolveDaemonPath keep the runtime, log, and PID locations consistent
// between the process that starts a daemon and the commands that later look
// for one.
package daemon
