package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

type diffFileParityRow struct {
	Status    string
	Path      string
	OldPath   string
	Additions int
	Deletions int
}

func TestDiffFilesParityWithNativeGit(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) (dir, from, to string)
	}{
		{
			name: "mixed text binary delete rename",
			setup: func(t *testing.T) (string, string, string) {
				dir, base := setupDiffTestRepo(t)
				return dir, base, "HEAD"
			},
		},
		{
			name: "special character filenames",
			setup: func(t *testing.T) (string, string, string) {
				repo := newGitDiffTestRepo(t, "main")
				repo.write("base.txt", "base\n")
				base := repo.commitAll("base")

				repo.run("checkout", "-b", "feature")
				repo.write("tab\tfile.txt", "tab\n")
				repo.write("line\nfile.txt", "newline\n")
				repo.commitAll("special filenames")
				return repo.dir, base, "HEAD"
			},
		},
		{
			name: "annotated tag from ref",
			setup: func(t *testing.T) (string, string, string) {
				repo := newGitDiffTestRepo(t, "main")
				repo.write("base.txt", "base\n")
				repo.commitAll("base")
				repo.run("tag", "-a", "v-base", "-m", "base tag")

				repo.run("checkout", "-b", "feature")
				repo.write("feature.txt", "feature\n")
				repo.commitAll("feature")
				return repo.dir, "v-base", "HEAD"
			},
		},
		{
			name: "mode only change",
			setup: func(t *testing.T) (string, string, string) {
				repo := newGitDiffTestRepo(t, "main")
				repo.run("config", "core.filemode", "true")
				repo.write("script.sh", "#!/bin/sh\necho hi\n")
				base := repo.commitAll("base")

				if err := os.Chmod(filepath.Join(repo.dir, "script.sh"), 0755); err != nil {
					t.Fatal(err)
				}
				repo.run("add", "script.sh")
				if repo.run("status", "--short") == "" {
					t.Skip("git did not detect executable bit changes on this filesystem")
				}
				repo.run("commit", "-m", "make executable")
				return repo.dir, base, "HEAD"
			},
		},
		{
			name: "symlink target change",
			setup: func(t *testing.T) (string, string, string) {
				repo := newGitDiffTestRepo(t, "main")
				link := filepath.Join(repo.dir, "link")
				if err := os.Symlink("target-a", link); err != nil {
					t.Skipf("symlink creation failed: %v", err)
				}
				base := repo.commitAll("base")

				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("target-b", link); err != nil {
					t.Skipf("symlink replacement failed: %v", err)
				}
				repo.commitAll("retarget symlink")
				return repo.dir, base, "HEAD"
			},
		},
		{
			name: "regular file to symlink typechange",
			setup: func(t *testing.T) (string, string, string) {
				repo := newGitDiffTestRepo(t, "main")
				repo.write("path", "regular\n")
				base := repo.commitAll("base")

				if err := os.Remove(filepath.Join(repo.dir, "path")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("target", filepath.Join(repo.dir, "path")); err != nil {
					t.Skipf("symlink creation failed: %v", err)
				}
				repo.commitAll("typechange")
				return repo.dir, base, "HEAD"
			},
		},
		{
			name: "directory rename as file renames",
			setup: func(t *testing.T) (string, string, string) {
				repo := newGitDiffTestRepo(t, "main")
				repo.write("old/a.txt", "a\n")
				repo.write("old/b.txt", "b\n")
				base := repo.commitAll("base")

				repo.run("mv", "old", "new")
				repo.commitAll("rename dir")
				return repo.dir, base, "HEAD"
			},
		},
		{
			name: "submodule gitlink change",
			setup: func(t *testing.T) (string, string, string) {
				repo := newGitDiffTestRepo(t, "main")
				repo.run("update-index", "--add", "--cacheinfo", "160000,1111111111111111111111111111111111111111,deps/mod")
				base := repo.commitIndex("add gitlink")

				repo.run("update-index", "--cacheinfo", "160000,2222222222222222222222222222222222222222,deps/mod")
				repo.commitIndex("update gitlink")
				return repo.dir, base, "HEAD"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, from, to := tt.setup(t)
			gotFiles, err := DiffFiles(t.Context(), dir, from, to)
			if err != nil {
				t.Fatalf("DiffFiles failed: %v", err)
			}
			got := canonicalGoGitDiffFiles(gotFiles)
			want := nativeGitDiffFiles(t, dir, from, to, "--find-renames")
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("DiffFiles parity mismatch\n got: %s\nwant: %s", formatParityRows(got), formatParityRows(want))
			}
		})
	}
}

