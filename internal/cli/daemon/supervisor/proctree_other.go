//go:build !unix

package supervisor

// On non-unix platforms (e.g. plan9, js/wasm) we don't have ps/lsof so the
// process inspector stays zero-value. findDescendantPGIDs and
// findWorktreeOrphans both check for nil and return early, so the daemon
// degrades to the pre-fix behavior on these platforms.
