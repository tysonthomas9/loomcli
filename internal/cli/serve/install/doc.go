// Package install implements `loom serve install-service`, which registers the
// loom server as a managed service on the host.
//
// It renders the platform's service definition — a systemd unit on Linux, a
// launchd plist on macOS — from the resolved server configuration, including
// any extra environment the operator supplied. The templates are rendered
// rather than hand-written so the installed unit always matches the flags the
// operator actually passed.
package install
