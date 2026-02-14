package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContainsSubstring(t *testing.T) {
	tests := []struct {
		name   string
		slice  []string
		substr string
		want   bool
	}{
		{"found", []string{"hello world", "foo"}, "world", true},
		{"not found", []string{"hello", "foo"}, "world", false},
		{"empty slice", []string{}, "world", false},
		{"empty substr", []string{"hello"}, "", true},
		{"exact match", []string{"hello"}, "hello", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContainsSubstring(tt.slice, tt.substr); got != tt.want {
				t.Errorf("ContainsSubstring(%v, %q) = %v, want %v", tt.slice, tt.substr, got, tt.want)
			}
		})
	}
}

func TestSetupTestEnv(t *testing.T) {
	const key = "TESTUTIL_TEST_VAR_12345"

	// Ensure it's not set initially
	os.Unsetenv(key)

	t.Run("sets and restores", func(t *testing.T) {
		SetupTestEnv(t, map[string]string{key: "test_value"})
		if got := os.Getenv(key); got != "test_value" {
			t.Errorf("expected %q, got %q", "test_value", got)
		}
	})

	// After sub-test cleanup, the var should be unset again
	if _, ok := os.LookupEnv(key); ok {
		t.Errorf("expected %s to be unset after cleanup", key)
	}
}

func TestMockStdin(t *testing.T) {
	origStdin := os.Stdin

	t.Run("replaces stdin", func(t *testing.T) {
		MockStdin(t, "test input")
		buf := make([]byte, 10)
		n, _ := os.Stdin.Read(buf)
		if string(buf[:n]) != "test input" {
			t.Errorf("expected %q from stdin, got %q", "test input", string(buf[:n]))
		}
	})

	// After sub-test cleanup, stdin should be restored
	if os.Stdin != origStdin {
		t.Error("expected stdin to be restored after cleanup")
	}
}

func TestLoadFixture(t *testing.T) {
	// Create a temp directory with a testdata subfolder
	tmpDir := t.TempDir()
	testdataDir := filepath.Join(tmpDir, "testdata")
	if err := os.MkdirAll(testdataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testdataDir, "sample.txt"), []byte("fixture content"), 0644); err != nil {
		t.Fatal(err)
	}

	// Change to the temp dir so LoadFixture can find testdata/
	origDir, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(origDir) })
	os.Chdir(tmpDir)

	got := LoadFixture(t, "sample.txt")
	if got != "fixture content" {
		t.Errorf("LoadFixture() = %q, want %q", got, "fixture content")
	}
}
