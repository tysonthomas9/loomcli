package cli

import (
	"encoding/json"
	"os"
	"testing"
)

// captureStderr captures stderr output during fn execution.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = origStderr })

	fn()
	w.Close()

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	return string(buf[:n])
}

// mustJSON is a test helper that marshals v to JSON.
func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
