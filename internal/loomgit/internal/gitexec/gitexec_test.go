package gitexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/cred"
)

func TestRunWithCredentialRefusesForeignURLBeforeConsumingToken(t *testing.T) {
	r, _, _ := fixture(t)
	source := cred.New("https://github.com/owner/repo.git", "secret")
	_, err := r.RunWithCredential(context.Background(), source, "https://github.com/owner/other.git", "fetch", "https://github.com/owner/other.git")
	if !errors.Is(err, cred.ErrRefused) {
		t.Fatalf("foreign URL error = %v, want refusal", err)
	}
	_, err = r.RunWithCredential(context.Background(), source, "https://github.com/owner/repo.git", "fetch", "https://github.com/owner/other.git")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("command with mismatched URL = %v, want forbidden", err)
	}
	if _, err := source.Take("https://github.com/owner/repo.git"); err != nil {
		t.Fatalf("foreign attempts consumed token: %v", err)
	}
}

func TestRunWithCredentialUsesHostAskpassWithoutTokenInArgv(t *testing.T) {
	r, dir, _ := fixture(t)
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CAPTURE_ARGS\"\n\"$GIT_ASKPASS\" 'Username for repo:' > \"$CAPTURE_USER\"\n\"$GIT_ASKPASS\" 'Password for repo:' > \"$CAPTURE_PASSWORD\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(dir, "args")
	userFile := filepath.Join(dir, "user")
	passwordFile := filepath.Join(dir, "password")
	t.Setenv("CAPTURE_ARGS", argsFile)
	t.Setenv("CAPTURE_USER", userFile)
	t.Setenv("CAPTURE_PASSWORD", passwordFile)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	url := "https://github.com/owner/repo.git"
	if _, err := r.RunWithCredential(context.Background(), cred.New(url, "fixture-secret"), url, "fetch", url); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), "fixture-secret") {
		t.Fatalf("token appeared in git argv: %s", args)
	}
	user, err := os.ReadFile(userFile)
	if err != nil || string(user) != "x-access-token" {
		t.Fatalf("askpass username = %q, %v", user, err)
	}
	password, err := os.ReadFile(passwordFile)
	if err != nil || string(password) != "fixture-secret" {
		t.Fatalf("askpass password = %q, %v", password, err)
	}
}

