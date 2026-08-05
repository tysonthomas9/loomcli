package workflowcatalog

import (
	"errors"
	"testing"
)

func TestSourceDigestIsOrderIndependent(t *testing.T) {
	left, err := SourceDigest(map[string]string{
		"workflows/task.ts": "task",
		"workflows/main.ts": "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := SourceDigest(map[string]string{
		"workflows/main.ts": "main",
		"workflows/task.ts": "task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("canonical digest mismatch: %q != %q", left, right)
	}
	again, err := SourceDigest(map[string]string{
		"workflows/task.ts": "task",
		"workflows/main.ts": "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if left != again {
		t.Fatal("source digest depends on map iteration order")
	}
}

func TestSourceDigestRejectsNormalizedPathCollision(t *testing.T) {
	_, err := SourceDigest(map[string]string{
		"workflows/main.ts":        "first",
		"workflows/dir/../main.ts": "second",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("SourceDigest collision err = %v, want ErrInvalid", err)
	}

	_, err = SourceDigest(map[string]string{
		"workflows/main.ts": "first",
		`workflows\main.ts`: "second",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("SourceDigest separator collision err = %v, want ErrInvalid", err)
	}
}
