package teamtemplate

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Both the CLI and the webui import this package, so an edge into either one
// would be a cycle waiting to happen. The dependency closure is deliberately
// two packages wide: internal/store imports only internal/domain, which imports
// only internal/types.
func TestPackageImportsStayInsideTheAllowedClosure(t *testing.T) {
	allowed := map[string]bool{
		"github.com/tysonthomas9/loomcli/internal/domain": true,
		"github.com/tysonthomas9/loomcli/internal/store":  true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(path, "github.com/tysonthomas9/loomcli/") {
				continue
			}
			if !allowed[path] {
				t.Errorf("%s imports %s; teamtemplate may import only internal/domain and internal/store", name, path)
			}
		}
	}
}
