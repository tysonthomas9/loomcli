// Package subscription bridges fleet-db mutation events onto the web UI's
// realtime.Hub so browsers see issue-tracker changes over SSE: one long-polling
// subscriber per active workspace, activated and idle-reaped by
// MultiWorkspaceSubscriber, plus the SSE token-exchange handler and route
// module. Wired by internal/webui/appinfra and driven by internal/webui/hooks.
package subscription
