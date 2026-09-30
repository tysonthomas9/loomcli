package pushproxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/mirror"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Test fixture uses the real Git client and bare provider.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

type fixture struct {
	repo, host, provider, token, url string
	store                            *journal.SQLite
	server                           *httptest.Server
	leaseFence                       int64
	base                             string
}

type failRecordStore struct {
	*journal.SQLite
	fail bool
}

func (s *failRecordStore) PutMirrorRecord(ctx context.Context, row journal.MirrorRecord) error {
	if s.fail {
		s.fail = false
		return errors.New("record unavailable")
	}
	return s.SQLite.PutMirrorRecord(ctx, row)
}

func setup(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{repo: filepath.Join(root, "task"), host: filepath.Join(root, "host.git"), provider: filepath.Join(root, "provider.git")}
	if err := os.Mkdir(f.repo, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, f.repo, "init", "-q", "-b", "main")
	git(t, f.repo, "config", "user.name", "Task")
	git(t, f.repo, "config", "user.email", "task@example.test")
	if err := os.WriteFile(filepath.Join(f.repo, ".env.example"), []byte("tracked fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "readme"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, f.repo, "add", ".")
	git(t, f.repo, "commit", "-qm", "base")
	f.base = git(t, f.repo, "rev-parse", "HEAD")
	baseRef, _ := refname.AttemptBase("w", "a")
	git(t, f.repo, "update-ref", baseRef, f.base)
	git(t, root, "init", "-q", "--bare", f.host)
	git(t, f.repo, "push", f.host, f.base+":"+baseRef)
	git(t, root, "init", "-q", "--bare", f.provider)
	var err error
	f.store, err = journal.OpenSQLite(filepath.Join(root, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.EnsureMirrorSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	lease, err := f.store.ClaimLease(context.Background(), LeaseScope("w", "a"), "runner", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.leaseFence = lease.Fence
	f.token, err = Mint(context.Background(), f.store, f.host, "w", "a", lease.Fence)
	if err != nil {
		t.Fatal(err)
	}
	proxy := &Proxy{Store: f.store, Remote: f.provider}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Authorization"), "provider-secret") {
			t.Error("host provider token sent by task copy")
		}
		proxy.ServeHTTP(w, r)
	}))
	f.url = f.server.URL + "/repo.git"
	t.Cleanup(func() { f.server.Close(); _ = f.store.Close() })
	return f
}

func (f *fixture) commit(t *testing.T, path string, data []byte) string {
	t.Helper()
	full := filepath.Join(f.repo, path)
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0600); err != nil {
		t.Fatal(err)
	}
	git(t, f.repo, "add", "-f", path)
	git(t, f.repo, "commit", "-qm", path)
	return git(t, f.repo, "rev-parse", "HEAD")
}

func (f *fixture) push(t *testing.T, ref string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", "-c", "http.extraHeader=Authorization: Bearer "+f.token, "push", f.url, "HEAD:"+ref) //nolint:norawexec // Exercises the real smart-HTTP Git client.
	cmd.Dir = f.repo
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestValidPushRecordsSHAWithoutTaskProviderToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "provider-secret")
	f := setup(t)
	sha := f.commit(t, "nested/readme", []byte("changed"))
	ref, _ := refname.AttemptCapture("w", "a")
	out, err := f.push(t, ref)
	if err != nil {
		t.Fatalf("push: %v: %s", err, out)
	}
	if got := git(t, f.provider, "rev-parse", ref); got != sha {
		t.Fatalf("provider SHA %s, want %s", got, sha)
	}
	row, found, err := f.store.MirrorState(context.Background(), f.host, ref)
	if err != nil || !found || row.SHA != sha || row.State != "mirrored" {
		t.Fatalf("record: %+v %v %v", row, found, err)
	}
}

func TestForeignRefsAndDeletionNeverReachProvider(t *testing.T) {
	for _, ref := range []string{"refs/heads/main", "refs/tags/v1", "refs/loom/ws/w/attempt/other/capture", "refs/loom/ws/w/attempt/a/base", "refs/loom/ws/w/attempt/a/nested/capture"} {
		t.Run(ref, func(t *testing.T) {
			f := setup(t)
			f.commit(t, "readme", []byte("changed"))
			if out, err := f.push(t, ref); err == nil {
				t.Fatalf("foreign ref accepted: %s", out)
			}
			if out := git(t, f.provider, "for-each-ref", "--format=%(refname)"); out != "" {
				t.Fatalf("provider changed: %s", out)
			}
			if out := git(t, f.host, "for-each-ref", "--format=%(refname)", "refs/loom/ws/w/attempt/a/capture"); out != "" {
				t.Fatalf("host ref changed: %s", out)
			}
		})
	}
	t.Run("deletion", func(t *testing.T) {
		f := setup(t)
		ref, _ := refname.AttemptCapture("w", "a")
		body := append(packet([]byte(f.base+" "+strings.Repeat("0", 40)+" "+ref+"\x00report-status\n")), []byte("0000")...)
		req, err := http.NewRequest(http.MethodPost, f.url+"/git-receive-pack", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+f.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("delete status: %s", resp.Status)
		}
		if out := git(t, f.provider, "for-each-ref", "--format=%(refname)"); out != "" {
			t.Fatalf("provider changed: %s", out)
		}
	})
}

