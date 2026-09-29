// Package onboarding serves the guided first-run path.
//
// HandleRunFirstTask spans what are normally two separate concerns — creating
// an issue and starting an agent on it — because a new user's first success
// should be one action, not a sequence they have to assemble correctly from
// the UI.
//
// It is a thin composition over the issue and agent services; nothing here
// bypasses them, so onboarding cannot drift from the behavior of the ordinary
// paths.
package onboarding
