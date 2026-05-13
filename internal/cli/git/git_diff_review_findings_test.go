package git

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

// P1.1 verification — DiffFiles / DiffFilePatch / DiffCommits do not accept
// a context.Context. The HTTP service layer (diff_service.go:56,84,119) takes
// `_ context.Context` and the public git API has no ctx parameter, so there
// is no cancellation pathway down to the go-git tree walk in diffChanges.
//
// The diffChanges function passes context.Background() into go-git's
// DiffContext (git_diff.go:232). This is structurally not a "discarded
// caller context" bug — the caller cannot pass a context in the first place.
// It is a missing-feature: long-running diff operations cannot be canceled
// even though the underlying go-git API supports it.
//
// This test pins the current API surface so any future addition of a ctx
// parameter must also plumb it through diffChanges.
func TestDiffAPISurfaceLacksContextCancellationPath(t *testing.T) {
	cases := []struct {
		name string
		fn   any
	}{
		{"DiffFiles", DiffFiles},
		{"DiffFilePatch", DiffFilePatch},
		{"DiffCommits", DiffCommits},
	}
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	for _, tc := range cases {
		ft := reflect.TypeOf(tc.fn)
		hasCtx := false
		for i := 0; i < ft.NumIn(); i++ {
			if ft.In(i).Implements(ctxType) {
				hasCtx = true
				break
			}
		}
		if hasCtx {
			t.Errorf("%s now accepts context.Context — also thread it into diffChanges via fromTree.DiffContext (git_diff.go:232)", tc.name)
		}
	}
}

// Behavioral observation: DiffFiles runs to natural completion regardless of
// any caller cancellation signal because no signal can reach it. Documents
// the gap so it stays visible.
func TestDiffFilesIgnoresExternalCancellationSignal(t *testing.T) {
	dir, base := setupDiffTestRepo(t)

	type result struct {
		files []ops.DiffFileResult
		err   error
		dur   time.Duration
	}
	done := make(chan result, 1)
	go func() {
		start := time.Now()
		files, err := DiffFiles(dir, base, "HEAD")
		done <- result{files: files, err: err, dur: time.Since(start)}
	}()

	_, cancel := context.WithCancel(context.Background())
	cancel() // No effect — DiffFiles can't see this.

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("DiffFiles failed: %v", got.err)
		}
		if len(got.files) == 0 {
			t.Fatal("expected at least one diff file")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DiffFiles hung on a small repo — unexpected")
	}
}
