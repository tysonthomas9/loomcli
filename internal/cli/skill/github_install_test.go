package skill

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
)

func TestParseGitHubSource(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    githubSource
		wantErr string
	}{
		{
			name: "shorthand repository",
			raw:  "owner/repo",
			want: githubSource{Owner: "owner", Repo: "repo", Ref: "HEAD"},
		},
		{
			name: "shorthand subpath",
			raw:  "owner/repo/sub/path",
			want: githubSource{Owner: "owner", Repo: "repo", Ref: "HEAD", Subpath: "sub/path"},
		},
		{
			name: "shorthand ref suffix",
			raw:  "owner/repo@release/v1",
			want: githubSource{Owner: "owner", Repo: "repo", Ref: "release/v1"},
		},
		{
			name: "shorthand subpath and ref suffix",
			raw:  "owner/repo/sub/path@abc123",
			want: githubSource{Owner: "owner", Repo: "repo", Ref: "abc123", Subpath: "sub/path"},
		},
		{
			name: "GitHub URL repository",
			raw:  "https://github.com/owner/repo",
			want: githubSource{Owner: "owner", Repo: "repo", Ref: "HEAD"},
		},
		{
			name: "GitHub URL ref suffix",
			raw:  "https://github.com/owner/repo@v2.1.0",
			want: githubSource{Owner: "owner", Repo: "repo", Ref: "v2.1.0"},
		},
		{
			name: "GitHub tree URL",
			raw:  "https://github.com/owner/repo/tree/main/sub/path",
			want: githubSource{Owner: "owner", Repo: "repo", Ref: "main", Subpath: "sub/path", TreeURL: true},
		},
		{
			name: "GitHub tree URL overridden by suffix",
			raw:  "https://github.com/owner/repo/tree/main/sub/path@v3",
			want: githubSource{Owner: "owner", Repo: "repo", Ref: "v3", Subpath: "sub/path", TreeURL: true},
		},
		{
			name: "tree URL greedily treats one segment as ref",
			raw:  "https://github.com/owner/repo/tree/feature/kimi-cli/examples",
			want: githubSource{Owner: "owner", Repo: "repo", Ref: "feature", Subpath: "kimi-cli/examples", TreeURL: true},
		},
		{
			name: "scheme-less GitHub URL",
			raw:  "github.com/owner/repo",
			want: githubSource{Owner: "owner", Repo: "repo", Ref: "HEAD"},
		},
		{
			name: "URL strips dot git suffix",
			raw:  "https://github.com/owner/repo.git",
			want: githubSource{Owner: "owner", Repo: "repo", Ref: "HEAD"},
		},
		{
			name: "shorthand strips dot git suffix",
			raw:  "owner/repo.git/sub/path",
			want: githubSource{Owner: "owner", Repo: "repo", Ref: "HEAD", Subpath: "sub/path"},
		},
		{name: "empty", raw: "", wantErr: "must not be empty"},
		{name: "owner only", raw: "owner", wantErr: "owner/repo"},
		{name: "non GitHub URL", raw: "https://gitlab.com/owner/repo", wantErr: "https://github.com"},
		{name: "empty suffix ref", raw: "owner/repo@", wantErr: "non-empty ref"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGitHubSource(tt.raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseGitHubSource(%q) error = %v, want substring %q", tt.raw, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseGitHubSource(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("parseGitHubSource(%q) = %+v, want %+v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestSkillInstallCommandFetchesAndCreates(t *testing.T) {
	withoutAgentName(t)
	t.Setenv("GITHUB_TOKEN", "fixture-token")
	tarball := buildSkillTarball(t,
		tarFixtureEntry{name: "repo-main/", kind: tar.TypeDir, mode: 0o755},
		tarFixtureEntry{
			name: "repo-main/SKILL.md",
			mode: 0o644,
			body: "---\nname: fetched-skill\ndescription: Use this fetched skill\nlicense: MIT\nmetadata: fixture\n---\n# Fetched body\n",
		},
		tarFixtureEntry{
			name: "repo-main/scripts/check.sh",
			mode: 0o755,
			body: "#!/bin/sh\necho checked\n",
		},
	)
	requests := 0
	server := serveTarball(t, tarball, func(r *http.Request) {
		requests++
		if r.Method != http.MethodGet {
			t.Errorf("request method = %s, want GET", r.Method)
		}
		if r.URL.EscapedPath() != "/owner/repo/tar.gz/HEAD" {
			t.Errorf("request path = %q, want codeload path", r.URL.EscapedPath())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer fixture-token" {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
	})

	originalInstaller := skillGitHubInstaller
	skillGitHubInstaller = githubSkillInstaller{HTTPClient: server.Client(), CodeloadBaseURL: server.URL}
	t.Cleanup(func() { skillGitHubInstaller = originalInstaller })
	st := memstore.New()
	withSkillCommandStore(t, st)

	out, err := executeSkillCommand(t, "", "install", "owner/repo")
	if err != nil {
		t.Fatalf("install skill: %v", err)
	}
	if !strings.Contains(out, "Installed skill TEST/workspace:fetched-skill") {
		t.Fatalf("install output = %q", out)
	}
	if !strings.Contains(out, "Notice: dropped SKILL.md frontmatter keys: license, metadata") {
		t.Fatalf("install output is missing dropped-key notice: %q", out)
	}
	if requests != 1 {
		t.Fatalf("codeload requests = %d, want exactly 1", requests)
	}

	sk, err := st.Skills().Get(context.Background(), testWorkspace, domain.WorkspaceSkillRef("fetched-skill"))
	if err != nil {
		t.Fatalf("get installed skill: %v", err)
	}
	if sk.Description != "Use this fetched skill" || sk.Content != "# Fetched body\n" {
		t.Fatalf("installed skill = %+v", sk)
	}
	if sk.Source != "github.com/owner/repo@HEAD" || sk.SourceRef != "" {
		t.Fatalf("installed provenance = source %q source_ref %q", sk.Source, sk.SourceRef)
	}
	if len(sk.Files) != 1 || sk.Files[0].Path != "scripts/check.sh" || !sk.Files[0].Executable || sk.Files[0].Content != "#!/bin/sh\necho checked\n" {
		t.Fatalf("installed files = %+v", sk.Files)
	}

	_, collisionErr := executeSkillCommand(t, "", "install", "owner/repo")
	if collisionErr == nil {
		t.Fatal("second install error = nil, want collision")
	}
	for _, want := range []string{"already exists", "--name <new-name>", "loom skill update fetched-skill --scope workspace"} {
		if !strings.Contains(collisionErr.Error(), want) {
			t.Errorf("collision error %q does not contain %q", collisionErr, want)
		}
	}
}

func TestGitHubSkillInstallerSelectsExplicitSubpath(t *testing.T) {
	tarball := buildSkillTarball(t,
		tarFixtureEntry{name: "repo-v1/skills/one/SKILL.md", mode: 0o644, body: skillDocument("one", "First skill")},
		tarFixtureEntry{name: "repo-v1/skills/one/outside-link", kind: tar.TypeSymlink, mode: 0o777, link: "target"},
		tarFixtureEntry{name: "repo-v1/skills/two/SKILL.md", mode: 0o644, body: "---\ndescription: Second skill\n---\nsecond body\n"},
		tarFixtureEntry{name: "repo-v1/skills/two/guide.txt", mode: 0o644, body: "guide\n"},
	)
	server := serveTarball(t, tarball, func(r *http.Request) {
		if r.URL.EscapedPath() != "/owner/repo/tar.gz/v1" {
			t.Errorf("request path = %q, want v1 archive", r.URL.EscapedPath())
		}
	})
	installer := githubSkillInstaller{HTTPClient: server.Client(), CodeloadBaseURL: server.URL}

	got, err := installer.Fetch(context.Background(), "owner/repo/skills/two@v1", "renamed-skill")
	if err != nil {
		t.Fatalf("fetch explicit subpath: %v", err)
	}
	if got.Name != "renamed-skill" || got.Description != "Second skill" || got.Content != "second body\n" {
		t.Fatalf("fetched skill = %+v", got)
	}
	if got.Source != "github.com/owner/repo/skills/two@v1" {
		t.Fatalf("source = %q", got.Source)
	}
	if len(got.Files) != 1 || got.Files[0].Path != "guide.txt" {
		t.Fatalf("files = %+v", got.Files)
	}
}

// Real codeload tarballs (git archive output) lead with a pax_global_header
// entry that archive/tar hands back to the caller; treating it as a top-level
// path rejected every genuine GitHub archive while in-code fixtures passed.
func TestGitHubSkillInstallerSkipsPaxGlobalHeader(t *testing.T) {
	tarball := buildSkillTarball(t,
		tarFixtureEntry{name: "pax_global_header", kind: tar.TypeXGlobalHeader},
		tarFixtureEntry{name: "repo-HEAD/SKILL.md", mode: 0o644, body: skillDocument("pax-skill", "Pax global header handling")},
	)
	server := serveTarball(t, tarball, nil)
	installer := githubSkillInstaller{HTTPClient: server.Client(), CodeloadBaseURL: server.URL}

	got, err := installer.Fetch(context.Background(), "owner/repo", "")
	if err != nil {
		t.Fatalf("fetch archive with pax_global_header: %v", err)
	}
	if got.Name != "pax-skill" {
		t.Fatalf("fetched skill name = %q, want pax-skill", got.Name)
	}
}

func TestGitHubSkillInstallerReportsMultipleCandidates(t *testing.T) {
	tarball := buildSkillTarball(t,
		tarFixtureEntry{name: "repo-main/skills/alpha/SKILL.md", mode: 0o644, body: skillDocument("alpha", "Alpha")},
		tarFixtureEntry{name: "repo-main/skills/beta/SKILL.md", mode: 0o644, body: skillDocument("beta", "Beta")},
	)
	server := serveTarball(t, tarball, nil)
	installer := githubSkillInstaller{HTTPClient: server.Client(), CodeloadBaseURL: server.URL}

	_, err := installer.Fetch(context.Background(), "owner/repo", "")
	if err == nil {
		t.Fatal("multiple candidate error = nil")
	}
	for _, want := range []string{"multiple skills", "skills/alpha", "skills/beta", "pass one of these skill subpaths"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("multiple candidate error %q does not contain %q", err, want)
		}
	}
}

func TestGitHubSkillInstallerReportsRootAndDeeperCandidates(t *testing.T) {
	tarball := buildSkillTarball(t,
		tarFixtureEntry{name: "repo-main/SKILL.md", mode: 0o644, body: skillDocument("root-skill", "Root")},
		tarFixtureEntry{name: "repo-main/skills/nested/SKILL.md", mode: 0o644, body: skillDocument("nested", "Nested")},
	)
	server := serveTarball(t, tarball, nil)
	installer := githubSkillInstaller{HTTPClient: server.Client(), CodeloadBaseURL: server.URL}

	_, err := installer.Fetch(context.Background(), "owner/repo", "")
	if err == nil {
		t.Fatal("root plus deeper candidate error = nil")
	}
	for _, want := range []string{"multiple skills", "\n  .\n", "skills/nested"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("root plus deeper candidate error %q does not contain %q", err, want)
		}
	}
}

func TestGitHubSkillInstallerRejectsDecodedBinaryFrontmatter(t *testing.T) {
	for _, tt := range []struct {
		name        string
		frontmatter string
		want        string
	}{
		{
			name:        "description",
			frontmatter: "name: safe-name\ndescription: !!binary AA==",
			want:        "frontmatter description",
		},
		{
			name:        "name",
			frontmatter: "name: !!binary Zm9vAA==\ndescription: Safe description",
			want:        "frontmatter name",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tarball := buildSkillTarball(t,
				tarFixtureEntry{name: "repo-main/SKILL.md", mode: 0o644, body: "---\n" + tt.frontmatter + "\n---\nbody\n"},
			)
			server := serveTarball(t, tarball, nil)
			installer := githubSkillInstaller{HTTPClient: server.Client(), CodeloadBaseURL: server.URL}

			_, err := installer.Fetch(context.Background(), "owner/repo", "")
			if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "control") {
				t.Fatalf("decoded binary %s error = %v", tt.name, err)
			}
		})
	}
}

