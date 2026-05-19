package modbuilder

import (
	"net/http"
	"testing"
)

func TestModuleBuildersReturnRegistrableModules(t *testing.T) {
	builders := [][]interface{ Register(*http.ServeMux) }{
		NewIssueModules(nil, nil, nil),
		NewTerminalModules(TerminalModuleDeps{}),
		{NewIssueTabModule(nil, nil)},
		{NewDiffModule(nil, nil)},
		{NewFileModule(nil)},
	}
	for i, mods := range builders {
		if len(mods) == 0 {
			t.Fatalf("builder %d returned no modules", i)
		}
		for j, mod := range mods {
			if mod == nil {
				t.Fatalf("builder %d module %d is nil", i, j)
			}
		}
	}
}
