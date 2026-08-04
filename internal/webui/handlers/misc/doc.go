// Package misc is a grab-bag of webui HTTP handlers that never earned their own
// package: its Module registers the scope-rooted workspace file browser, and it
// also exports standalone handlers for auth config, AI CLI backend health, client
// error reports, editor launching, agent/task logs, session transcripts, and the
// worker registration API. Wired by internal/webui/handlermux and internal/webui/modbuilder.
package misc