func TestGitHubSkillInstallerAllowsMultilineDescription(t *testing.T) {
	tarball := buildSkillTarball(t,
		tarFixtureEntry{
			name: "repo-main/SKILL.md",
			mode: 0o644,
			body: "---\nname: multiline\ndescription: |-\n  First line\n  Second line\n---\nbody\n",
		},
	)
	server := serveTarball(t, tarball, nil)
	installer := githubSkillInstaller{HTTPClient: server.Client(), CodeloadBaseURL: server.URL}

	got, err := installer.Fetch(context.Background(), "owner/repo", "")
	if err != nil {
		t.Fatalf("fetch multiline description: %v", err)
	}
	if got.Description != "First line\nSecond line" {
		t.Fatalf("description = %q, want preserved newlines", got.Description)
	}
}

func TestGitHubTreeURLFailuresSuggestRefSuffix(t *testing.T) {
	t.Run("missing subpath", func(t *testing.T) {
		tarball := buildSkillTarball(t,
			tarFixtureEntry{name: "repo-feature/SKILL.md", mode: 0o644, body: skillDocument("root-skill", "Root")},
		)
		server := serveTarball(t, tarball, nil)
		installer := githubSkillInstaller{HTTPClient: server.Client(), CodeloadBaseURL: server.URL}

		_, err := installer.Fetch(context.Background(), "https://github.com/owner/repo/tree/feature/kimi-cli/examples", "")
		if err == nil || !strings.Contains(err.Error(), "branches containing \"/\"") || !strings.Contains(err.Error(), "@<ref>") {
			t.Fatalf("tree missing-subpath error = %v", err)
		}
	})

	t.Run("codeload not found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "not found", http.StatusNotFound)
		}))
		t.Cleanup(server.Close)
		installer := githubSkillInstaller{HTTPClient: server.Client(), CodeloadBaseURL: server.URL}

		_, err := installer.Fetch(context.Background(), "https://github.com/owner/repo/tree/feature/kimi-cli/examples", "")
		if err == nil || !strings.Contains(err.Error(), "branches containing \"/\"") || !strings.Contains(err.Error(), "@<ref>") {
			t.Fatalf("tree codeload-404 error = %v", err)
		}
	})
}

