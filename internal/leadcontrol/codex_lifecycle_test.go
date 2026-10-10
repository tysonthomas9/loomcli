package leadcontrol

import (
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
