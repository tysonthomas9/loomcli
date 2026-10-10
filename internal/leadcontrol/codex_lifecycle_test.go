package leadcontrol

import (
	"strconv"
	"strings"
	"testing"
)

// B3: the lead's workspace folder is trusted for this app-server only, so a
// fresh workspace's lead does not stop on Codex's "Trust this folder?" screen
// and nothing is written to the user's ~/.codex/config.toml.
func TestCodexAppServerArgsTrustWorkspaceFolderForThisRun(t *testing.T) {
	args := codexAppServerArgs("ws://127.0.0.1:1", "/cache/sqlite", "/work/my repo")
	got := strings.Join(args, " ")
	want := `-c projects={"/work/my repo"={trust_level="trusted"}}`
	if !strings.Contains(got, want) {
		t.Fatalf("app-server args %q do not trust the workspace folder (want %q)", got, want)
	}
	if !strings.HasPrefix(got, "app-server --listen ws://127.0.0.1:1 -c sqlite_home=") {
		t.Fatalf("app-server args %q lost the listen endpoint or sqlite home", got)
	}
	if strings.Contains(strings.Join(codexAppServerArgs("ws://x", "/s", ""), " "), "projects=") {
		t.Fatal("no work dir must not trust any folder")
	}
}

// B1: only lead app-servers whose lead runtime is gone (parent is PID 1) and
// whose sqlite home is under this Loom's codex-leads cache are reaped.
func TestOrphanedCodexAppServersPicksOnlyOrphanedLoomLeads(t *testing.T) {
	base := "/Users/me/Library/Caches/loom/codex-leads"
	home := strconv.Quote(base + "/ws/lead/s1/sqlite")
	ps := strings.Join([]string{
		`  101     1   101 node /bin/codex app-server --listen ws://127.0.0.1:5000 -c sqlite_home=` + home,
		`  102   101   101 /vendor/codex app-server --listen ws://127.0.0.1:5000 -c sqlite_home=` + home,
		`  103     1    90 /vendor/codex app-server --listen ws://127.0.0.1:5001 -c sqlite_home=` + home,
		`  104   555   104 node /bin/codex app-server --listen ws://127.0.0.1:5002 -c sqlite_home=` + home,
		`  105     1   105 node /bin/codex app-server --listen ws://127.0.0.1:5003 -c sqlite_home="/elsewhere/sqlite"`,
		`  106     1   106 node /bin/codex app-server --listen ws://127.0.0.1:5004 -c sqlite_home="` + base + `-other/x"`,
		`  107     1   107 node /bin/codex --remote ws://127.0.0.1:5000 -c sqlite_home=` + home,
		`garbage`,
	}, "\n")
	got := orphanedCodexAppServers(ps, base)
	want := []codexAppServerProcess{{pid: 101, pgid: 101}, {pid: 103, pgid: 90}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("orphanedCodexAppServers() = %+v, want %+v", got, want)
	}
}
