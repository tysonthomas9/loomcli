// Package configlock serializes mutations to a loom config directory across
// processes.
//
// ConfigLock takes an exclusive lock on config.lock inside the given config
// directory and returns the matching unlock function; WithLock wraps a
// function in that pair. Any read-modify-write of a shared config file must
// hold this lock, because several loom CLI invocations and the daemon can
// target one config directory at the same time.
package configlock
