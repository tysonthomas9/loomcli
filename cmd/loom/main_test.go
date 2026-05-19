package main

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func TestMainRunsHelp(t *testing.T) {
	oldArgs := os.Args
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() {
		os.Args = oldArgs
		os.Stdout = oldStdout
		_ = r.Close()
	})

	os.Args = []string{"loom", "--help"}
	os.Stdout = w
	main()

	if err := w.Close(); err != nil {
		t.Fatalf("close stdout: %v", err)
	}
	var out bytes.Buffer
	if _, err := io.Copy(&out, r); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("loom")) {
		t.Fatalf("help output = %q", out.String())
	}
}
