package localworkspace

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/gitauth"
	"github.com/tysonthomas9/loomcli/internal/gitbranch"
)

type staticGitCredentialSource struct {
	username string
	password []byte
}

func (s staticGitCredentialSource) Resolve(context.Context, string) (*gitauth.Credential, error) {
	return &gitauth.Credential{
		Username: s.username,
		Password: s.password,
	}, nil
}

type mutableGitCredentialSource struct {
	mu       sync.Mutex
	password string
	calls    int
	issued   [][]byte
}

func (s *mutableGitCredentialSource) Resolve(context.Context, string) (*gitauth.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	password := []byte(s.password)
	s.issued = append(s.issued, password)
	return &gitauth.Credential{Username: "x-access-token", Password: password}, nil
}

func (s *mutableGitCredentialSource) rotate(password string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.password = password
}

func (s *mutableGitCredentialSource) snapshot() (int, [][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, append([][]byte(nil), s.issued...)
}

type dumbGitHTTPServer struct {
	mu                    sync.Mutex
	password              string
	allowAnonymous        bool
	anonymousRequests     int
	authenticatedRequests int
}

func (s *dumbGitHTTPServer) rotate(password string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.password = password
}

func (s *dumbGitHTTPServer) handler(remote string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, authenticated := r.BasicAuth()

		s.mu.Lock()
		allowAnonymous := s.allowAnonymous
		expectedPassword := s.password
		if authenticated {
			s.authenticatedRequests++
		} else {
			s.anonymousRequests++
		}
		s.mu.Unlock()

		if !allowAnonymous &&
			(!authenticated || username != "x-access-token" || password != expectedPassword) {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		const repoMarker = "/repo.git/"
		markerIndex := strings.Index(r.URL.Path, repoMarker)
		if markerIndex < 0 {
			http.NotFound(w, r)
			return
		}
		relative := r.URL.Path[markerIndex+len(repoMarker):]
		http.ServeFile(w, r, filepath.Join(remote, filepath.Clean(relative)))
	})
}

func (s *dumbGitHTTPServer) requestCounts() (anonymous, authenticated int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.anonymousRequests, s.authenticatedRequests
}

// newGitHubGitServer exposes handler as https://github.com/acme/repo.git
// through a loopback CONNECT proxy. This exercises the production helper's
// exact HTTPS github.com host binding without changing DNS or reaching the
// public network.
func newGitHubGitServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	certificate, caPEM := githubTestCertificate(t)
	tlsServer := httptest.NewUnstartedServer(handler)
	tlsServer.TLS = &tls.Config{ //nolint:gosec // test-only certificate and loopback server.
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
	}
	tlsServer.StartTLS()
	t.Cleanup(tlsServer.Close)

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		upstream, err := net.Dial("tcp", tlsServer.Listener.Addr().String())
		if err != nil {
			http.Error(w, "dial test git server", http.StatusBadGateway)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			_ = upstream.Close()
			http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
			return
		}
		client, buffered, err := hijacker.Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		if _, err := fmt.Fprint(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			_ = client.Close()
			_ = upstream.Close()
			return
		}
		go func() {
			_, _ = io.Copy(upstream, buffered)
			_ = upstream.Close()
		}()
		go func() {
			_, _ = io.Copy(client, upstream)
			_ = client.Close()
		}()
	}))
	t.Cleanup(proxy.Close)

	caPath := filepath.Join(t.TempDir(), "github-test-ca.pem")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatalf("write GitHub test CA: %v", err)
	}
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("https_proxy", proxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	t.Setenv("GIT_SSL_CAINFO", caPath)
	return "https://github.com/acme/repo.git"
}

func githubTestCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate test CA key: %v", err)
	}
	now := time.Now()
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Loom Git Test CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create test CA certificate: %v", err)
	}

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate test server key: %v", err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "github.com"},
		DNSNames:     []string{"github.com"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create test server certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(serverKey),
	})
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("load test server certificate: %v", err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	return certificate, caPEM
}

