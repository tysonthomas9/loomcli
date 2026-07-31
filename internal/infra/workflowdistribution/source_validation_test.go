package workflowdistribution

import (
	"context"
	"strings"
	"testing"
)

func mustSourceDigest(t testing.TB, files map[string]string) string {
	t.Helper()
	digest, err := SourceDigest(files)
	if err != nil {
		t.Fatalf("SourceDigest: %v", err)
	}
	return digest
}

func TestValidateWorkflowFilesRejectsNormalizedPathCollisions(t *testing.T) {
	whitespacePaddedPath := " workflows/main.ts "
	for _, files := range []map[string]string{
		{
			"workflows/main.ts":        "first",
			"workflows/dir/../main.ts": "second",
		},
		{
			"workflows/main.ts":  "first",
			"workflows//main.ts": "second",
		},
		{
			whitespacePaddedPath: "first",
			"workflows/main.ts":  "second",
		},
	} {
		if _, err := ValidateWorkflowFiles(files); err == nil || !strings.Contains(err.Error(), "normalize to the same path") {
			t.Fatalf("ValidateWorkflowFiles(%v) err = %v, want normalized-path collision", files, err)
		}
	}
}

func TestValidateWorkflowFilesRejectsPortableSeparatorEscape(t *testing.T) {
	_, err := ValidateWorkflowFiles(map[string]string{`workflows\..\outside.ts`: "source"})
	if err == nil || !strings.Contains(err.Error(), "canonical slash separators") {
		t.Fatalf("ValidateWorkflowFiles backslash err = %v", err)
	}
}

func TestBuildRejectsPathCollisionBeforeToolchainOrFilesystemWork(t *testing.T) {
	root := t.TempDir()
	_, _, err := Build(context.Background(), BuildOptions{
		Name: "collision", WorkDir: root,
		Files: map[string]string{
			"workflows/main.ts":        "first",
			"workflows/dir/../main.ts": "second",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "normalize to the same path") {
		t.Fatalf("Build collision err = %v", err)
	}
}