func TestDiffFilesKnownNativeGitDivergences(t *testing.T) {
	t.Run("dirty worktree is outside committed tree diff scope", func(t *testing.T) {
		repo := newGitDiffTestRepo(t, "main")
		repo.write("base.txt", "base\n")
		repo.commitAll("base")
		repo.write("base.txt", "dirty\n")

		gotFiles, err := DiffFiles(t.Context(), repo.dir, "HEAD", "HEAD")
		if err != nil {
			t.Fatalf("DiffFiles failed: %v", err)
		}
		got := canonicalGoGitDiffFiles(gotFiles)
		native := nativeGitDiffWorktreeFiles(t, repo.dir, "HEAD", "--find-renames")
		if reflect.DeepEqual(got, native) {
			t.Fatalf("expected dirty worktree divergence, got matching rows %s", formatParityRows(got))
		}
		if len(got) != 0 || len(native) != 1 || native[0].Path != "base.txt" || native[0].Status != "M" {
			t.Fatalf("unexpected dirty worktree rows\n got: %s\nnative: %s", formatParityRows(got), formatParityRows(native))
		}
	})

	t.Run("copy detection is not part of the API status model", func(t *testing.T) {
		repo := newGitDiffTestRepo(t, "main")
		repo.write("source.txt", "same\n")
		base := repo.commitAll("base")

		repo.write("copy.txt", "same\n")
		repo.commitAll("copy file")

		gotFiles, err := DiffFiles(t.Context(), repo.dir, base, "HEAD")
		if err != nil {
			t.Fatalf("DiffFiles failed: %v", err)
		}
		got := canonicalGoGitDiffFiles(gotFiles)
		nativeCopies := nativeGitDiffFiles(t, repo.dir, base, "HEAD", "--find-copies", "--find-copies-harder")
		if reflect.DeepEqual(got, nativeCopies) {
			t.Fatalf("expected copy-detection divergence, got matching rows %s", formatParityRows(got))
		}
		if len(got) != 1 || got[0].Status != "A" || got[0].Path != "copy.txt" {
			t.Fatalf("go-git rows = %s, want copy surfaced as add", formatParityRows(got))
		}
		if len(nativeCopies) != 1 || nativeCopies[0].Status != "C" || nativeCopies[0].OldPath != "source.txt" || nativeCopies[0].Path != "copy.txt" {
			t.Fatalf("native copy rows = %s, want C source.txt -> copy.txt", formatParityRows(nativeCopies))
		}
	})

	t.Run("file list is intentionally capped", func(t *testing.T) {
		repo := newGitDiffTestRepo(t, "main")
		repo.write("base.txt", "base\n")
		base := repo.commitAll("base")

		for i := 0; i < maxDiffFiles+1; i++ {
			repo.write(fmt.Sprintf("files/%03d.txt", i), "x\n")
		}
		repo.commitAll("many files")

		gotFiles, err := DiffFiles(t.Context(), repo.dir, base, "HEAD")
		if err != nil {
			t.Fatalf("DiffFiles failed: %v", err)
		}
		native := nativeGitDiffFiles(t, repo.dir, base, "HEAD", "--find-renames")
		if len(gotFiles) != maxDiffFiles || len(native) != maxDiffFiles+1 {
			t.Fatalf("file cap mismatch: go-git=%d native=%d", len(gotFiles), len(native))
		}
	})
}

