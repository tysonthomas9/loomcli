package opencode

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// TestCompatibility is the R22 copy-of-data check: the pinned v2 build opens
// a copy of the user's OpenCode database and creates a session, and the 1.18
// executable can still read its own data afterwards. It also pins the known
// incompatibility: 1.18 cannot see sessions v2 creates. It never opens the
// user's database for writing.
//
// LOOM_REAL_OPENCODE=1 and LOOM_OPENCODE_DB (an owned /tmp copy; there is
// no default) enable it. LOOM_OPENCODE_V2 and LOOM_OPENCODE_V1 override the
// default binary paths.
func TestCompatibility(t *testing.T) {
	if os.Getenv("LOOM_REAL_OPENCODE") != "1" {
		t.Skip("set LOOM_REAL_OPENCODE=1 to run against real OpenCode binaries")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	v2 := envOr("LOOM_OPENCODE_V2", filepath.Join(home, ".loom/harness/opencode/2.0.19/opencode"))
	v1 := envOr("LOOM_OPENCODE_V1", filepath.Join(home, ".opencode/bin/opencode"))
	// No default source: the user's real database is never read unless a
	// copy is named explicitly, and it must be an owned /tmp copy.
	src := os.Getenv("LOOM_OPENCODE_DB")
	if src == "" {
		t.Skip("set LOOM_OPENCODE_DB to an owned /tmp copy of an OpenCode database; there is no default")
	}
	if !ownedTmp(filepath.Dir(src)) {
		t.Fatalf("LOOM_OPENCODE_DB %s is not in an owned /tmp directory", src)
	}

	out := run(t, v2, "", nil, "--version")
	got, err := loomharness.ParseVersion(out)
	if err != nil {
		t.Fatal(err)
	}
	if min := loomharness.Versions["opencode"].Minimum; got.Less(min) {
		t.Fatalf("v2 build is %s, below the minimum %s", got, min)
	}
	t.Logf("v2 %s: %s", v2, strings.TrimSpace(out))
	t.Logf("v1 %s: %s", v1, strings.TrimSpace(run(t, v1, "", nil, "--version")))

	sbx := newSandbox(t, "loom-opencode-compat-", "{}")
	env := sandboxEnv(sbx)
	data := filepath.Join(sbx, "data", "opencode")
	repo := filepath.Join(sbx, "repo")
	for _, d := range []string{data, repo, filepath.Join(sbx, "home"), filepath.Join(sbx, "tmp")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dst := filepath.Join(data, "opencode.db")
	snapshot(t, src, dst)

	before := sessionIDs(t, dst)
	if len(before) == 0 {
		t.Skip("source database has no sessions to compare")
	}
	newest := newestSession(t, dst)
	exportBefore := run(t, v1, repo, env, "export", newest)

	created := createWithV2(t, v2, repo, env)
	t.Logf("v2 created %s on the copy", created)

	run(t, v1, repo, env, "session", "list", "--format", "json")
	if exportAfter := run(t, v1, repo, env, "export", newest); exportAfter != exportBefore {
		t.Errorf("1.18 export of %s changed after v2 used the data", newest)
	}
	after := sessionIDs(t, dst)
	for id := range before {
		if !after[id] {
			t.Errorf("1.18 session %s missing after v2 used the data", id)
		}
	}

	// R22 DoD: 1.18 should also read the session v2 created. On 2.0.19 it
	// cannot: v2 keeps new sessions in its own tables (session_v2), so the
	// result is INCOMPATIBLE for two-way use. The test asserts that observed
	// state, so it fails if a new build changes it either way.
	_, exportErr := runErr(v1, repo, env, "export", created)
	visible := after[created]
	t.Logf("R22 finding: v2-created session %s visible to 1.18: table=%v export=%v (INCOMPATIBLE for two-way use when both false)",
		created, visible, exportErr == nil)
	if visible || exportErr == nil {
		t.Errorf("1.18 can now see v2 session %s; the R22 INCOMPATIBLE finding is out of date, so re-check two-way use", created)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// snapshot copies src to dst through a read-only connection.
func snapshot(t *testing.T, src, dst string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+src+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("VACUUM INTO ?", dst); err != nil {
		t.Fatalf("snapshot %s: %v", src, err)
	}
}

func sessionIDs(t *testing.T, path string) map[string]bool {
	t.Helper()
	db := openRO(t, path)
	rows, err := db.Query("SELECT id FROM session")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func newestSession(t *testing.T, path string) string {
	t.Helper()
	var id string
	if err := openRO(t, path).QueryRow("SELECT id FROM session ORDER BY time_updated DESC LIMIT 1").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func openRO(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func run(t *testing.T, bin, dir string, env []string, args ...string) string {
	t.Helper()
	out, err := runErr(bin, dir, env, args...)
	if err != nil {
		t.Fatalf("%s %v: %v", bin, args, err)
	}
	return out
}

func runErr(bin, dir string, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir, cmd.Env = dir, env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("%w\n%s", err, stderr.String())
	}
	return string(out), nil
}

// createWithV2 runs `serve` on the copy, creates a session, reads it back and
// stops the server.
func createWithV2(t *testing.T, bin, repo string, env []string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	const password = "loom-compat"
	cmd := exec.Command(bin, "serve", "--hostname", "127.0.0.1", "--port", fmt.Sprint(port))
	cmd.Dir = repo
	cmd.Env = append(append([]string{}, env...), "OPENCODE_SERVER_PASSWORD="+password)
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}()

	base := fmt.Sprintf("http://127.0.0.1:%d/api/session", port)
	call := func(method, url string, body any) (int, []byte) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, url, rd)
		req.SetBasicAuth("opencode", password)
		req.Header.Set("content-type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, nil
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, out
	}
	deadline := time.Now().Add(5 * time.Minute)
	for {
		if code, _ := call("GET", base+"?limit=1", nil); code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("v2 serve not ready:\n%s", logs.String())
		}
		time.Sleep(250 * time.Millisecond)
	}
	code, body := call("POST", base, map[string]any{
		"title":    "loom 1.4a compatibility",
		"location": map[string]string{"directory": repo},
	})
	var created struct {
		Data struct{ ID string } `json:"data"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &created) != nil || created.Data.ID == "" {
		t.Fatalf("v2 create: %d %s", code, body)
	}
	if code, body := call("GET", base+"/"+created.Data.ID, nil); code != http.StatusOK {
		t.Fatalf("v2 read %s: %d %s", created.Data.ID, code, body)
	}
	return created.Data.ID
}
