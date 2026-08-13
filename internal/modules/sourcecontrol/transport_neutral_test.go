package sourcecontrol

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strconv"
	"strings"
	"testing"
)

func TestOwnerModelsCarryNoJSONContractMetadata(t *testing.T) {
	files, err := parser.ParseDir(token.NewFileSet(), ".", func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse Source Control package: %v", err)
	}
	for _, pkg := range files {
		for filename, file := range pkg.Files {
			ast.Inspect(file, func(node ast.Node) bool {
				field, ok := node.(*ast.Field)
				if !ok || field.Tag == nil {
					return true
				}
				tag, err := strconv.Unquote(field.Tag.Value)
				if err == nil && strings.Contains(tag, "json:") {
					t.Errorf("%s carries JSON contract metadata %q", filename, tag)
				}
				return true
			})
		}
	}
}
