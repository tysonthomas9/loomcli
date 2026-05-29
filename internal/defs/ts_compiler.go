package defs

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed ts_compiler.js
var tsCompilerSource string

func loadWithTypeScriptCompiler(root string) (*Plan, error) {
	compilerDir, compiler, err := writeCompiler()
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(compilerDir) }()

	cmd := exec.Command("node", "--no-warnings", compiler, root) //nolint:gosec // Compiler path is generated under a private temp dir by writeCompiler.
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("compile .loom TypeScript definitions: %w\n%s", err, string(out))
	}
	var plan Plan
	if err := json.Unmarshal(out, &plan); err != nil {
		return nil, fmt.Errorf("decode TypeScript definition plan: %w\n%s", err, string(out))
	}
	return &plan, nil
}

func writeCompiler() (string, string, error) {
	dir, err := os.MkdirTemp("", "loom-ts-compiler-*")
	if err != nil {
		return "", "", err
	}
	path := filepath.Join(dir, "compiler.cjs")
	if err := os.WriteFile(path, []byte(tsCompilerSource), 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", err
	}
	return dir, path, nil
}