func TestCloneRepoToWithCredentialsAuthenticatesWithoutPersistingSecret(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	remote := filepath.Join(root, "repo.git")
	seed := filepath.Join(root, "seed")
	target := filepath.Join(root, "checkout")
	git(t, "", "init", "--bare", remote)
	git(t, "", "init", "-b", "main", seed)
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(seed, "README.md"), "private\n")
	git(t, seed, "add", "README.md")
	git(t, seed, "commit", "-m", "seed")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "main")
	git(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")
	git(t, remote, "update-server-info")

	const token = "github-private-test-token"
	serverState := &dumbGitHTTPServer{password: token}
	remoteURL := newGitHubGitServer(t, serverState.handler(remote))
	// Git's credential protocol preserves host case. Exercise the production
	// helper with a mixed-case URL so Source recognition and helper binding
	// cannot drift.
	remoteURL = strings.Replace(remoteURL, "https://github.com", "HTTPS://GITHUB.COM", 1)
	password := []byte(token)
	if err := CloneRepoToWithCredentials(
		context.Background(),
		remoteURL,
		target,
		staticGitCredentialSource{username: "x-access-token", password: password},
	); err != nil {
		t.Fatalf("authenticated clone: %v", err)
	}
	for i, value := range password {
		if value != 0 {
			t.Fatalf("password byte %d was not cleared", i)
		}
	}
	if got := gitOut(t, target, "remote", "get-url", "origin"); got != remoteURL {
		t.Fatalf("stored remote = %q, want token-free %q", got, remoteURL)
	}
	if got, err := gitMaybe(target, "config", "--local", "--get-all", "credential.helper"); err == nil || strings.TrimSpace(got) != "" {
		t.Fatalf("credential helper persisted in repo config: %q (err=%v)", got, err)
	}
	if got := gitOut(t, target, "show", "HEAD:README.md"); got != "private" {
		t.Fatalf("cloned content = %q, want private", got)
	}

	// A later task-worktree fetch resolves the credential again instead of
	// relying on clone-time config. This is the path UI-created agents use.
	writeFile(t, filepath.Join(seed, "README.md"), "private v2\n")
	git(t, seed, "add", "README.md")
	git(t, seed, "commit", "-m", "advance")
	git(t, seed, "push", "origin", "main")
	git(t, remote, "update-server-info")
	fetchPassword := []byte(token)
	worktree := filepath.Join(root, "task-worktree")
	if err := EnsureDetachedGitWorktreeFromBranchWithCredentials(
		context.Background(),
		target,
		worktree,
		"origin",
		"main",
		staticGitCredentialSource{username: "x-access-token", password: fetchPassword},
	); err != nil {
		t.Fatalf("authenticated follow-on fetch: %v", err)
	}
	if got := gitOut(t, worktree, "show", "HEAD:README.md"); got != "private v2" {
		t.Fatalf("fetched worktree content = %q, want private v2", got)
	}
	for i, value := range fetchPassword {
		if value != 0 {
			t.Fatalf("fetch password byte %d was not cleared", i)
		}
	}
}

func TestCloneRepoToWithCredentialsUsesAnonymousFirstForPublicRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	remote := filepath.Join(root, "repo.git")
	seed := filepath.Join(root, "seed")
	target := filepath.Join(root, "checkout")
	git(t, "", "init", "--bare", remote)
	git(t, "", "init", "-b", "main", seed)
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(seed, "README.md"), "public\n")
	git(t, seed, "add", "README.md")
	git(t, seed, "commit", "-m", "seed")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "main")
	git(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")
	git(t, remote, "update-server-info")

	serverState := &dumbGitHTTPServer{allowAnonymous: true}
	remoteURL := newGitHubGitServer(t, serverState.handler(remote))

	// This deliberately invalid saved token must neither be resolved nor sent
	// for a public read.
	source := &mutableGitCredentialSource{password: "expired-public-token"}
	if err := CloneRepoToWithCredentials(context.Background(), remoteURL, target, source); err != nil {
		t.Fatalf("anonymous public clone: %v", err)
	}
	if got := gitOut(t, target, "show", "HEAD:README.md"); got != "public" {
		t.Fatalf("cloned content = %q, want public", got)
	}
	if calls, _ := source.snapshot(); calls != 0 {
		t.Fatalf("credential source calls = %d, want 0 for public remote", calls)
	}
	anonymous, authenticated := serverState.requestCounts()
	if anonymous == 0 || authenticated != 0 {
		t.Fatalf("public request counts = anonymous:%d authenticated:%d, want anonymous-only", anonymous, authenticated)
	}
}

