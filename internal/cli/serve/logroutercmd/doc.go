// Package logroutercmd registers the hidden `loom log-router` command.
//
// The command is a thin entry point around internal/logrouter: loom re-execs
// itself in this mode to run an agent's log router as its own process, which
// is why the command is hidden and requires an agent flag. It overrides the
// root command's persistent pre-run so that starting a log router does not
// drag in the setup a normal user-facing command performs.
package logroutercmd
