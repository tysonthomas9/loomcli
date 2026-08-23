//go:build darwin

package buildsandbox

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"testing"
)

// TestSeatbeltProfileDeniesNetwork proves (deny network*) actually blocks a
// connection at run time, deterministically: a loopback listener is reachable
// without the profile and unreachable under it, so the result does not depend on
// external connectivity. Node is the real build runtime, so it stands in for the
// build process here.
func TestSeatbeltProfileDeniesNetwork(t *testing.T) {
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		t.Skip("sandbox-exec unavailable")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	script := fmt.Sprintf(`const s=require('net').connect(%s,'127.0.0.1');s.on('connect',()=>{s.end();process.exit(0)});s.on('error',()=>process.exit(3));setTimeout(()=>process.exit(4),2000);`, portOf(t, ln))
	run := func(profile string) Result {
		return Run(context.Background(), Request{
			Command: []string{node, "-e", script},
			Dir:     t.TempDir(),
			Env:     map[string]string{"PATH": "/usr/bin:/bin", "HOME": t.TempDir()},
			Profile: profile,
		})
	}

	// Control: without the profile the loopback connection succeeds.
	if r := run(""); r.Err != nil {
		t.Fatalf("loopback connect should succeed without a profile, got err=%v out=%q", r.Err, r.Output)
	}
	// Under the profile the connection is denied.
	profile := Profile(ProfileSpec{BuildRoot: t.TempDir(), Home: t.TempDir()})
	if r := run(profile); r.Err == nil {
		t.Fatalf("network connect should be denied under the seatbelt profile, but it succeeded")
	}
}

func portOf(t *testing.T, ln net.Listener) string {
	t.Helper()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}