func TestDiffFilePatchKnownNativeGitDivergences(t *testing.T) {
	t.Run("large single file patches are intentionally capped", func(t *testing.T) {
		repo := newGitDiffTestRepo(t, "main")
		repo.write("large.txt", "base\n")
		base := repo.commitAll("base")

		repo.write("large.txt", strings.Repeat("line with enough bytes to exceed the patch cap\n", 12000))
		repo.commitAll("large patch")

		result, err := DiffFilePatch(t.Context(), repo.dir, base, "HEAD", "large.txt")
		if err != nil {
			t.Fatalf("DiffFilePatch failed: %v", err)
		}
		nativePatch := runNativeGitBytes(t, repo.dir, "diff", base, "HEAD", "--", "large.txt")
		if !result.IsTooLarge || result.Patch != "" {
			t.Fatalf("large patch result = %+v, want too-large without patch body", result)
		}
		if len(nativePatch) <= maxDiffPatchBytes {
			t.Fatalf("native patch length = %d, want > %d", len(nativePatch), maxDiffPatchBytes)
		}
	})
}

func TestDiffFilesParityMissingBaseObject(t *testing.T) {
	source := newGitDiffTestRepo(t, "main")
	source.write("base.txt", "base\n")
	base := source.commitAll("base")
	source.run("checkout", "-b", "feature")
	source.write("feature.txt", "feature\n")
	source.commitAll("feature")

	root := t.TempDir()
	shallow := filepath.Join(root, "shallow")
	runGitInDir(t, root, "clone", "--depth", "1", "--branch", "feature", "file://"+source.dir, shallow)
	if _, err := runNativeGitBytesAllowError(t, shallow, "cat-file", "-e", base+"^{commit}"); err == nil {
		t.Skip("git retained the base commit despite depth=1")
	}

	_, goGitErr := DiffFiles(t.Context(), shallow, base, "HEAD")
	_, nativeErr := runNativeGitBytesAllowError(t, shallow, "diff", "--name-status", base, "HEAD")
	if goGitErr == nil || nativeErr == nil {
		t.Fatalf("missing base object errors: go-git=%v native=%v", goGitErr, nativeErr)
	}
}

func canonicalGoGitDiffFiles(files []ops.DiffFileResult) []diffFileParityRow {
	rows := make([]diffFileParityRow, 0, len(files))
	for _, file := range files {
		rows = append(rows, diffFileParityRow{
			Status:    file.Status,
			Path:      file.Path,
			OldPath:   file.OldPath,
			Additions: file.Additions,
			Deletions: file.Deletions,
		})
	}
	sortParityRows(rows)
	return rows
}

func nativeGitDiffFiles(t *testing.T, dir, from, to string, options ...string) []diffFileParityRow {
	t.Helper()
	args := append([]string{"diff", "--name-status", "-z"}, options...)
	args = append(args, from, to)
	statusRows := parseNativeNameStatus(t, runNativeGitBytes(t, dir, args...))

	args = append([]string{"diff", "--numstat", "-z"}, options...)
	args = append(args, from, to)
	stats := parseNativeNumstat(t, runNativeGitBytes(t, dir, args...))
	return mergeNativeStatusAndStats(statusRows, stats)
}

func nativeGitDiffWorktreeFiles(t *testing.T, dir, from string, options ...string) []diffFileParityRow {
	t.Helper()
	args := append([]string{"diff", "--name-status", "-z"}, options...)
	args = append(args, from)
	statusRows := parseNativeNameStatus(t, runNativeGitBytes(t, dir, args...))

	args = append([]string{"diff", "--numstat", "-z"}, options...)
	args = append(args, from)
	stats := parseNativeNumstat(t, runNativeGitBytes(t, dir, args...))
	return mergeNativeStatusAndStats(statusRows, stats)
}

func mergeNativeStatusAndStats(statusRows []diffFileParityRow, stats map[string]diffFileParityRow) []diffFileParityRow {
	rows := make([]diffFileParityRow, 0, len(statusRows))
	for _, row := range statusRows {
		if stat, ok := stats[row.Path]; ok {
			row.Additions = stat.Additions
			row.Deletions = stat.Deletions
		}
		rows = append(rows, row)
	}
	sortParityRows(rows)
	return rows
}

