package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMainSuccessPath(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "openapi.yaml")
	if err := os.WriteFile(spec, []byte("openapi: 3.1.0\nschema:\n  type: [string, 'null']\n"), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	oldArgs := os.Args
	oldStdout := os.Stdout
	t.Cleanup(func() {
		os.Args = oldArgs
		os.Stdout = oldStdout
	})
	os.Args = []string{"openapi-to-30", spec}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	main()

	if err := w.Close(); err != nil {
		t.Fatalf("close stdout: %v", err)
	}
	var out bytes.Buffer
	if _, err := out.ReadFrom(r); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	got := out.String()
	for _, want := range []string{"openapi: 3.0.3", "type: string", "nullable: true"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}