func TestExpandedTreeBytesAreCappedPerPush(t *testing.T) {
	var entries []byte
	for i := 0; i < 22; i++ {
		entries = append(entries, []byte(fmt.Sprintf("100644 blob %040x %d\tfile-%02d", i+1, 99<<20, i))...)
		entries = append(entries, 0)
	}
	var total int64
	if err := validateTreeEntries(entries, nil, &total, make(map[string]bool)); err == nil || !strings.Contains(err.Error(), "2 GiB") {
		t.Fatalf("expanded push cap: %d %v", total, err)
	}
}

func TestTokenCannotPushAnotherAttempt(t *testing.T) {
	f := setup(t)
	if _, err := f.store.ClaimLease(context.Background(), LeaseScope("w", "b"), "runner", time.Minute); err != nil {
		t.Fatal(err)
	}
	token, err := Mint(context.Background(), f.store, f.host, "w", "b", 1)
	if err != nil {
		t.Fatal(err)
	}
	f.token = token
	f.commit(t, "readme", []byte("changed"))
	ref, _ := refname.AttemptCapture("w", "a")
	if out, err := f.push(t, ref); err == nil {
		t.Fatalf("wrong attempt accepted: %s", out)
	}
	if out := git(t, f.provider, "for-each-ref", "--format=%(refname)"); out != "" {
		t.Fatalf("provider changed: %s", out)
	}
}

func TestRejectedQuarantineUpdateCannotForwardItsObjects(t *testing.T) {
	f := setup(t)
	f.commit(t, "readme", []byte("changed"))
	f.server.Close()
	proxy := &Proxy{Store: f.store, Remote: f.provider}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			at := bytes.Index(body, bytes.Repeat([]byte{'0'}, 40))
			if at < 0 {
				t.Error("no zero old SHA in push")
				return
			}
			copy(body[at:at+40], f.base)
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		proxy.ServeHTTP(w, r)
	}))
	f.url = f.server.URL + "/repo.git"
	ref, _ := refname.AttemptCapture("w", "a")
	if out, err := f.push(t, ref); err == nil {
		t.Fatalf("rejected quarantine update forwarded: %s", out)
	}
	if out := git(t, f.provider, "for-each-ref", "--format=%(refname)"); out != "" {
		t.Fatalf("provider changed: %s", out)
	}
}

func TestRetryRecordsExactSHAAfterRecordFailure(t *testing.T) {
	f := setup(t)
	sha := f.commit(t, "readme", []byte("changed"))
	f.server.Close()
	store := &failRecordStore{SQLite: f.store, fail: true}
	f.server = httptest.NewServer(&Proxy{Store: store, Remote: f.provider})
	f.url = f.server.URL + "/repo.git"
	ref, _ := refname.AttemptCapture("w", "a")
	if out, err := f.push(t, ref); err == nil || !strings.Contains(out, "contact the host operator") || strings.Contains(out, "record unavailable") {
		t.Fatalf("record failure: %v: %s", err, out)
	}
	if got := git(t, f.provider, "rev-parse", ref); got != sha {
		t.Fatalf("provider ref %s, want %s", got, sha)
	}
	if got := git(t, f.host, "rev-parse", ref); got != sha {
		t.Fatalf("host ref %s, want %s after record failure", got, sha)
	}
	if out, err := f.push(t, ref); err != nil {
		t.Fatalf("retry: %v: %s", err, out)
	}
	row, found, err := f.store.MirrorState(context.Background(), f.host, ref)
	if err != nil || !found || row.SHA != sha {
		t.Fatalf("retry record: %+v %v %v", row, found, err)
	}
}

func TestSecretInIntermediateCommitIsRefused(t *testing.T) {
	f := setup(t)
	f.commit(t, "nested/id_rsa", []byte("private key"))
	git(t, f.repo, "rm", "-q", "nested/id_rsa")
	git(t, f.repo, "commit", "-qm", "drop key")
	ref, _ := refname.AttemptCapture("w", "a")
	if out, err := f.push(t, ref); err == nil || !strings.Contains(out, "secret_path_refused") {
		t.Fatalf("secret history was accepted: %v: %s", err, out)
	}
	if out := git(t, f.provider, "for-each-ref", "--format=%(refname)", ref); out != "" {
		t.Fatalf("secret history reached provider: %s", out)
	}
}

