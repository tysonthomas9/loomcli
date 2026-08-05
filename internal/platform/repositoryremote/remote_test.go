package repositoryremote

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeTokenFreeRepositorySourceContract(t *testing.T) {
	t.Parallel()

	local := filepath.Join(string(filepath.Separator), "srv", "repos", "app.git")
	valid := []string{
		"https://github.com/acme/app.git",
		"http://git.example.test/acme/app.git",
		"git://git.example.test/acme/app.git",
		"ssh://git@git.example.test/acme/app.git",
		"ssh://git.example.test/acme/app.git",
		"git@git.example.test:acme/app.git",
		local,
	}
	for _, remote := range valid {
		remote := remote
		t.Run("accept_"+safeTestName(remote), func(t *testing.T) {
			got, err := Normalize(remote)
			if err != nil {
				t.Fatalf("Normalize(%q): %v", remote, err)
			}
			if got != remote {
				t.Fatalf("Normalize(%q) = %q", remote, got)
			}
		})
	}

	invalid := []string{
		"",
		" https://github.com/acme/app.git",
		"https://github.com/acme/app.git ",
		"https://token@github.com/acme/app.git",
		"http://user:secret@git.example.test/acme/app.git",
		"ssh://deploy@git.example.test/acme/app.git",
		"ssh://git:secret@git.example.test/acme/app.git",
		"file:///srv/repos/app.git",
		"ftp://git.example.test/acme/app.git",
		"github.com/acme/app.git",
		"git.example.test:acme/app.git",
		"user@git.example.test:acme/app.git",
		"git@git.example.test:",
		"https://git.example.test",
		"https:///acme/app.git",
		"https://git.example.test/acme/app.git?token=secret",
		"https://git.example.test/acme/app.git#fragment",
		local + string(filepath.Separator) + ".." + string(filepath.Separator) + "other.git",
		strings.Repeat("a", 1025),
	}
	for index, remote := range invalid {
		remote := remote
		t.Run("reject_"+safeTestName(remote)+"_"+string(rune('a'+index)), func(t *testing.T) {
			if _, err := Normalize(remote); err == nil {
				t.Fatalf("Normalize(%q) unexpectedly succeeded", remote)
			}
		})
	}
}

func safeTestName(value string) string {
	value = strings.NewReplacer("/", "_", ":", "_", "@", "_").Replace(value)
	if len(value) > 80 {
		return value[:80]
	}
	return value
}