func fixture(t *testing.T) (*Runner, string, string) {
	t.Helper()
	dir := t.TempDir()
	config := filepath.Join(dir, "global.gitconfig")
	if err := os.WriteFile(config, []byte("[user]\nname = Example Author\nemail = author@example.test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init", "-q")
	r, err := New(dir, Options{GlobalConfig: config, SystemConfig: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	return r, dir, config
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // Test fixture setup uses real Git.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func TestDefaultOptionsReadAllowlistedGlobalConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	previousGlobal, hadGlobal := os.LookupEnv("GIT_CONFIG_GLOBAL")
	if err := os.Unsetenv("GIT_CONFIG_GLOBAL"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadGlobal {
			_ = os.Setenv("GIT_CONFIG_GLOBAL", previousGlobal)
		} else {
			_ = os.Unsetenv("GIT_CONFIG_GLOBAL")
		}
	})
	config := `[user]
name = Home Author
email = home@example.test
signingkey = ABC123
[filter "lfs"]
clean = git-lfs clean -- %f
[lfs]
fetchinclude = assets/*
[commit]
gpgsign = true
[gpg]
format = openpgp
[credential]
helper = !false
[alias]
evil = !false
`
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	r, err := New(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Identity() != (Identity{Name: "Home Author", Email: "home@example.test"}) {
		t.Fatalf("identity: %+v", r.Identity())
	}
	joined := strings.Join(r.config, "\n")
	for _, want := range []string{"filter.lfs.clean=git-lfs clean -- %f", "lfs.fetchinclude=assets/*", "commit.gpgsign=true", "gpg.format=openpgp", "user.signingkey=ABC123"} {
		if !strings.Contains(joined, want) {
			t.Errorf("allowed global config %q missing from %q", want, joined)
		}
	}
	for _, forbidden := range []string{"credential.helper", "alias.evil"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("unsafe config %q survived", forbidden)
		}
	}
}

func mustRun(t *testing.T, r *Runner, args ...string) string {
	t.Helper()
	out, err := r.Run(context.Background(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestRedactsCredentialsFromGitErrors(t *testing.T) {
	r, _, _ := fixture(t)
	_, err := r.Run(context.Background(), "show", "https://user:ghp_x@github.com")
	if err == nil || !strings.Contains(err.Error(), "https://***@github.com") || strings.Contains(err.Error(), "ghp_x") {
		t.Fatalf("error was not redacted: %v", err)
	}
}

func TestForbiddenCommandsNeverStart(t *testing.T) {
	r, dir, _ := fixture(t)
	marker := filepath.Join(dir, "started")
	for _, args := range [][]string{{"clean", "-fd"}, {"push", "--force", "origin", "HEAD"}, {"push", "-f"}, {"push", "-uf"}, {"push", "--mirror"}, {"push", "origin", "+HEAD:refs/heads/main"}, {"push", "--force-with-lease", "origin"}} {
		_, err := r.Run(context.Background(), args...)
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("%v: got %v", args, err)
		}
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected command side effect: %v", err)
	}
	remote := filepath.Join(dir, "remote.git")
	runGit(t, dir, "init", "--bare", "-q", remote)
	os.WriteFile(filepath.Join(dir, "file"), []byte("first"), 0600)
	mustRun(t, r, "add", "file")
	mustRun(t, r, "commit", "-qm", "first")
	sha := mustRun(t, r, "rev-parse", "HEAD")
	mustRun(t, r, "push", "--force-with-lease=refs/heads/test:", remote, "HEAD:refs/heads/test")
	if got := runGit(t, remote, "rev-parse", "refs/heads/test"); got != sha {
		t.Fatalf("push got %s, want %s", got, sha)
	}
	os.WriteFile(filepath.Join(dir, "file"), []byte("second"), 0600)
	mustRun(t, r, "add", "file")
	mustRun(t, r, "commit", "-qm", "second")
	mustRun(t, r, "push", "--force-with-lease=refs/heads/test:"+sha, remote, "HEAD:refs/heads/test")
}

func TestIdentityAndUnsafeConfigIsolation(t *testing.T) {
	r, dir, config := fixture(t)
	hooks := filepath.Join(dir, "hooks")
	if err := os.Mkdir(hooks, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "hook-ran")
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(config, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("[core]\nhooksPath = " + hooks + "\n[alias]\nevil = !touch " + filepath.Join(dir, "alias-ran") + "\n[credential]\nhelper = !touch " + filepath.Join(dir, "helper-ran") + "\n")
	file.Close()
	runGit(t, dir, "config", "--local", "alias.evil", "!touch "+filepath.Join(dir, "local-alias-ran"))
	runGit(t, dir, "config", "--local", "core.hooksPath", hooks)
	runGit(t, dir, "config", "--local", "credential.https://example.test.helper", "!touch "+filepath.Join(dir, "local-helper-ran"))
	r, err = New(dir, Options{GlobalConfig: config, SystemConfig: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "file"), []byte("content"), 0600)
	mustRun(t, r, "add", "file")
	mustRun(t, r, "commit", "-qm", "test")
	if got := mustRun(t, r, "show", "-s", "--format=%an <%ae>|%cn <%ce>"); got != "Example Author <author@example.test>|Example Author <author@example.test>" {
		t.Fatal(got)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("hook ran: %v", err)
	}
	_, err = r.Run(context.Background(), "evil")
	if err == nil {
		t.Fatal("global alias ran")
	}
	if _, err := os.Stat(filepath.Join(dir, "alias-ran")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("alias side effect")
	}
	if _, err := os.Stat(filepath.Join(dir, "local-alias-ran")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("local alias side effect")
	}
	_, err = r.run(context.Background(), strings.NewReader("protocol=https\nhost=example.test\n\n"), "credential", "fill")
	if err == nil {
		t.Fatal("credential fill unexpectedly succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, "helper-ran")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("credential helper ran")
	}
	if _, err := os.Stat(filepath.Join(dir, "local-helper-ran")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("local credential helper ran")
	}
}

func TestUpdateRefCAS(t *testing.T) {
	r, dir, _ := fixture(t)
	os.WriteFile(filepath.Join(dir, "file"), []byte("a"), 0600)
	mustRun(t, r, "add", "file")
	mustRun(t, r, "commit", "-qm", "first")
	first := mustRun(t, r, "rev-parse", "HEAD")
	os.WriteFile(filepath.Join(dir, "file"), []byte("b"), 0600)
	mustRun(t, r, "add", "file")
	mustRun(t, r, "commit", "-qm", "second")
	second := mustRun(t, r, "rev-parse", "HEAD")
	ref := "refs/loom/test"
	if err := r.UpdateRef(context.Background(), ref, first, strings.Repeat("0", 40)); err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateRef(context.Background(), ref, second, strings.Repeat("0", 40)); !errors.Is(err, ErrStale) {
		t.Fatalf("want ErrStale, got %v", err)
	}
	if got := mustRun(t, r, "rev-parse", ref); got != first {
		t.Fatal(got)
	}
}

func TestOutputCapAndTimeoutAreTyped(t *testing.T) {
	r, _, _ := fixture(t)
	r.cap = 8
	_, err := r.Run(context.Background(), "version")
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	r.cap = defaultOutputCap
	r.timeout = time.Nanosecond
	_, err = r.Run(context.Background(), "version")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
}

func TestGlobalLFSFilterSurvivesIsolation(t *testing.T) {
	if _, err := exec.LookPath("git-lfs"); err != nil {
		t.Skip("git-lfs is not installed")
	}
	r, dir, config := fixture(t)
	file, err := os.OpenFile(config, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("[filter \"lfs\"]\nclean = git-lfs clean -- %f\nsmudge = git-lfs smudge -- %f\nprocess = git-lfs filter-process\nrequired = true\n")
	file.Close()
	r, err = New(dir, Options{GlobalConfig: config, SystemConfig: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("asset.bin filter=lfs diff=lfs merge=lfs -text\n"), 0600)
	os.WriteFile(filepath.Join(dir, "asset.bin"), []byte("large asset content"), 0600)
	mustRun(t, r, "add", ".gitattributes", "asset.bin")
	mustRun(t, r, "commit", "-qm", "lfs")
	stored := mustRun(t, r, "show", "HEAD:asset.bin")
	if !strings.HasPrefix(stored, "version https://git-lfs.github.com/spec/v1") {
		t.Fatalf("not LFS pointer: %s", stored)
	}
	if err := os.Remove(filepath.Join(dir, "asset.bin")); err != nil {
		t.Fatal(err)
	}
	mustRun(t, r, "checkout", "--", "asset.bin")
	content, err := os.ReadFile(filepath.Join(dir, "asset.bin"))
	if err != nil || string(content) != "large asset content" {
		t.Fatalf("smudge got %q: %v", content, err)
	}
}

func TestGlobalSigningConfigSurvivesIsolation(t *testing.T) {
	if _, err := exec.LookPath("gpg"); err != nil {
		t.Skip("gpg is not installed")
	}
	r, dir, config := fixture(t)
	gpgHome, err := os.MkdirTemp("/tmp", "gpg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(gpgHome) })
	t.Setenv("GNUPGHOME", gpgHome)
	keygen := exec.Command("gpg", "--batch", "--pinentry-mode", "loopback", "--passphrase", "", "--quick-generate-key", "Example Author <author@example.test>", "default", "default", "never") //nolint:norawexec,gosec // Test generates a disposable key.
	if out, err := keygen.CombinedOutput(); err != nil {
		t.Fatalf("gpg keygen: %s: %v", out, err)
	}
	list := exec.Command("gpg", "--batch", "--with-colons", "--list-secret-keys") //nolint:norawexec,gosec // Test reads its disposable key.
	out, err := list.Output()
	if err != nil {
		t.Fatal(err)
	}
	var fingerprint string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) > 9 && fields[0] == "fpr" {
			fingerprint = fields[9]
			break
		}
	}
	if fingerprint == "" {
		t.Fatal("no GPG fingerprint")
	}
	file, err := os.OpenFile(config, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("[commit]\ngpgsign = true\n[user]\nsigningkey = " + fingerprint + "\n")
	file.Close()
	r, err = New(dir, Options{GlobalConfig: config, SystemConfig: os.DevNull})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "signed"), []byte("signed"), 0600)
	mustRun(t, r, "add", "signed")
	mustRun(t, r, "commit", "-qm", "signed")
	mustRun(t, r, "verify-commit", "HEAD")
}
