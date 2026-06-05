package flue

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// templateFS holds the default flue project loom scaffolds into the managed
// project directory. The `all:` prefix is required so dot-prefixed entries
// (notably .flue/) are embedded — a plain `//go:embed template` silently
// skips them.
//
//go:embed all:template
var templateFS embed.FS

const templateRoot = "template"

// computeTemplateHash returns a stable hex fingerprint over every embedded
// template file's path and contents. The managed project records this hash;
// when it changes (e.g. a flue dependency bump in package.json, or an edited
// workflow), the manager re-scaffolds, re-installs, and re-builds.
func computeTemplateHash() (string, error) {
	var paths []string
	if err := fs.WalkDir(templateFS, templateRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("flue: walk template: %w", err)
	}
	sort.Strings(paths)

	h := sha256.New()
	for _, p := range paths {
		data, err := templateFS.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("flue: read embedded %s: %w", p, err)
		}
		// Include the path so renames/moves change the fingerprint too.
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// scaffoldTemplate writes every embedded template file into dst, creating
// parent directories as needed. It overwrites existing files so a template
// change (new hash) refreshes the managed project in place. node_modules and
// dist are never part of the template, so they are left untouched.
func scaffoldTemplate(dst string) error {
	return fs.WalkDir(templateFS, templateRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(templateRoot, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := templateFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("flue: read embedded %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil { //nolint:gosec // project source files are user-readable
			return fmt.Errorf("flue: write %s: %w", target, err)
		}
		return nil
	})
}
