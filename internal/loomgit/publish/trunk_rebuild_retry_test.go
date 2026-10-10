package publish

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
)

// S6: C was rebuilt onto trunk when its PR opened; when its blocker landed, the
// landing restack rebuilt it onto the same trunk again. The replay made a new
// commit SHA under the same request ID and every restack pass failed with
// "reused for different revision", so C never merged.
func TestDeriveTrunkRevisionRetryReusesRecordedRebuild(t *testing.T) {
	useExplicitGitIdentity(t)
	fixture := newFixture(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(fixture.repo, "a"), []byte("blocker A\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, fixture.repo, "add", "a")
	git(t, fixture.repo, "commit", "-qm", "blocker A")
	blocker := git(t, fixture.repo, "rev-parse", "HEAD")
	source := fixture.revision(t, 1, blocker, "task C", "source")
	fixture.approve(t, source)
	runner, err := gitexec.New(fixture.repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		t.Fatal(err)
	}
	request := fixture.request()
	first, err := deriveTrunkRevision(ctx, fixture.store, runner, request, source, fixture.base)
	if err != nil {
		t.Fatal(err)
	}
	// Commit dates have one-second resolution; a later replay gets a new SHA.
	time.Sleep(1100 * time.Millisecond)
	second, err := deriveTrunkRevision(ctx, fixture.store, runner, request, source, fixture.base)
	if err != nil {
		t.Fatalf("retried trunk rebuild: %v", err)
	}
	if second != first {
		t.Fatalf("retry head = %s, want recorded %s", second, first)
	}
}
