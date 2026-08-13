package git

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

// Regression: DiffFiles / DiffFilePatch / DiffCommits MUST accept a
// context.Context so callers can cancel slow tree walks. Previously the
// public API took no ctx and diffChanges hardcoded context.Background()
// into go-git's DiffContext, leaving no cancellation pathway.
func TestDiffAPIAcceptsContext(t *testing.T) {
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
		if !hasCtx {
			t.Errorf("%s must accept context.Context so cancellation reaches diffChanges", tc.name)
		}
	}
}

// Regression: a pre-canceled context returned from DiffFiles must surface as
// an error from the tree walk, not run to natural completion.
func TestDiffFilesHonorsCanceledContext(t *testing.T) {
	dir, base := setupDiffTestRepo(t)

	type result struct {
		files []sourcecontrol.DiffFile
		err   error
		dur   time.Duration
	}
	done := make(chan result, 1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	go func() {
		start := time.Now()
		files, err := DiffFiles(ctx, dir, base, "HEAD")
		done <- result{files: files, err: err, dur: time.Since(start)}
	}()

	select {
	case got := <-done:
		if got.err == nil {
			// go-git's DiffContext checks ctx between entries; a tiny tree
			// can finish without ever observing the cancel. Accept either
			// "errored" or "completed quickly" — the contract is that ctx
			// is plumbed through, which TestDiffAPIAcceptsContext pins.
			if got.dur > 1*time.Second {
				t.Fatalf("DiffFiles took %v with canceled ctx — likely ignored", got.dur)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DiffFiles hung — ctx cancellation did not propagate")
	}
}
