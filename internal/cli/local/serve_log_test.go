package local

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenBoundedServeLogCompactsLegacyOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loom-serve.log")
	if err := os.WriteFile(path, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}

	log, err := openBoundedServeLogWithLimits(path, 5, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "56789" {
		t.Fatalf("compacted log = %q, want newest bytes", data)
	}
}

func TestBoundedServeLogRotatesAndBoundsBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loom-serve.log")
	log, err := openBoundedServeLogWithLimits(path, 5, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"1111", "2222", "3333", "4444"} {
		if _, err := log.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]string{
		path:        "4444",
		path + ".1": "3333",
		path + ".2": "2222",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("%s = %q, want %q", path, data, want)
		}
	}
}

func TestBoundedServeLogKeepsTailOfSingleOversizedWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loom-serve.log")
	log, err := openBoundedServeLogWithLimits(path, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	written, err := log.Write([]byte("0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	if written != 10 {
		t.Fatalf("Write reported %d bytes, want 10", written)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "56789" {
		t.Fatalf("log = %q, want newest bytes", data)
	}
}
