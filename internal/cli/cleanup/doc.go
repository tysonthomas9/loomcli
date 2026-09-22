// Package cleanup registers the `loom cleanup` command tree, which reclaims
// disk from loom's append-only local state.
//
// Events, sessions, and usage records all accumulate indefinitely by design —
// each is a log of something that already happened — so trimming them is an
// explicit operator action rather than a background side effect. Each
// subcommand purges one of those stores older than DefaultRetentionAge, and
// every purge supports a dry run so an operator can see what would go first.
//
// The event purge never deletes the current day's file, so cleanup running
// against a live daemon cannot remove the file that daemon is still appending
// to.
package cleanup
