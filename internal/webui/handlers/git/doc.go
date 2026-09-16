// Package git serves the repository views the UI renders: diffs and blocked
// work.
//
// The diff endpoints go through service.DiffService and split by granularity —
// the files in a range, the commits in it, a single file's patch, and the
// summary stat shown next to an agent or issue. Splitting them keeps the UI
// from fetching a whole diff to display a line count.
//
// The blocked endpoints report work that cannot proceed. They come in several
// forms because what "blocked" can be computed from differs by deployment: a
// daemon pool alone, a pool with an issue backend, or an injected connection
// getter for tests.
package git
