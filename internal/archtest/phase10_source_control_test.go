package archtest

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhase10SourceControlBroadPublicAuthoritiesCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "internal", "modules", "sourcecontrol"))
	if err != nil {
		t.Fatal(err)
	}
	packages, err := parser.ParseDir(token.NewFileSet(), root, func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse Source Control owner: %v", err)
	}
	retired := map[string]struct{}{
		"API": {}, "Materializer": {}, "RepositoryAdmissionMaterializer": {},
		"TaskOutcomeRecorder": {}, "StackLifecycle": {}, "StackBindingResolver": {},
		"FileMechanics": {}, "WorkspaceFileAdapter": {},
	}
	for _, pkg := range packages {
		for filename, file := range pkg.Files {
			for _, declaration := range file.Decls {
				general, ok := declaration.(*ast.GenDecl)
				if !ok || general.Tok != token.TYPE {
					continue
				}
				for _, spec := range general.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if _, forbidden := retired[typeSpec.Name.Name]; forbidden {
						t.Errorf("retired broad Source Control authority %s returned in %s", typeSpec.Name.Name, filename)
					}
				}
			}
		}
	}
}

func TestPhase10LegacySourceControlCoordinatorsCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	for _, relative := range []string{
		"internal/ops/fileops.go",
		"internal/ops/gitops.go",
		"internal/app/query/issuediff",
		"internal/webui/filecoord",
		"internal/webui/sourcecontrolcoord",
	} {
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("retired Source Control coordinator %s returned (stat error: %v)", relative, statErr)
		}
	}
}

func TestPhase10LegacySourceControlSymbolsCannotReturn(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	repository := openRootedTestFS(t, root)
	retired := []string{
		"type GitOps interface",
		"type FileOps interface",
		"GitOpsImpl",
		"NewGitOps",
		"SourceControlBrowseAdapter",
		"NewSourceControlBrowseAdapter",
		"NewReadGrant",
		"NewReadWriteGrant",
		"fileCapabilitiesContextKey",
		"withFileCapabilities",
		"fileCapabilitiesFromContext",
		"internal/webui/filecoord",
		"internal/webui/sourcecontrolcoord",
	}

	if err := fs.WalkDir(repository, "internal", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || strings.HasSuffix(path, "_test.go") || !strings.HasSuffix(path, ".go") {
			return nil
		}
		content, readErr := fs.ReadFile(repository, path)
		if readErr != nil {
			return readErr
		}
		for _, fragment := range retired {
			if strings.Contains(string(content), fragment) {
				t.Errorf("retired Source Control symbol or import %q returned in %s", fragment, path)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPhase10SourceControlGrantIssuerIsCompositionOwned(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	const owner = "internal/cli/serve/opsimpl/source_control_runtime.go"
	for _, path := range productionGoFilesBelow(t, root, "internal") {
		facts := loadSourceControlFileFacts(t, root, path)
		if path != owner && facts.selectors["NewAccessGrantIssuer"] != 0 {
			t.Errorf("%s mints Source Control access grants outside the composition root", path)
		}
	}
	if got := loadSourceControlFileFacts(t, root, owner).selectors["NewAccessGrantIssuer"]; got != 1 {
		t.Fatalf("%s grant issuer construction count = %d, want exactly 1", owner, got)
	}
}

func TestPhase10GitAndFileHTTPHandlersDependOnSourceControlOwner(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		relative string
		include  func(string) bool
	}{
		{relative: "internal/webui/handlers/git", include: func(string) bool { return true }},
		{relative: "internal/webui/handlers/misc", include: func(name string) bool {
			return name == "module.go" || strings.HasPrefix(name, "files")
		}},
	}
	for _, check := range checks {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(check.relative)), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || strings.HasSuffix(path, "_test.go") || !strings.HasSuffix(path, ".go") || !check.include(entry.Name()) {
				return nil
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for _, forbidden := range []string{
				"internal/ops",
				"internal/webui/agentcoord",
				"internal/webui/filecoord",
				"internal/webui/sourcecontrolcoord",
			} {
				if strings.Contains(string(content), forbidden) {
					t.Errorf("Source Control HTTP adapter %s bypasses its owner through %q", path, forbidden)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestPhase10SourceControlFilesystemAdapterStaysPrivate(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, legacyRootFile := range []string{
		"files_git_status.go",
		"files_history.go",
		"files_index_cache.go",
		"files_rooted_store.go",
		"files_service.go",
		"files_validation.go",
		"files_versions.go",
		"files_walk.go",
	} {
		path := filepath.Join(root, "internal", "modules", "sourcecontrol", legacyRootFile)
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("machine-local filesystem implementation returned to Source Control root: %s", legacyRootFile)
		}
	}

	const adapterImport = "internal/modules/sourcecontrol/filesystem"
	const compositionOwner = "internal/cli/serve/opsimpl/source_control_runtime.go"
	for _, path := range productionGoFilesBelow(t, root, "internal") {
		content, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Contains(string(content), adapterImport) && path != compositionOwner {
			t.Errorf("%s imports Source Control's private filesystem adapter outside composition", path)
		}
	}
}