func TestLargeBlobInIntermediateCommitIsRefused(t *testing.T) {
	f := setup(t)
	f.commit(t, "large.bin", make([]byte, 150<<20))
	git(t, f.repo, "rm", "-q", "large.bin")
	git(t, f.repo, "commit", "-qm", "drop blob")
	ref, _ := refname.AttemptCapture("w", "a")
	if out, err := f.push(t, ref); err == nil || !strings.Contains(out, "100 MiB") {
		t.Fatalf("large blob history was accepted: %v: %s", err, out)
	}
	if out := git(t, f.provider, "for-each-ref", "--format=%(refname)", ref); out != "" {
		t.Fatalf("large blob reached provider: %s", out)
	}
}

func TestMirrorKeepsProxyAcceptedRef(t *testing.T) {
	f := setup(t)
	sha := f.commit(t, "readme", []byte("changed"))
	ref, _ := refname.AttemptCapture("w", "a")
	if out, err := f.push(t, ref); err != nil {
		t.Fatalf("push: %v: %s", err, out)
	}
	if got := git(t, f.host, "rev-parse", ref); got != sha {
		t.Fatalf("host ref %s, want %s", got, sha)
	}
	git(t, f.host, "remote", "add", "origin", f.provider)
	if err := mirror.SyncRepo(context.Background(), f.store, f.host, f.base); err != nil {
		t.Fatal(err)
	}
	if got := git(t, f.provider, "rev-parse", ref); got != sha {
		t.Fatalf("mirror lost provider ref: got %s, want %s", got, sha)
	}
}

func TestSecretPathSizeAndStaleFenceRefused(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		data       []byte
		want       string
	}{
		{"env", "nested/.env", []byte("secret"), "secret_path_refused"},
		{"ssh", "nested/id_rsa", []byte("secret"), "secret_path_refused"},
		{"size", "large.bin", make([]byte, 150<<20), "100 MiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			f.commit(t, tc.path, tc.data)
			ref, _ := refname.AttemptCapture("w", "a")
			out, err := f.push(t, ref)
			if err == nil || !strings.Contains(out, tc.want) {
				t.Fatalf("push result: %v: %s", err, out)
			}
			if out := git(t, f.provider, "for-each-ref", "--format=%(refname)"); out != "" {
				t.Fatalf("provider changed: %s", out)
			}
		})
	}
	t.Run("stale", func(t *testing.T) {
		f := setup(t)
		f.commit(t, "readme", []byte("changed"))
		lease, err := f.store.CurrentLease(context.Background(), LeaseScope("w", "a"))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.ReleaseLease(context.Background(), lease); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.ClaimLease(context.Background(), LeaseScope("w", "a"), "new", time.Minute); err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, f.url+"/git-receive-pack", strings.NewReader(""))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+f.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("stale status: %s", resp.Status)
		}
		message, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(message), "stale") {
			t.Fatalf("stale reason: %s", message)
		}
		if out := git(t, f.provider, "for-each-ref", "--format=%(refname)"); out != "" {
			t.Fatalf("provider changed: %s", out)
		}
	})
}

func TestProviderRejectionRecordsNoAcceptedSHA(t *testing.T) {
	f := setup(t)
	f.commit(t, "readme", []byte("changed"))
	hook := filepath.Join(f.provider, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho provider denied >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ref, _ := refname.AttemptCapture("w", "a")
	out, err := f.push(t, ref)
	if err == nil || !strings.Contains(out, "provider denied") {
		t.Fatalf("provider rejection: %v: %s", err, out)
	}
	if _, found, err := f.store.MirrorState(context.Background(), f.host, ref); err != nil || found {
		t.Fatalf("accepted record after rejection: %v %v", found, err)
	}
	if out := git(t, f.provider, "for-each-ref", "--format=%(refname)"); out != "" {
		t.Fatalf("provider changed: %s", out)
	}
}

func TestProviderReceivesOnlyCommitsAlreadyFetchedByHost(t *testing.T) {
	f := setup(t)
	sha := f.commit(t, "readme", []byte("changed"))
	hook := filepath.Join(f.provider, "hooks", "pre-receive")
	script := fmt.Sprintf("#!/bin/sh\nread old new ref\nunset GIT_DIR GIT_WORK_TREE GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_QUARANTINE_PATH\ngit -C %q cat-file -e \"$new^{commit}\" || exit 1\n", f.host)
	if err := os.WriteFile(hook, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ref, _ := refname.AttemptCapture("w", "a")
	if out, err := f.push(t, ref); err != nil {
		t.Fatalf("provider could not find commit in host before push: %v: %s", err, out)
	}
	if got := git(t, f.provider, "rev-parse", ref); got != sha {
		t.Fatalf("provider SHA = %s, want %s", got, sha)
	}
}
