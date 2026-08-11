//go:build loom_packaged_builtins

package authoring

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedPackagedBuiltinsMatchSource(t *testing.T) {
	for _, name := range BuiltinWorkflowNames() {
		t.Run(name, func(t *testing.T) {
			spec, ok := BuiltinWorkflow(name)
			if !ok {
				t.Fatalf("%s builtin missing", name)
			}
			distPath := filepath.ToSlash(filepath.Join("builtin-dist", name, "dist"))
			matches, err := packagedBuiltinDigestMatches(packagedBuiltinFS, distPath, mustSourceDigest(t, spec.Files))
			if err != nil {
				t.Fatalf("read embedded %s digest: %v", name, err)
			}
			if !matches {
				t.Fatalf("embedded %s bundle is missing or stale; rebuild it before packaging", name)
			}
			assertPackagedBundleHasNoBuildAnnotations(t, distPath)
		})
	}
}

func assertPackagedBundleHasNoBuildAnnotations(t *testing.T, distPath string) {
	t.Helper()
	err := fs.WalkDir(packagedBuiltinFS, distPath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || (filepath.Ext(path) != ".js" && filepath.Ext(path) != ".mjs" && filepath.Ext(path) != ".cjs") {
			return nil
		}
		content, err := fs.ReadFile(packagedBuiltinFS, path)
		if err != nil {
			return err
		}
		for _, marker := range []string{"//#region", "//#endregion", "sourceMappingURL="} {
			if strings.Contains(string(content), marker) {
				t.Errorf("packaged executable %s retained build annotation %q", path, marker)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect packaged bundle %s: %v", distPath, err)
	}
}