func TestGitHubSkillInstallerRefusesOffHostRedirect(t *testing.T) {
	tarball := buildSkillTarball(t,
		tarFixtureEntry{name: "repo-main/SKILL.md", mode: 0o644, body: skillDocument("redirected", "Redirected")},
	)
	destinationRequests := 0
	destination := serveTarball(t, tarball, func(*http.Request) { destinationRequests++ })
	offHostURL := strings.Replace(destination.URL, "127.0.0.1", "localhost", 1)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, offHostURL, http.StatusFound)
	}))
	t.Cleanup(origin.Close)
	installer := githubSkillInstaller{CodeloadBaseURL: origin.URL}

	_, err := installer.Fetch(context.Background(), "owner/repo", "")
	if err == nil || !strings.Contains(err.Error(), "refusing redirect") {
		t.Fatalf("off-host redirect error = %v", err)
	}
	if destinationRequests != 0 {
		t.Fatalf("off-host destination requests = %d, want 0", destinationRequests)
	}
}

func TestGitHubSkillInstallerOmitsEmptyTokenHeader(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	tarball := buildSkillTarball(t,
		tarFixtureEntry{name: "repo-main/SKILL.md", mode: 0o644, body: skillDocument("empty-token", "Empty token")},
	)
	server := serveTarball(t, tarball, func(r *http.Request) {
		if _, present := r.Header["Authorization"]; present {
			t.Errorf("empty GITHUB_TOKEN sent Authorization header %q", r.Header.Get("Authorization"))
		}
	})
	installer := githubSkillInstaller{HTTPClient: server.Client(), CodeloadBaseURL: server.URL}

	if _, err := installer.Fetch(context.Background(), "owner/repo", ""); err != nil {
		t.Fatalf("fetch with empty token: %v", err)
	}
}

