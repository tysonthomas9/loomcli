// Package svcimpl holds the concrete implementations behind the
// internal/webui/service interfaces: agent, session, diff, workspace job store,
// and the file service with its rooted, symlink-hardened file store. Handlers
// depend only on the interfaces; internal/webui/app constructs these and injects
// them, and no other non-test package imports it.
package svcimpl
