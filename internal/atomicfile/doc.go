// Package atomicfile writes whole files so a concurrent reader never observes
// a partial write.
//
// WriteFile stages content in a temporary file in the destination directory,
// applies the requested permissions, then renames it over the target; a
// failure at any step removes the temporary file and leaves the original
// untouched. Note that it does not fsync — it protects against torn reads,
// not against loss of the write across an unclean shutdown.
//
// Use it for state that another loom process may read while it is being
// rewritten: config, manifests, and lock metadata.
package atomicfile