func TestGitHubSkillInstallerRejectsBinaryFiles(t *testing.T) {
	tarball := buildSkillTarball(t,
		tarFixtureEntry{name: "repo-main/tool/SKILL.md", mode: 0o644, body: skillDocument("tool", "Tool")},
		tarFixtureEntry{name: "repo-main/tool/assets/nul.dat", mode: 0o644, body: "has\x00nul"},
		tarFixtureEntry{name: "repo-main/tool/assets/utf8.dat", mode: 0o644, data: []byte{0xff, 0xfe}},
	)
	server := serveTarball(t, tarball, nil)
	installer := githubSkillInstaller{HTTPClient: server.Client(), CodeloadBaseURL: server.URL}

	_, err := installer.Fetch(context.Background(), "owner/repo", "")
	if err == nil {
		t.Fatal("binary file error = nil")
	}
	for _, want := range []string{"binary content is not supported", "assets/nul.dat", "assets/utf8.dat"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("binary error %q does not contain %q", err, want)
		}
	}
}

func TestGitHubSkillInstallerRejectsSelectedSymlink(t *testing.T) {
	tarball := buildSkillTarball(t,
		tarFixtureEntry{name: "repo-main/tool/SKILL.md", mode: 0o644, body: skillDocument("tool", "Tool")},
		tarFixtureEntry{name: "repo-main/tool/references/latest", kind: tar.TypeSymlink, mode: 0o777, link: "guide.md"},
	)
	server := serveTarball(t, tarball, nil)
	installer := githubSkillInstaller{HTTPClient: server.Client(), CodeloadBaseURL: server.URL}

	_, err := installer.Fetch(context.Background(), "owner/repo", "")
	if err == nil || !strings.Contains(err.Error(), "references/latest (symlink)") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestGitHubSkillInstallerRejectsSizeCaps(t *testing.T) {
	tarball := buildSkillTarball(t,
		tarFixtureEntry{name: "repo-main/SKILL.md", mode: 0o644, body: skillDocument("root-skill", "Root")},
	)
	for _, tt := range []struct {
		name      string
		installer func(*httptest.Server) githubSkillInstaller
		want      string
	}{
		{
			name: "compressed",
			installer: func(server *httptest.Server) githubSkillInstaller {
				return githubSkillInstaller{HTTPClient: server.Client(), CodeloadBaseURL: server.URL, MaxCompressedBytes: int64(len(tarball) - 1)}
			},
			want: "compressed GitHub archive exceeds",
		},
		{
			name: "decompressed",
			installer: func(server *httptest.Server) githubSkillInstaller {
				return githubSkillInstaller{HTTPClient: server.Client(), CodeloadBaseURL: server.URL, MaxDecompressedBytes: 64}
			},
			want: "decompressed GitHub archive exceeds",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := serveTarball(t, tarball, nil)
			_, err := tt.installer(server).Fetch(context.Background(), "owner/repo", "")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("size cap error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestGitHubSkillInstallerRejectsSparseLogicalSize(t *testing.T) {
	const logicalSize = int64(1 << 30)
	tarball := buildPAXSparseTarball(t, logicalSize)
	assertPAXSparseLogicalSize(t, tarball, logicalSize)
	server := serveTarball(t, tarball, nil)
	installer := githubSkillInstaller{
		HTTPClient:           server.Client(),
		CodeloadBaseURL:      server.URL,
		MaxDecompressedBytes: 16 << 10,
	}

	_, err := installer.Fetch(context.Background(), "owner/repo", "")
	if err == nil || !strings.Contains(err.Error(), "logical file content exceeds") {
		t.Fatalf("sparse logical size error = %v", err)
	}
}

func TestReadGitHubTarSharesLogicalBudgetAcrossFiles(t *testing.T) {
	document := skillDocument("budgeted", "Budgeted")
	guide := "guide body\n"
	tarball := buildSkillTarball(t,
		tarFixtureEntry{name: "repo-main/SKILL.md", mode: 0o644, body: document},
		tarFixtureEntry{name: "repo-main/guide.txt", mode: 0o644, body: guide},
	)
	decompressed, err := readGzipArchive(tarball, 1<<20)
	if err != nil {
		t.Fatalf("decompress fixture: %v", err)
	}
	logicalBudget := int64(len(document) + len(guide) - 1)

	_, err = readGitHubTar(decompressed, logicalBudget)
	if err == nil || !strings.Contains(err.Error(), "logical file content exceeds") || !strings.Contains(err.Error(), "remaining") {
		t.Fatalf("shared logical budget error = %v", err)
	}
}

func TestGitHubSkillInstallerRejectsTraversalEntry(t *testing.T) {
	tarball := buildSkillTarball(t,
		tarFixtureEntry{name: "repo-main/SKILL.md", mode: 0o644, body: skillDocument("root-skill", "Root")},
		tarFixtureEntry{name: "repo-main/../escape.txt", mode: 0o644, body: "escape"},
	)
	server := serveTarball(t, tarball, nil)
	installer := githubSkillInstaller{HTTPClient: server.Client(), CodeloadBaseURL: server.URL}

	_, err := installer.Fetch(context.Background(), "owner/repo", "")
	if err == nil || !strings.Contains(err.Error(), ".. components are forbidden") {
		t.Fatalf("traversal error = %v", err)
	}
}

func TestRunSkillInstallRefusesAgentWriteBeforeFetch(t *testing.T) {
	t.Setenv("LOOM_AGENT_NAME", "task-worker-1")
	cmd := newSkillInstallCommand()
	err := runSkillInstall(cmd, "owner/repo", skillInstallFlags{scope: "workspace"})
	if err == nil || !strings.Contains(err.Error(), "agent-initiated skill writes are deferred") {
		t.Fatalf("run install refusal = %v", err)
	}
}

type tarFixtureEntry struct {
	name string
	kind byte
	mode int64
	body string
	data []byte
	link string
}

func buildSkillTarball(t *testing.T, entries ...tarFixtureEntry) []byte {
	t.Helper()
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(zw)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		data := entry.data
		if data == nil {
			data = []byte(entry.body)
		}
		header := &tar.Header{
			Name:     entry.name,
			Typeflag: kind,
			Mode:     entry.mode,
			Linkname: entry.link,
		}
		if kind == tar.TypeReg || kind == tar.TypeRegA {
			header.Size = int64(len(data))
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatalf("write tar header %q: %v", entry.name, err)
		}
		if header.Size > 0 {
			if _, err := tw.Write(data); err != nil {
				t.Fatalf("write tar body %q: %v", entry.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar fixture: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close gzip fixture: %v", err)
	}
	return compressed.Bytes()
}

func buildPAXSparseTarball(t *testing.T, logicalSize int64) []byte {
	t.Helper()
	replacements := map[string]string{
		strings.Repeat("a", len("GNU.sparse.major")):    "GNU.sparse.major",
		strings.Repeat("b", len("GNU.sparse.minor")):    "GNU.sparse.minor",
		strings.Repeat("c", len("GNU.sparse.name")):     "GNU.sparse.name",
		strings.Repeat("d", len("GNU.sparse.realsize")): "GNU.sparse.realsize",
	}
	dummyRecords := map[string]string{}
	for dummy, real := range replacements {
		switch real {
		case "GNU.sparse.major":
			dummyRecords[dummy] = "1"
		case "GNU.sparse.minor":
			dummyRecords[dummy] = "0"
		case "GNU.sparse.name":
			dummyRecords[dummy] = "repo-main/huge.bin"
		case "GNU.sparse.realsize":
			dummyRecords[dummy] = fmt.Sprint(logicalSize)
		}
	}

	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	if err := tw.WriteHeader(&tar.Header{
		Name:       "repo-main/sparse-physical",
		Typeflag:   tar.TypeReg,
		Mode:       0o644,
		Size:       512,
		Format:     tar.FormatPAX,
		PAXRecords: dummyRecords,
	}); err != nil {
		t.Fatalf("write sparse fixture header: %v", err)
	}
	// GNU sparse 1.0 stores its map at the start of the physical data. Zero
	// extents means the whole logical file is a synthesized hole.
	sparseMap := make([]byte, 512)
	copy(sparseMap, "0\n")
	if _, err := tw.Write(sparseMap); err != nil {
		t.Fatalf("write sparse fixture map: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close sparse fixture tar: %v", err)
	}
	rawBytes := raw.Bytes()
	for dummy, real := range replacements {
		from, to := []byte(dummy+"="), []byte(real+"=")
		if bytes.Count(rawBytes, from) != 1 {
			t.Fatalf("sparse fixture record %q count = %d, want 1", dummy, bytes.Count(rawBytes, from))
		}
		rawBytes = bytes.Replace(rawBytes, from, to, 1)
	}

	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(rawBytes); err != nil {
		t.Fatalf("compress sparse fixture: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close sparse fixture gzip: %v", err)
	}
	return compressed.Bytes()
}

func assertPAXSparseLogicalSize(t *testing.T, tarball []byte, logicalSize int64) {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		t.Fatalf("open sparse fixture gzip: %v", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("decode sparse fixture header: %v", err)
	}
	if hdr.Typeflag != tar.TypeReg || hdr.Size != logicalSize {
		t.Fatalf("decoded sparse header = type %d size %d, want regular logical size %d", hdr.Typeflag, hdr.Size, logicalSize)
	}
	if _, err := io.CopyN(io.Discard, tr, 1); err != nil {
		t.Fatalf("read synthesized sparse byte: %v", err)
	}
}

func serveTarball(t *testing.T, tarball []byte, inspect func(*http.Request)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inspect != nil {
			inspect(r)
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Length", fmt.Sprint(len(tarball)))
		_, _ = w.Write(tarball)
	}))
	t.Cleanup(server.Close)
	return server
}

func skillDocument(name, description string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\nbody\n", name, description)
}