func TestCloneRepoToWithCredentialsRejectsURLSecretsBeforeGitOrSource(t *testing.T) {
	for _, remoteURL := range []string{
		"https://github.com/acme/repo.git?access_token=query-secret",
		"https://github.com/acme/repo.git#fragment-secret",
		"https://github.com/acme/repo.git?access_token=%zz-query-secret",
		"https://user:%zz-userinfo-secret@github.com/acme/repo.git",
	} {
		t.Run(remoteURL, func(t *testing.T) {
			source := &mutableGitCredentialSource{password: "must-not-be-resolved"}
			target := filepath.Join(t.TempDir(), "checkout")
			err := CloneRepoToWithCredentials(
				context.Background(),
				remoteURL,
				target,
				source,
			)
			if err == nil ||
				(!strings.Contains(err.Error(), "forbidden") &&
					!strings.Contains(err.Error(), "malformed")) {
				t.Fatalf("clone error = %v, want rejected URL secret", err)
			}
			if strings.Contains(err.Error(), "query-secret") ||
				strings.Contains(err.Error(), "fragment-secret") ||
				strings.Contains(err.Error(), "userinfo-secret") {
				t.Fatalf("clone error reflected URL secret: %v", err)
			}
			if calls, _ := source.snapshot(); calls != 0 {
				t.Fatalf("credential source calls = %d, want 0", calls)
			}
			if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
				t.Fatalf("target was materialized before URL rejection: %v", statErr)
			}
		})
	}
}

func TestRunGitForRemoteRejectsMutatingCredentialedRetry(t *testing.T) {
	source := &mutableGitCredentialSource{password: "must-not-be-resolved"}
	_, err := runGitForRemote(
		context.Background(),
		t.TempDir(),
		source,
		"https://github.com/acme/repo.git",
		"push", "origin", "main",
	)
	if err == nil || !strings.Contains(err.Error(), "not a read-only clone/fetch") {
		t.Fatalf("mutating credentialed operation error = %v", err)
	}
	if calls, _ := source.snapshot(); calls != 0 {
		t.Fatalf("credential source calls = %d, want 0 for rejected mutating operation", calls)
	}
}

