// Package fileaccess decides whether a request may read a sensitive path.
//
// IsSensitivePath classifies a path — dotfiles, credentials, and the other
// things a workspace file browser should not hand out by default.
// Capabilities carries what the current request is permitted to do;
// WithCapabilities attaches them to a context and FromContext retrieves them.
// AllowsSensitivePath combines the two: the path's classification and the
// caller's capabilities.
//
// Capabilities travel in the context because the decision happens deep in file
// serving, far from the middleware that authenticated the request, and
// threading a permission argument through every layer between them is how such
// a check ends up quietly skipped on one path.
package fileaccess
