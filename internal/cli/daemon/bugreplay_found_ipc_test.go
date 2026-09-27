//go:build daemon_bugreplay

package daemon

import "testing"

// Found bug #11: a mutation without its lease validator must be rejected.
func TestBugReplay_Found11_IPCWithoutLeaseValidatorFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Daemon)
	}{
		{"no store", func(d *Daemon) { d.store = nil }},
		{"no supervisor", func(d *Daemon) { d.sup = nil }},
		{"no workspace", func(d *Daemon) { d.sup.WorkspaceID = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newLeaseValidationDaemon(&scriptedAgentLeaseStore{heartbeatLease: validIPCLease()})
			tc.edit(d)
			if resp, ok := d.validateIPCLease(t.Context(), validIPCRequest()); ok {
				t.Fatalf("IPC lease validation passed without %s: %+v", tc.name, resp)
			}
		})
	}
}