func TestPrivatePRFetchAndReviewBaseUseRotatedCredential(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	remote := filepath.Join(root, "repo.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "repo")
	reviewWorktree := filepath.Join(root, "review", "pr-7")
	git(t, "", "init", "--bare", remote)
	git(t, "", "init", "-b", "main", seed)
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(seed, "base.txt"), "base\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "base")
	baseSHA := gitOut(t, seed, "rev-parse", "HEAD")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "HEAD:refs/heads/main")
	writeFile(t, filepath.Join(seed, "pr.txt"), "private PR\n")
	git(t, seed, "add", "pr.txt")
	git(t, seed, "commit", "-m", "private PR")
	headSHA := gitOut(t, seed, "rev-parse", "HEAD")
	git(t, seed, "push", "origin", "HEAD:refs/pull/7/head")
	git(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")
	git(t, remote, "update-server-info")

	const firstToken = "private-token-before-rotation"
	const rotatedToken = "private-token-after-rotation"
	serverState := &dumbGitHTTPServer{password: firstToken}
	remoteURL := newGitHubGitServer(t, serverState.handler(remote))
	source := &mutableGitCredentialSource{password: firstToken}

	if err := CloneRepoToWithCredentials(context.Background(), remoteURL, repo, source); err != nil {
		t.Fatalf("authenticated private clone: %v", err)
	}

	// Rotate both ends without restarting or reconstructing the Source. Each
	// subsequent fetch must resolve fresh authority rather than reusing the
	// clone-time token.
	serverState.rotate(rotatedToken)
	source.rotate(rotatedToken)
	gotHead, err := EnsureDetachedGitWorktreeAtPRHeadWithCredentials(
		context.Background(),
		repo,
		reviewWorktree,
		"origin",
		7,
		headSHA,
		source,
	)
	if err != nil {
		t.Fatalf("authenticated PR-head fetch after rotation: %v", err)
	}
	if gotHead != headSHA {
		t.Fatalf("PR-head fetch returned %q, want %q", gotHead, headSHA)
	}
	gotBase, err := RecordPRReviewContextWithCredentials(
		context.Background(),
		reviewWorktree,
		"origin",
		"main",
		map[string]string{"Pr": "7"},
		source,
	)
	if err != nil {
		t.Fatalf("authenticated review-base fetch after rotation: %v", err)
	}
	if gotBase != baseSHA {
		t.Fatalf("review base = %q, want %q", gotBase, baseSHA)
	}
	if diff := gitOut(t, reviewWorktree, "diff", gotBase+"...HEAD", "--name-only"); diff != "pr.txt" {
		t.Fatalf("review diff = %q, want pr.txt", diff)
	}

	calls, issued := source.snapshot()
	if calls != 3 {
		t.Fatalf("credential source calls = %d, want clone + PR-head + review-base", calls)
	}
	for call, password := range issued {
		for index, value := range password {
			if value != 0 {
				t.Fatalf("issued credential call %d byte %d was not cleared", call, index)
			}
		}
	}
	anonymous, authenticated := serverState.requestCounts()
	if anonymous < 3 || authenticated < 3 {
		t.Fatalf("private request counts = anonymous:%d authenticated:%d, want anonymous-first then authenticated retries", anonymous, authenticated)
	}
}

func TestRunGitWithCredentialRedactsFailureAndKeepsTokenOutOfArgv(t *testing.T) {
	fakeBin := t.TempDir()
	script := filepath.Join(fakeBin, "git")
	argsPath := script + ".args"
	envNamesPath := script + ".envnames"
	body := `#!/bin/sh
printf '%s\n' "$@" > "$0.args"
env | while IFS='=' read -r name value; do printf '%s\n' "$name"; done > "$0.envnames"
printf 'fatal: rejected password=%s\n' "$LOOM_PR_GIT_PASSWORD" >&2
exit 1
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OPENAI_API_KEY", "must-not-reach-git")
	t.Setenv("ANTHROPIC_API_KEY", "must-not-reach-git")
	t.Setenv("GITHUB_TOKEN", "must-not-reach-git")
	t.Setenv("LOOM_FLEET_DB_API_KEY", "must-not-reach-git")
	t.Setenv("LOOM_RUN_TOKEN", "must-not-reach-git")

	const token = "github-super-secret-token"
	password := []byte(token)
	_, err := runGitWithCredential(
		context.Background(),
		t.TempDir(),
		&gitauth.Credential{Username: "x-access-token", Password: password},
		"fetch", "origin", "main",
	)
	if err == nil {
		t.Fatal("runGitForRemote succeeded, want fake failure")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("git error leaked credential: %v", err)
	}
	if !strings.Contains(err.Error(), "password=***") {
		t.Fatalf("git error = %v, want redacted subprocess output", err)
	}
	args, readErr := os.ReadFile(argsPath)
	if readErr != nil {
		t.Fatalf("read fake git argv: %v", readErr)
	}
	if strings.Contains(string(args), token) {
		t.Fatalf("git argv leaked credential: %s", args)
	}
	if !strings.Contains(string(args), "credential.helper=") {
		t.Fatalf("git argv missing ephemeral credential helper: %s", args)
	}
	for _, reset := range []string{"core.askPass=", "http.extraHeader="} {
		if !strings.Contains(string(args), reset) {
			t.Fatalf("git argv missing %s reset: %s", reset, args)
		}
	}
	envNames, readErr := os.ReadFile(envNamesPath)
	if readErr != nil {
		t.Fatalf("read fake git environment names: %v", readErr)
	}
	names := make(map[string]bool)
	for _, name := range strings.Fields(string(envNames)) {
		names[name] = true
	}
	for _, forbidden := range []string{
		"OPENAI_API_KEY",
		"ANTHROPIC_API_KEY",
		"GITHUB_TOKEN",
		"LOOM_FLEET_DB_API_KEY",
		"LOOM_RUN_TOKEN",
	} {
		if names[forbidden] {
			t.Fatalf("credentialed git inherited forbidden environment name %s", forbidden)
		}
	}
	if !names[gitHTTPPasswordEnv] {
		t.Fatalf("credentialed git environment omitted %s", gitHTTPPasswordEnv)
	}
}

func TestRedactCredentialDoesNotAliasCombinedOutput(t *testing.T) {
	const token = "raw-output-secret"
	raw := []byte("failure " + token)
	credential := &gitauth.Credential{Password: []byte(token)}
	redacted := redactCredential(raw, credential)
	zeroBytes(raw)
	if got := string(redacted); got != "failure ***" {
		t.Fatalf("redacted output after raw overwrite = %q, want failure ***", got)
	}
}

func TestCredentialedStockGitCancellationIsBounded(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	const token = "cancellation-test-token"
	started := make(chan struct{})
	var startedOnce sync.Once
	remoteURL := newGitHubGitServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "x-access-token" || password != token {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		startedOnce.Do(func() { close(started) })
		<-r.Context().Done()
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	password := []byte(token)
	begin := time.Now()
	_, err := runGitForRemote(
		ctx,
		t.TempDir(),
		staticGitCredentialSource{username: "x-access-token", password: password},
		remoteURL,
		"ls-remote", "--", remoteURL,
	)
	elapsed := time.Since(begin)
	if err == nil {
		t.Fatal("credentialed git succeeded, want context cancellation")
	}
	select {
	case <-started:
	default:
		t.Fatal("credentialed request never reached the hanging server")
	}
	if elapsed > 4*time.Second {
		t.Fatalf("credentialed git cancellation took %s, want bounded by process-group kill/WaitDelay", elapsed)
	}
	for index, value := range password {
		if value != 0 {
			t.Fatalf("canceled credential byte %d was not cleared", index)
		}
	}
}

func TestEnsureGitWorktreeFromBranchUsesFetchedDefaultBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "repo")
	target := filepath.Join(root, "worktrees", "worker")

	git(t, "", "init", "--bare", remote)
	git(t, "", "init", seed)
	git(t, seed, "checkout", "-b", "main")
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(seed, "base.txt"), "v1\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "base")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "main")

	git(t, "", "clone", remote, repo)
	git(t, repo, "checkout", "main")

	writeFile(t, filepath.Join(seed, "base.txt"), "v2\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "advance")
	git(t, seed, "push", "origin", "main")

	if err := EnsureGitWorktreeFromBranch(repo, target, "worker", "origin", "main"); err != nil {
		t.Fatalf("EnsureGitWorktreeFromBranch() error = %v", err)
	}

	gotBytes, err := os.ReadFile(filepath.Join(target, "base.txt"))
	if err != nil {
		t.Fatalf("read target file: %v", err)
	}
	if got := string(gotBytes); got != "v2\n" {
		t.Fatalf("target base.txt = %q, want fetched v2", got)
	}
}

func TestEnsureGitWorktreeReusesExistingCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	target := filepath.Join(root, "worktrees", "worker")
	git(t, "", "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")

	if err := EnsureGitWorktree(repo, target, "worker"); err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	sentinel := filepath.Join(target, "uncommitted.txt")
	if err := os.WriteFile(sentinel, []byte("adopted\n"), 0o644); err != nil {
		t.Fatalf("write adoption sentinel: %v", err)
	}
	if err := EnsureGitWorktree(repo, target, "worker"); err != nil {
		t.Fatalf("reuse worktree: %v", err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "adopted\n" {
		t.Fatalf("reused worktree changed = %q err=%v", string(data), err)
	}
}

func TestRunGitHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runGit(ctx, t.TempDir(), "status")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runGit error = %v, want context canceled", err)
	}
}

func TestEnsureGitWorktreeFromBranchFallsBackToLocalDefaultBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "repo")
	target := filepath.Join(root, "worktrees", "worker")

	git(t, "", "init", "--bare", remote)
	git(t, "", "init", seed)
	git(t, seed, "checkout", "-b", "main")
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(seed, "base.txt"), "main\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "base")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "main")

	git(t, "", "clone", remote, repo)
	git(t, repo, "checkout", "-b", "browser-e2e")
	writeFile(t, filepath.Join(repo, "base.txt"), "local branch\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "local branch")

	if err := EnsureGitWorktreeFromBranch(repo, target, "worker", "origin", "browser-e2e"); err != nil {
		t.Fatalf("EnsureGitWorktreeFromBranch() error = %v", err)
	}

	gotBytes, err := os.ReadFile(filepath.Join(target, "base.txt"))
	if err != nil {
		t.Fatalf("read target file: %v", err)
	}
	if got := string(gotBytes); got != "local branch\n" {
		t.Fatalf("target base.txt = %q, want local branch content", got)
	}
}

func TestEnsureGitWorktreeFromBranchRecoversCorruptBranchRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	target := filepath.Join(root, "worktrees", "worker")

	git(t, "", "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "base.txt"), "main\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")
	git(t, repo, "checkout", "-b", "worker")
	writeFile(t, filepath.Join(repo, "worker.txt"), "worker\n")
	git(t, repo, "add", "worker.txt")
	git(t, repo, "commit", "-m", "worker")
	workerSHA := gitOut(t, repo, "rev-parse", "HEAD")
	git(t, repo, "checkout", "main")
	corruptLocalBranchRef(t, repo, "worker")

	if err := EnsureGitWorktreeFromBranch(repo, target, "worker", "", "main"); err != nil {
		t.Fatalf("EnsureGitWorktreeFromBranch() error = %v", err)
	}
	if got := gitOut(t, target, "rev-parse", "HEAD"); got != workerSHA {
		t.Fatalf("worktree HEAD = %s, want recovered reflog SHA %s", got, workerSHA)
	}
	if got := gitOut(t, target, "branch", "--show-current"); got != "worker" {
		t.Fatalf("worktree branch = %q, want worker", got)
	}
}

func TestGitRemoteURL(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	const url = "https://github.com/owner/repo.git"
	dir := t.TempDir()
	git(t, "", "init", dir)
	git(t, dir, "remote", "add", "origin", url)

	got, err := GitRemoteURL(dir, "origin")
	if err != nil {
		t.Fatalf("GitRemoteURL: %v", err)
	}
	if got != url {
		t.Errorf("GitRemoteURL = %q, want %q", got, url)
	}

	// Empty remote name defaults to origin.
	if got, err := GitRemoteURL(dir, ""); err != nil || got != url {
		t.Errorf("GitRemoteURL(\"\") = %q, %v; want %q", got, err, url)
	}

	// A non-git directory is reported as an error (the "not a usable checkout" signal).
	if _, err := GitRemoteURL(t.TempDir(), "origin"); err == nil {
		t.Error("GitRemoteURL on a non-git dir should return an error")
	}
}

func TestEnsureDetachedGitWorktreeAtPRHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "repo")
	target := filepath.Join(root, "pr-worktrees", "repo", "pr-7")

	git(t, "", "init", "--bare", remote)
	git(t, "", "init", seed)
	git(t, seed, "checkout", "-b", "main")
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(seed, "base.txt"), "base\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "base")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "HEAD:refs/heads/main")

	writeFile(t, filepath.Join(seed, "pr.txt"), "pr v1\n")
	git(t, seed, "add", "pr.txt")
	git(t, seed, "commit", "-m", "pr head")
	headSHA := gitOutput(t, seed, "rev-parse", "HEAD")
	git(t, seed, "push", "origin", "HEAD:refs/pull/7/head")

	git(t, "", "clone", remote, repo)
	git(t, repo, "checkout", "main")

	gotSHA, err := EnsureDetachedGitWorktreeAtPRHead(context.Background(), repo, target, "origin", 7, headSHA)
	if err != nil {
		t.Fatalf("EnsureDetachedGitWorktreeAtPRHead() create error = %v", err)
	}
	if gotSHA != headSHA {
		t.Fatalf("create returned sha = %s, want %s", gotSHA, headSHA)
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
		t.Fatalf("target .git does not exist: %v", err)
	}
	if got := gitOutput(t, target, "rev-parse", "HEAD"); got != headSHA {
		t.Fatalf("target HEAD = %s, want %s", got, headSHA)
	}
	if out, err := gitMaybe(target, "symbolic-ref", "-q", "HEAD"); err == nil {
		t.Fatalf("target HEAD is attached to %q, want detached", strings.TrimSpace(out))
	}

	// Clean-tree cache hit: a re-ensure with no changes is a no-op at the same sha.
	if gotSHA, err := EnsureDetachedGitWorktreeAtPRHead(context.Background(), repo, target, "origin", 7, headSHA); err != nil {
		t.Fatalf("EnsureDetachedGitWorktreeAtPRHead() clean cache hit error = %v", err)
	} else if gotSHA != headSHA {
		t.Fatalf("clean cache hit returned sha = %s, want %s", gotSHA, headSHA)
	}
	if got := gitOutput(t, target, "rev-parse", "HEAD"); got != headSHA {
		t.Fatalf("target HEAD after clean cache hit = %s, want %s", got, headSHA)
	}

	// Pristine guarantee: an untracked file at the right sha is scrubbed, not
	// handed back — a review checkout must faithfully match the PR head.
	sentinel := filepath.Join(target, "cache-hit-sentinel.txt")
	writeFile(t, sentinel, "cruft\n")
	if _, err := EnsureDetachedGitWorktreeAtPRHead(context.Background(), repo, target, "origin", 7, headSHA); err != nil {
		t.Fatalf("EnsureDetachedGitWorktreeAtPRHead() pristine scrub error = %v", err)
	}
	if got := gitOutput(t, target, "rev-parse", "HEAD"); got != headSHA {
		t.Fatalf("target HEAD after pristine scrub = %s, want %s", got, headSHA)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("untracked sentinel survived a re-ensure (err=%v), want it scrubbed", err)
	}

	git(t, target, "reset", "--hard", "HEAD~1")
	if _, err := EnsureDetachedGitWorktreeAtPRHead(context.Background(), repo, target, "origin", 7, headSHA); err != nil {
		t.Fatalf("EnsureDetachedGitWorktreeAtPRHead() drift repair error = %v", err)
	}
	if got := gitOutput(t, target, "rev-parse", "HEAD"); got != headSHA {
		t.Fatalf("target HEAD after drift repair = %s, want %s", got, headSHA)
	}

	writeFile(t, filepath.Join(seed, "pr.txt"), "pr v2\n")
	git(t, seed, "add", "pr.txt")
	git(t, seed, "commit", "-m", "advance pr head")
	newHeadSHA := gitOutput(t, seed, "rev-parse", "HEAD")
	git(t, seed, "push", "--force", "origin", "HEAD:refs/pull/7/head")

	gotSHA, err = EnsureDetachedGitWorktreeAtPRHead(context.Background(), repo, target, "origin", 7, newHeadSHA)
	if err != nil {
		t.Fatalf("EnsureDetachedGitWorktreeAtPRHead() advance error = %v", err)
	}
	if gotSHA != newHeadSHA {
		t.Fatalf("advance returned sha = %s, want %s", gotSHA, newHeadSHA)
	}
	if got := gitOutput(t, target, "rev-parse", "HEAD"); got != newHeadSHA {
		t.Fatalf("target HEAD after advance = %s, want %s", got, newHeadSHA)
	}
}

func TestEnsureDetachedGitWorktreeAtPRHeadRejectsFastForwardedTip(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "repo")
	target := filepath.Join(root, "pr-worktrees", "repo", "pr-7")

	git(t, "", "init", "--bare", remote)
	git(t, "", "init", seed)
	git(t, seed, "checkout", "-b", "main")
	git(t, seed, "config", "user.name", "Test User")
	git(t, seed, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(seed, "pr.txt"), "A\n")
	git(t, seed, "add", "pr.txt")
	git(t, seed, "commit", "-m", "PR head A")
	headA := gitOutput(t, seed, "rev-parse", "HEAD")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "HEAD:refs/heads/main")
	git(t, seed, "push", "origin", "HEAD:refs/pull/7/head")

	git(t, "", "clone", remote, repo)
	git(t, repo, "checkout", "main")
	if _, err := EnsureDetachedGitWorktreeAtPRHead(context.Background(), repo, target, "origin", 7, headA); err != nil {
		t.Fatalf("materialize head A: %v", err)
	}
	sentinel := filepath.Join(target, "stale-sentinel.txt")
	writeFile(t, sentinel, "leave untouched\n")

	writeFile(t, filepath.Join(seed, "pr.txt"), "B\n")
	git(t, seed, "add", "pr.txt")
	git(t, seed, "commit", "-m", "PR head B")
	headB := gitOutput(t, seed, "rev-parse", "HEAD")
	git(t, seed, "push", "origin", "HEAD:refs/pull/7/head")

	gotTip, err := EnsureDetachedGitWorktreeAtPRHead(context.Background(), repo, target, "origin", 7, " "+strings.ToUpper(headA)+" ")
	var changed *PRHeadChangedError
	if !errors.As(err, &changed) {
		t.Fatalf("stale ensure error = %v, want PRHeadChangedError", err)
	}
	if gotTip != headB || changed.TipSHA != headB {
		t.Fatalf("stale tip = returned:%q error:%q, want %q", gotTip, changed.TipSHA, headB)
	}
	if !strings.EqualFold(strings.TrimSpace(changed.ExpectedSHA), headA) {
		t.Fatalf("stale expected sha = %q, want %q", changed.ExpectedSHA, headA)
	}
	if got := gitOutput(t, target, "rev-parse", "HEAD"); got != headA {
		t.Fatalf("target HEAD after stale outcome = %s, want untouched %s", got, headA)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("stale outcome scrubbed existing worktree: %v", err)
	}

	gotSHA, err := EnsureDetachedGitWorktreeAtPRHead(context.Background(), repo, target, "origin", 7, "\n"+strings.ToUpper(headB)+"\t")
	if err != nil {
		t.Fatalf("ensure expected head B: %v", err)
	}
	if gotSHA != headB {
		t.Fatalf("expected-B ensure returned %q, want %q", gotSHA, headB)
	}
	if got := gitOutput(t, target, "rev-parse", "HEAD"); got != headB {
		t.Fatalf("target HEAD after expected-B ensure = %s, want %s", got, headB)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("expected-B ensure did not scrub sentinel (err=%v)", err)
	}
}

func TestPRHeadReviewWorktreePath(t *testing.T) {
	root := t.TempDir()
	got, err := PRReviewWorktreePath(root, "repo", 7)
	if err != nil {
		t.Fatalf("PRReviewWorktreePath() error = %v", err)
	}
	want := filepath.Join(root, ".loom", "pr-worktrees", "repo", "pr-7")
	if got != want {
		t.Fatalf("PRReviewWorktreePath() = %q, want %q", got, want)
	}
	if !PathContains(root, got) {
		t.Fatalf("PRReviewWorktreePath() = %q, want under %q", got, root)
	}

	if _, err := PRReviewWorktreePath("", "repo", 7); err == nil {
		t.Fatal("PRReviewWorktreePath() with empty workspace path returned nil error")
	}
	if _, err := PRReviewWorktreePath(root, "", 7); err == nil {
		t.Fatal("PRReviewWorktreePath() with empty repo name returned nil error")
	}
	if _, err := PRReviewWorktreePath(root, "repo", 0); err == nil {
		t.Fatal("PRReviewWorktreePath() with zero PR number returned nil error")
	}
	if _, err := PRReviewWorktreePath(root, "repo", -1); err == nil {
		t.Fatal("PRReviewWorktreePath() with negative PR number returned nil error")
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // fixed test helper commands.
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitMaybe(dir, args...)
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out)
}

func gitMaybe(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:norawexec,gosec // fixed test helper commands.
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitMaybe(dir, args...)
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out)
}

func corruptLocalBranchRef(t *testing.T, repoPath, branch string) {
	t.Helper()
	common, err := gitbranch.CommonDir(repoPath)
	if err != nil {
		t.Fatalf("git common dir: %v", err)
	}
	refPath := filepath.Join(common, "refs", "heads", filepath.FromSlash(branch))
	if err := os.MkdirAll(filepath.Dir(refPath), 0o755); err != nil {
		t.Fatalf("mkdir branch ref parent: %v", err)
	}
	if err := os.WriteFile(refPath, nil, 0o644); err != nil {
		t.Fatalf("corrupt branch ref: %v", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestRecordPRReviewContext(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "repo")
	target := filepath.Join(root, "wt", "pr-7")

	git(t, "", "init", "--bare", remote)
	git(t, "", "init", seed)
	git(t, seed, "checkout", "-b", "main")
	git(t, seed, "config", "user.name", "T")
	git(t, seed, "config", "user.email", "t@t")
	writeFile(t, filepath.Join(seed, "base.txt"), "base\n")
	git(t, seed, "add", "base.txt")
	git(t, seed, "commit", "-m", "base")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "origin", "HEAD:refs/heads/main")
	baseSHA := gitOutput(t, seed, "rev-parse", "HEAD")
	// A PR head commit on top of base.
	writeFile(t, filepath.Join(seed, "pr.txt"), "pr\n")
	git(t, seed, "add", "pr.txt")
	git(t, seed, "commit", "-m", "pr head")
	prHeadSHA := gitOutput(t, seed, "rev-parse", "HEAD")
	git(t, seed, "push", "origin", "HEAD:refs/pull/7/head")

	git(t, "", "clone", remote, repo)
	if _, err := EnsureDetachedGitWorktreeAtPRHead(context.Background(), repo, target, "origin", 7, prHeadSHA); err != nil {
		t.Fatalf("worktree: %v", err)
	}

	got, err := RecordPRReviewContext(context.Background(), target, "origin", "main", map[string]string{"Pr": "7", "Title": "Add X"})
	if err != nil {
		t.Fatalf("RecordPRReviewContext: %v", err)
	}
	if got != baseSHA {
		t.Fatalf("returned base = %s, want %s", got, baseSHA)
	}
	// Recorded per-worktree, readable, and the review diff shows the PR change.
	if rec := strings.TrimSpace(gitOutput(t, target, "config", "loom.reviewBase")); rec != baseSHA {
		t.Fatalf("loom.reviewBase = %s, want %s", rec, baseSHA)
	}
	if diff := gitOutput(t, target, "diff", baseSHA+"...HEAD", "--name-only"); !strings.Contains(diff, "pr.txt") {
		t.Fatalf("review diff = %q, want pr.txt", diff)
	}
	if pr := strings.TrimSpace(gitOutput(t, target, "config", "loom.reviewPr")); pr != "7" {
		t.Fatalf("loom.reviewPr = %q, want 7", pr)
	}
}
