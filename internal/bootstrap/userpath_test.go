package bootstrap

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMergePATH(t *testing.T) {
	sep := string(os.PathListSeparator)
	tests := []struct {
		name    string
		current string
		add     []string
		want    string
	}{
		{name: "current first and deduped", current: "/one" + sep + "/two" + sep + "/one", add: []string{"/three", "/two"}, want: "/one" + sep + "/two" + sep + "/three"},
		{name: "skips empty and relative additions", current: "relative" + sep + "/one", add: []string{"", "bin", "/two"}, want: "relative" + sep + "/one" + sep + "/two"},
		{name: "preserves current entries", current: "./bin" + sep + "/one/../two", add: []string{"/three"}, want: "./bin" + sep + "/one/../two" + sep + "/three"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergePATH(tt.current, tt.add); got != tt.want {
				t.Fatalf("mergePATH() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCandidateUserBinDirs(t *testing.T) {
	home := t.TempDir()
	nvmBin := filepath.Join(home, ".nvm", "versions", "node", "v22.1.0", "bin")
	if err := os.MkdirAll(nvmBin, 0o755); err != nil {
		t.Fatal(err)
	}
	dirs := candidateUserBinDirs(home)
	for _, want := range []string{
		filepath.Join(home, ".local", "bin"), filepath.Join(home, "bin"),
		filepath.Join(home, ".cargo", "bin"), filepath.Join(home, "go", "bin"),
		"/opt/homebrew/bin", "/usr/local/bin", nvmBin,
	} {
		if !containsString(dirs, want) {
			t.Errorf("candidateUserBinDirs() does not contain %q: %v", want, dirs)
		}
	}
}

func TestExistingDirs(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "existing")
	file := filepath.Join(root, "file")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []string{existing}
	if got := existingDirs([]string{existing, filepath.Join(root, "missing"), file}); !reflect.DeepEqual(got, want) {
		t.Fatalf("existingDirs() = %v, want %v", got, want)
	}
}

func TestAugmentedPATH(t *testing.T) {
	home := t.TempDir()
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	got := augmentedPATH("/usr/bin"+string(os.PathListSeparator)+"/bin", home)
	if !strings.HasPrefix(got, "/usr/bin"+string(os.PathListSeparator)+"/bin") || !containsPathEntry(got, localBin) {
		t.Fatalf("augmentedPATH() = %q, want original prefix and %q", got, localBin)
	}
}

func TestAugmentedPATHFindsCodexInScrubbedPATH(t *testing.T) {
	originalPATH := os.Getenv("PATH")
	t.Cleanup(func() { _ = os.Setenv("PATH", originalPATH) })
	home := t.TempDir()
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	codex := filepath.Join(localBin, "codex")
	if err := os.WriteFile(codex, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("PATH", "/usr/bin"+string(os.PathListSeparator)+"/bin"); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("codex"); err == nil {
		t.Fatal("exec.LookPath(codex) unexpectedly succeeded with scrubbed PATH")
	}
	if err := os.Setenv("PATH", augmentedPATH(os.Getenv("PATH"), home)); err != nil {
		t.Fatal(err)
	}
	got, err := exec.LookPath("codex")
	if err != nil || got != codex {
		t.Fatalf("exec.LookPath(codex) = %q, %v; want %q", got, err, codex)
	}
}

func TestMergePATHIdempotent(t *testing.T) {
	first := mergePATH("/one", []string{"/two", "/three"})
	if got := mergePATH(first, []string{"/two", "/three"}); got != first {
		t.Fatalf("second merge = %q, want stable result %q", got, first)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsPathEntry(path, want string) bool {
	return containsString(filepath.SplitList(path), want)
}
