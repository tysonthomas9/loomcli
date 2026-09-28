//go:build gitlab_real

package gitlab

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func (s *state) makeFixture(kind string) {
	s.home = filepath.Join(s.t.TempDir(), "home")
	if err := os.MkdirAll(s.home, 0700); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.home, ".gitconfig"), []byte("[user]\nname = Git Lab\nemail = lab@example.test\n"), 0600); err != nil {
		s.t.Fatal(err)
	}
	s.t.Setenv("HOME", s.home)
	s.dir = filepath.Join(s.t.TempDir(), "repo")
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		s.t.Fatal(err)
	}
	if kind != "empty" {
		archive := os.Getenv("GITLAB_SOURCE_ARCHIVE")
		if archive == "" {
			s.t.Fatal("GITLAB_SOURCE_ARCHIVE is required")
		}
		cmd := exec.Command("tar", "-xf", archive, "-C", s.dir) //nolint:norawexec // Extracts the pinned source archive for the real-repo fixture.
		if out, err := cmd.CombinedOutput(); err != nil {
			s.t.Fatalf("extract fixture: %v: %s", err, out)
		}
		for path, content := range map[string]string{".npmrc": "registry=https://example.test\n", "src/id_utils.go": "package src\n"} {
			full := filepath.Join(s.dir, path)
			if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
				s.t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(content), 0644); err != nil {
				s.t.Fatal(err)
			}
		}
	}
	if _, err := gitCommand(s.dir, "init", "-q", "-b", "main"); err != nil {
		s.t.Fatal(err)
	}
	if _, err := gitCommand(s.dir, "add", "-A"); err != nil {
		s.t.Fatal(err)
	}
	if _, err := gitCommand(s.dir, "commit", "--allow-empty", "-qm", "pinned Loom source fixture"); err != nil {
		s.t.Fatal(err)
	}
	if kind != "empty" {
		out, err := gitCommand(s.dir, "ls-files")
		if err != nil || len(strings.Split(out, "\n")) < 3000 {
			s.t.Fatalf("fixture has fewer than 3000 tracked files: %v", err)
		}
	}
	if kind == "mixed" {
		s.makeMixedFixture()
	} else if kind != "loomcli" && kind != "empty" {
		s.t.Fatalf("unknown fixture %q", kind)
	}
}

func (s *state) makeMixedFixture() {
	for path, content := range map[string]string{".env": "TOKEN=fixture\n", ".env.local": "TOKEN=local\n", "id_rsa": "fixture key\n"} {
		if err := os.WriteFile(filepath.Join(s.dir, path), []byte(content), 0600); err != nil {
			s.t.Fatal(err)
		}
	}
	if err := os.Symlink("README.md", filepath.Join(s.dir, "readme-link")); err != nil {
		s.t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(s.dir, "empty-dir"), 0755); err != nil {
		s.t.Fatal(err)
	}
	nested := filepath.Join(s.dir, "nested-repo")
	if err := os.Mkdir(nested, 0755); err != nil {
		s.t.Fatal(err)
	}
	if _, err := gitCommand(nested, "init", "-q", "-b", "main"); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "readme"), []byte("nested"), 0644); err != nil {
		s.t.Fatal(err)
	}
	if _, err := gitCommand(nested, "add", "readme"); err != nil {
		s.t.Fatal(err)
	}
	if _, err := gitCommand(nested, "commit", "-qm", "nested"); err != nil {
		s.t.Fatal(err)
	}
	if _, err := gitCommand(s.dir, "-c", "protocol.file.allow=always", "submodule", "add", "-q", nested, "vendor/nested"); err != nil {
		s.t.Fatal(err)
	}
	if _, err := gitCommand(s.dir, "lfs", "install", "--local"); err != nil {
		s.t.Fatal(err)
	}
	if _, err := gitCommand(s.dir, "lfs", "track", "*.lfs"); err != nil {
		s.t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.dir, "assets"), 0755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "assets", "sample.lfs"), []byte("LFS fixture content\n"), 0644); err != nil {
		s.t.Fatal(err)
	}
	if _, err := gitCommand(s.dir, "add", ".gitattributes", ".gitmodules", "assets/sample.lfs", "vendor/nested"); err != nil {
		s.t.Fatal(err)
	}
	if _, err := gitCommand(s.dir, "commit", "-qm", "submodule and LFS fixture"); err != nil {
		s.t.Fatal(err)
	}
	big, err := os.Create(filepath.Join(s.dir, "large-150MiB.bin"))
	if err != nil {
		s.t.Fatal(err)
	}
	if err := big.Truncate(150 << 20); err != nil {
		s.t.Fatal(err)
	}
	if err := big.Close(); err != nil {
		s.t.Fatal(err)
	}
	unreadable := filepath.Join(s.dir, "unreadable.txt")
	if err := os.WriteFile(unreadable, []byte("private"), 0600); err != nil {
		s.t.Fatal(err)
	}
	if err := os.Chmod(unreadable, 0000); err != nil {
		s.t.Fatal(err)
	}
}

func (s *state) makeProvider() string {
	remote := filepath.Join(s.t.TempDir(), "provider.git")
	if err := os.MkdirAll(remote, 0755); err != nil {
		s.t.Fatal(err)
	}
	if _, err := gitCommand(remote, "init", "-q", "--bare"); err != nil {
		s.t.Fatal(err)
	}
	hook := `#!/bin/sh
set -eu
total=0
while read old new ref; do
  case "$new" in 0000000000000000000000000000000000000000) continue;; esac
  case "$old" in 0000000000000000000000000000000000000000) range="$new";; *) range="$old..$new";; esac
  list=$(mktemp)
  git rev-list --objects "$range" > "$list"
  while read oid path; do
    [ -n "$path" ] || continue
    [ "$(git cat-file -t "$oid")" = blob ] || continue
    size=$(git cat-file -s "$oid")
    [ "$size" -le 104857600 ] || { echo "provider: file exceeds 100 MiB: $path" >&2; exit 1; }
    total=$((total + size))
    [ "$total" -le 2147483648 ] || { echo "provider: push exceeds 2 GiB" >&2; exit 1; }
    case "/$path/" in */.env/*|*/.env.*/*|*/id_rsa/*|*/id_ed25519/*|*/.npmrc/*) echo "provider: secret path refused: $path" >&2; exit 1;; esac
  done < "$list"
  rm -f "$list"
done
`
	path := filepath.Join(remote, "hooks", "pre-receive")
	if err := os.WriteFile(path, []byte(hook), 0755); err != nil {
		s.t.Fatal(err)
	}
	s.remote = remote
	return remote
}

func requireFile(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(fmt.Errorf("%s: %w", path, err))
	}
	return info
}
