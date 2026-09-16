// Package prreview serves the pull-request review surface.
//
// The module composes a store, an agent service, and a connector Dispatcher:
// reviewing a PR needs loom's own state, an agent to perform the review, and
// authenticated egress to the forge the PR lives on. The dispatcher is how
// that egress happens — the handler never holds forge credentials itself, it
// asks the connector layer to make the call.
package prreview
