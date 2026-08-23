//go:build darwin

package buildsandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSeatbeltProfileConfinesWritesAndSecrets is the H1 positive control. With
// the generated profile wired through Run it proves the two robust guarantees —
// writes are confined to the build/output roots (a write under HOME is denied)
// and a well-known credential store under HOME is unreadable — while ordinary
// reads and the build root stay usable. TmpDir is left empty so the fake HOME
// (a t.TempDir under the real TMPDIR) is not incidentally re-allowed.
func TestSeatbeltProfileConfinesWritesAndSecrets(t *testing.T) {
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		t.Skip("sandbox-exec unavailable")
	}
	home := t.TempDir()
	build := t.TempDir()
	output := t.TempDir()

	// A credential under a denied store, and an ordinary readable file under HOME.
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(home, ".ssh", "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATEKEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	ordinary := filepath.Join(home, "notes.txt")
	if err := os.WriteFile(ordinary, []byte("not-secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	okFile := filepath.Join(build, "ok.txt")
	if err := os.WriteFile(okFile, []byte("readable"), 0o644); err != nil {
		t.Fatal(err)
	}

	profile := Profile(ProfileSpec{BuildRoot: build, OutputRoot: output, Home: home})
	if !strings.Contains(profile, "(deny network*)") {
		t.Fatalf("profile must deny network:\n%s", profile)
	}
	run := func(args ...string) Result {
		return Run(context.Background(), Request{
			Command: args,
			Dir:     build,
			Env:     map[string]string{"PATH": "/usr/bin:/bin", "HOME": home},
			Profile: profile,
		})
	}

	// Reads: build root readable; ordinary HOME file readable (module resolution
	// must work); credential store denied.
	if r := run("/bin/cat", okFile); r.Err != nil {
		t.Fatalf("reading build-root file should be allowed, got err=%v out=%q", r.Err, r.Output)
	}
	if r := run("/bin/cat", ordinary); r.Err != nil {
		t.Fatalf("reading an ordinary HOME file should be allowed (module resolution), got err=%v", r.Err)
	}
	if r := run("/bin/cat", secret); r.Err == nil {
		t.Fatalf("reading $HOME/.ssh/id_rsa should be denied, but succeeded; out=%q", r.Output)
	} else if strings.Contains(r.Output, "PRIVATEKEY") {
		t.Fatalf("credential leaked through sandbox: %q", r.Output)
	}

	// Writes: build root and output dir allowed; anywhere under HOME denied.
	if r := run("/bin/sh", "-c", "echo hi > "+filepath.Join(build, "w.txt")); r.Err != nil {
		t.Fatalf("writing build-root file should be allowed, got err=%v out=%q", r.Err, r.Output)
	}
	if r := run("/bin/sh", "-c", "echo hi > "+filepath.Join(output, "server.mjs")); r.Err != nil {
		t.Fatalf("writing the output dir should be allowed, got err=%v out=%q", r.Err, r.Output)
	}
	if r := run("/bin/sh", "-c", "echo hi > "+filepath.Join(home, "w.txt")); r.Err == nil {
		t.Fatalf("writing under $HOME should be denied, but succeeded")
	}
	if _, err := os.Stat(filepath.Join(home, "w.txt")); err == nil {
		t.Fatalf("sandbox allowed a write under $HOME")
	}
}
