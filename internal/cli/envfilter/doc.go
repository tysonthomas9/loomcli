// Package envfilter strips the parent process's environment down to what is
// safe to hand a spawned agent.
//
// FilterEnv filters a supplied environment and FilteredEnv filters the current
// one. Loom runs agent subprocesses that it does not trust with its own
// credentials, so the filter is the seam that keeps loom's secrets out of an
// agent's environment rather than relying on each call site to remember.
package envfilter
