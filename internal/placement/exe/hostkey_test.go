package exe

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func testHostKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("wrap key: %v", err)
	}
	return signer
}

// TestHostKeyPinsOnFirstUseAndRejectsChanges is the security property.
//
// exe.dev gives every VM a fresh host key and exposes it nowhere, so there is
// nothing to pin in advance -- trust-on-first-use is the strongest option
// available. What makes it worth having rather than theater is that it must
// REJECT a later substitution: the lead's occupant token travels over this
// channel, so a silently accepted new key hands the credential to whoever
// presented it.
func TestHostKeyPinsOnFirstUseAndRejectsChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hostkeys")
	store := newHostKeyStore(path)
	callback := store.callback()

	original := testHostKey(t)
	if err := callback("loom-p1.exe.xyz:22", nil, original); err != nil {
		t.Fatalf("first use should pin, got: %v", err)
	}
	// Same key again: accepted.
	if err := callback("loom-p1.exe.xyz:22", nil, original); err != nil {
		t.Fatalf("unchanged key rejected: %v", err)
	}
	// Different key for the same host: refused.
	err := callback("loom-p1.exe.xyz:22", nil, testHostKey(t))
	if err == nil {
		t.Fatal("a substituted host key was ACCEPTED; the occupant token would be handed to it")
	}
	if !strings.Contains(err.Error(), "changed since first use") {
		t.Fatalf("unhelpful error: %v", err)
	}
	// A different host is independent.
	if err := callback("loom-p2.exe.xyz:22", nil, testHostKey(t)); err != nil {
		t.Fatalf("a different host must pin independently: %v", err)
	}
}

// TestHostKeysPersistAcrossRestarts: without persistence every connection is a
// first connection, so nothing is ever verified and the pinning is decorative.
func TestHostKeysPersistAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "hostkeys")
	original := testHostKey(t)

	first := newHostKeyStore(path)
	if err := first.callback()("loom-p1.exe.xyz:22", nil, original); err != nil {
		t.Fatalf("pin: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("store not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("store mode = %v, want 0600", perm)
	}

	// A fresh process must reject a substitution using only the file.
	reloaded := newHostKeyStore(path)
	if err := reloaded.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := reloaded.callback()("loom-p1.exe.xyz:22", nil, original); err != nil {
		t.Fatalf("reloaded store rejected the pinned key: %v", err)
	}
	if err := reloaded.callback()("loom-p1.exe.xyz:22", nil, testHostKey(t)); err == nil {
		t.Fatal("reloaded store accepted a substituted key")
	}
}

// TestForgetAllowsNameReuseAfterDelete: exe.dev VM names are reusable, so a
// stale pin would make every future placement on a recycled name fail as a
// host key change.
func TestForgetAllowsNameReuseAfterDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hostkeys")
	store := newHostKeyStore(path)
	if err := store.callback()("loom-p1.exe.xyz:22", nil, testHostKey(t)); err != nil {
		t.Fatalf("pin: %v", err)
	}
	store.forget("loom-p1.exe.xyz")
	if err := store.callback()("loom-p1.exe.xyz:22", nil, testHostKey(t)); err != nil {
		t.Fatalf("after forget, a rebuilt VM must pin afresh: %v", err)
	}
}

// TestNewRequiresAHostKeyStorePath: without a path there is nothing to compare
// against, and every connection silently becomes a first connection.
func TestNewRequiresCredentialsAndPinning(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
		want string
	}{
		{"no token", Config{SSHKeyPath: "k", HostKeyPath: "h"}, "token required"},
		{"no ssh key", Config{Token: "t", HostKeyPath: "h"}, "ssh key path required"},
		{"no host key store", Config{Token: "t", SSHKeyPath: "k"}, "host key store path required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestVMSSHRouteUsesServiceResponse covers both routing shapes returned by
// exe.dev accounts. The VM record, not a hardcoded hostname, is authoritative.
func TestVMSSHRouteUsesServiceResponse(t *testing.T) {
	for _, tc := range []struct {
		name     string
		vm       vm
		wantHost string
		wantUser string
	}{
		{
			name:     "direct hostname",
			vm:       vm{Name: "loom-p1", SSHDest: "loom-p1.exe.xyz", SSHHost: "loom-p1.exe.xyz"},
			wantHost: "loom-p1.exe.xyz", wantUser: "exedev",
		},
		{
			name:     "shared gateway",
			vm:       vm{Name: "loom-p1", SSHDest: "vm+loom-p1@vm.exe.xyz", SSHHost: "vm.exe.xyz", SSHUser: "vm+loom-p1"},
			wantHost: "vm.exe.xyz", wantUser: "vm+loom-p1",
		},
		{
			name:     "destination fallback",
			vm:       vm{Name: "loom-p1", SSHDest: "vm+loom-p1@vm.exe.xyz"},
			wantHost: "vm.exe.xyz", wantUser: "vm+loom-p1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			route, err := sshRouteForVM(tc.vm)
			if err != nil {
				t.Fatalf("sshRouteForVM: %v", err)
			}
			if route.host != tc.wantHost || route.user != tc.wantUser {
				t.Fatalf("route = %#v, want host=%q user=%q", route, tc.wantHost, tc.wantUser)
			}
			if route.pinIdentity != "vm:loom-p1" {
				t.Fatalf("pin identity = %q, want stable VM identity", route.pinIdentity)
			}
		})
	}
}
