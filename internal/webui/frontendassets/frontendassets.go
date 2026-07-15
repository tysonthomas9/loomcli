package frontendassets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

const buildMetaFile = ".build-meta"

// BuildInfo describes the built frontend currently served by loom serve.
type BuildInfo struct {
	FrontendHash string `json:"frontend_hash,omitempty"`
	Build        string `json:"build,omitempty"`
	GitHash      string `json:"git_hash,omitempty"`
	BuiltAt      string `json:"built_at,omitempty"`
}

type buildMeta struct {
	BuiltAt string `json:"built_at"`
	GitHash string `json:"git_hash"`
}

// HasIndex reports whether dir looks like a built frontend root.
func HasIndex(dir string) bool {
	if dir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil && !info.IsDir()
}

// HashDir returns a deterministic hash of every regular file below dir.
func HashDir(dir string) (string, error) {
	if !HasIndex(dir) {
		return "", fmt.Errorf("frontend index missing in %s", dir)
	}
	var rels []string
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rels = append(rels, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		return "", err
	}
	sort.Strings(rels)

	sum := sha256.New()
	for _, rel := range rels {
		sum.Write([]byte(rel))
		sum.Write([]byte{0})
		file, err := os.Open(filepath.Join(dir, filepath.FromSlash(rel))) //nolint:gosec // path is rooted in trusted frontend dir
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(sum, file); err != nil {
			_ = file.Close()
			return "", err
		}
		if err := file.Close(); err != nil {
			return "", err
		}
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// ReadBuildInfo returns best-effort metadata plus the deterministic content hash.
func ReadBuildInfo(dir string, build string) BuildInfo {
	info := BuildInfo{Build: build}
	if hash, err := HashDir(dir); err == nil {
		info.FrontendHash = hash
	}
	data, err := os.ReadFile(filepath.Join(dir, buildMetaFile)) //nolint:gosec // path is rooted in trusted frontend dir
	if err != nil {
		return info
	}
	var meta buildMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return info
	}
	info.BuiltAt = meta.BuiltAt
	info.GitHash = meta.GitHash
	return info
}