func parseNativeNameStatus(t *testing.T, out []byte) []diffFileParityRow {
	t.Helper()
	fields := splitNULTerminated(out)
	rows := make([]diffFileParityRow, 0, len(fields)/2)
	for i := 0; i < len(fields); {
		statusCode := string(fields[i])
		i++
		if statusCode == "" {
			continue
		}
		status := nativeStatusForAPI(statusCode)
		if status == "R" || status == "C" {
			if i+1 >= len(fields) {
				t.Fatalf("malformed name-status rename/copy output: %q", out)
			}
			oldPath := string(fields[i])
			path := string(fields[i+1])
			i += 2
			rows = append(rows, diffFileParityRow{Status: status, OldPath: oldPath, Path: path})
			continue
		}
		if i >= len(fields) {
			t.Fatalf("malformed name-status output: %q", out)
		}
		path := string(fields[i])
		i++
		rows = append(rows, diffFileParityRow{Status: status, Path: path})
	}
	return rows
}

func nativeStatusForAPI(statusCode string) string {
	status := statusCode[:1]
	if status == "T" {
		return "M"
	}
	return status
}

func parseNativeNumstat(t *testing.T, out []byte) map[string]diffFileParityRow {
	t.Helper()
	stats := map[string]diffFileParityRow{}
	for i := 0; i < len(out); {
		additionsField, next, ok := readUntilByte(out, i, '\t')
		if !ok {
			t.Fatalf("malformed numstat additions field near %q", out[i:])
		}
		i = next
		deletionsField, next, ok := readUntilByte(out, i, '\t')
		if !ok {
			t.Fatalf("malformed numstat deletions field near %q", out[i:])
		}
		i = next

		var path string
		if i < len(out) && out[i] == 0 {
			i++
			_, next, ok = readUntilByte(out, i, 0)
			if !ok {
				t.Fatalf("malformed numstat rename old path near %q", out[i:])
			}
			i = next
			pathBytes, next, ok := readUntilByte(out, i, 0)
			if !ok {
				t.Fatalf("malformed numstat rename new path near %q", out[i:])
			}
			path = string(pathBytes)
			i = next
		} else {
			pathBytes, next, ok := readUntilByte(out, i, 0)
			if !ok {
				t.Fatalf("malformed numstat path near %q", out[i:])
			}
			path = string(pathBytes)
			i = next
		}
		stats[path] = diffFileParityRow{
			Path:      path,
			Additions: parseNativeNum(t, string(additionsField)),
			Deletions: parseNativeNum(t, string(deletionsField)),
		}
	}
	return stats
}

func parseNativeNum(t *testing.T, field string) int {
	t.Helper()
	if field == "-" {
		return 0
	}
	n, err := strconv.Atoi(field)
	if err != nil {
		t.Fatalf("parse numstat field %q: %v", field, err)
	}
	return n
}

func readUntilByte(b []byte, start int, delim byte) ([]byte, int, bool) {
	idx := bytes.IndexByte(b[start:], delim)
	if idx < 0 {
		return nil, start, false
	}
	end := start + idx
	return b[start:end], end + 1, true
}

func splitNULTerminated(out []byte) [][]byte {
	out = bytes.TrimSuffix(out, []byte{0})
	if len(out) == 0 {
		return nil
	}
	return bytes.Split(out, []byte{0})
}

func runNativeGitBytes(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	out, err := runNativeGitBytesAllowError(t, dir, args...)
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return out
}

func runNativeGitBytesAllowError(t *testing.T, dir string, args ...string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec
	cmd.Dir = dir
	cmd.Env = gitSafeEnv(
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	return out, err
}

func runGitInDir(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	out, err := runNativeGitBytesAllowError(t, dir, args...)
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return out
}

func sortParityRows(rows []diffFileParityRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Path != rows[j].Path {
			return rows[i].Path < rows[j].Path
		}
		if rows[i].OldPath != rows[j].OldPath {
			return rows[i].OldPath < rows[j].OldPath
		}
		return rows[i].Status < rows[j].Status
	})
}

func formatParityRows(rows []diffFileParityRow) string {
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, fmt.Sprintf("{status:%q path:%q old:%q +:%d -:%d}", row.Status, row.Path, row.OldPath, row.Additions, row.Deletions))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
