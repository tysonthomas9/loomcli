package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestFormatYAMLDiagnostic(t *testing.T) {
	t.Parallel()

	t.Run("MissingColon", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "bad.yaml")

		// Line 3 is missing a colon — "baz qux" instead of "baz: qux"
		content := "foo: bar\ncount: 3\nbaz qux\nlast: ok\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		var out map[string]interface{}
		rawErr := yaml.Unmarshal([]byte(content), &out)
		if rawErr == nil {
			t.Fatal("expected yaml unmarshal error")
		}
		wrappedErr := fmt.Errorf("parsing project file %s: %w", path, rawErr)

		result := FormatYAMLDiagnostic(path, wrappedErr)

		if !strings.Contains(result, ">>>") {
			t.Errorf("expected >>> marker in output, got:\n%s", result)
		}
		if !strings.Contains(result, "missing its colon") {
			t.Errorf("expected 'missing its colon' suggestion, got:\n%s", result)
		}
		// Should contain line numbers from context
		if !strings.Contains(result, "| foo: bar") {
			t.Errorf("expected context line with 'foo: bar', got:\n%s", result)
		}
		// Verify the error line is highlighted with >>>
		if !strings.Contains(result, ">>> ") {
			t.Errorf("expected >>> prefix on error line, got:\n%s", result)
		}
	})

	t.Run("TabCharacter", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "tab.yaml")

		content := "root:\n\tchild: val\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		var out map[string]interface{}
		rawErr := yaml.Unmarshal([]byte(content), &out)
		if rawErr == nil {
			t.Fatal("expected yaml unmarshal error")
		}
		wrappedErr := fmt.Errorf("parsing project file %s: %w", path, rawErr)

		result := FormatYAMLDiagnostic(path, wrappedErr)

		if !strings.Contains(result, "tab character") {
			t.Errorf("expected 'tab character' mention, got:\n%s", result)
		}
		// Should mention line number where the tab is
		if !strings.Contains(result, "line(s) 2") {
			t.Errorf("expected tab line number 2, got:\n%s", result)
		}
	})

	t.Run("NonYAMLError", func(t *testing.T) {
		t.Parallel()
		err := fmt.Errorf("permission denied")
		result := FormatYAMLDiagnostic("/some/path", err)

		if result != "permission denied" {
			t.Errorf("expected exact error string, got: %q", result)
		}
	})

	t.Run("ErrorOnLine1", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "line1.yaml")

		// Invalid YAML on line 1
		content := ":\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		var out map[string]interface{}
		rawErr := yaml.Unmarshal([]byte(content), &out)
		if rawErr == nil {
			t.Fatal("expected yaml unmarshal error")
		}

		// Should not panic or produce negative line numbers
		result := FormatYAMLDiagnostic(path, rawErr)
		if strings.Contains(result, "-1 |") || strings.Contains(result, " 0 |") {
			t.Errorf("got negative or zero line number in output:\n%s", result)
		}
	})

	t.Run("ErrorOnLastLine", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "lastline.yaml")

		// 3-line file with error on the last line
		content := "a: 1\nb: 2\nc d"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		var out map[string]interface{}
		rawErr := yaml.Unmarshal([]byte(content), &out)
		if rawErr == nil {
			t.Fatal("expected yaml unmarshal error")
		}

		// Should not panic or produce out-of-bounds access
		result := FormatYAMLDiagnostic(path, rawErr)
		if result == "" {
			t.Error("expected non-empty output")
		}
		// Verify no panic occurred (reaching here means success)
	})

	t.Run("FileNotReadable", func(t *testing.T) {
		t.Parallel()
		err := fmt.Errorf("yaml: line 5: could not find expected ':'")

		result := FormatYAMLDiagnostic("/nonexistent/path/to/file.yaml", err)

		// Should not panic; should still contain the original error
		if !strings.Contains(result, "yaml: line 5") {
			t.Errorf("expected original error in output, got: %q", result)
		}
		// Should still include suggestion even without file context
		if !strings.Contains(result, "missing its colon") {
			t.Errorf("expected suggestion even without file, got:\n%s", result)
		}
	})

	t.Run("NoLineNumber", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "nolinenum.yaml")

		content := "foo: bar\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		// Error that mentions yaml: but without a line number
		err := fmt.Errorf("yaml: unmarshal errors: something went wrong")
		result := FormatYAMLDiagnostic(path, err)

		// Should not contain context lines (no >>> marker)
		if strings.Contains(result, ">>>") {
			t.Errorf("expected no context lines without line number, got:\n%s", result)
		}
		if !strings.Contains(result, "yaml: unmarshal errors") {
			t.Errorf("expected original error, got:\n%s", result)
		}
	})

	t.Run("LongLine", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "longline.yaml")

		// Create a file with a very long line that exceeds 120 chars
		longVal := strings.Repeat("x", 200)
		content := fmt.Sprintf("key %s value\n", longVal)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		// Use a synthetic error pointing at line 1
		err := fmt.Errorf("yaml: line 1: could not find expected ':'")
		result := FormatYAMLDiagnostic(path, err)

		if !strings.Contains(result, "...") {
			t.Errorf("expected truncation with '...' for long line, got:\n%s", result)
		}
	})

	t.Run("MultipleTabs", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "manytabs.yaml")

		// Create a file with tabs on 8 lines; use a synthetic yaml: error
		var lines []string
		for i := 0; i < 8; i++ {
			lines = append(lines, fmt.Sprintf("\tkey%d: val%d", i, i))
		}
		content := strings.Join(lines, "\n") + "\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		err := fmt.Errorf("yaml: line 1: found character that cannot start any token")
		result := FormatYAMLDiagnostic(path, err)

		if !strings.Contains(result, "tab character") {
			t.Errorf("expected tab character mention, got:\n%s", result)
		}
		// At most 5 lines listed, then "and N more"
		if !strings.Contains(result, "and 3 more") {
			t.Errorf("expected 'and 3 more' for extra tab lines, got:\n%s", result)
		}
	})

	t.Run("TypeMismatch", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "typemismatch.yaml")

		content := "enabled: notabool\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		// Unmarshal into a struct with a bool field to get *yaml.TypeError
		type config struct {
			Enabled bool `yaml:"enabled"`
		}
		var cfg config
		rawErr := yaml.Unmarshal([]byte(content), &cfg)
		if rawErr == nil {
			t.Fatal("expected yaml type error")
		}

		// The yaml.TypeError error message contains "cannot unmarshal"
		// We need to make sure the error string contains "yaml:" for the
		// function to process it. yaml.TypeError messages start with "yaml:"
		result := FormatYAMLDiagnostic(path, rawErr)

		if !strings.Contains(result, "cannot unmarshal") {
			t.Errorf("expected 'cannot unmarshal' in output, got:\n%s", result)
		}
		if !strings.Contains(result, "Wrong value type") {
			t.Errorf("expected type mismatch suggestion, got:\n%s", result)
		}
	})

	t.Run("DuplicateKey", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "dupkey.yaml")

		content := "foo: 1\nfoo: 2\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		var out map[string]interface{}
		rawErr := yaml.Unmarshal([]byte(content), &out)
		if rawErr == nil {
			// yaml.v3 may silently accept duplicate keys (last wins).
			// In that case, test with a synthetic error.
			t.Skip("yaml.v3 does not report duplicate keys as an error; skipping")
		}

		result := FormatYAMLDiagnostic(path, rawErr)
		if !strings.Contains(result, "already defined") && !strings.Contains(result, "duplicate") {
			t.Errorf("expected duplicate key indication, got:\n%s", result)
		}
	})
}
