package terminal

import (
	"errors"
	"fmt"
	"testing"

	daytonaerrors "github.com/daytonaio/daytona/libs/sdk-go/pkg/errors"
)

func TestIsDaytonaSandboxNotFound(t *testing.T) {
	notFound := daytonaerrors.NewDaytonaNotFoundError("sandbox missing", nil)
	wrapped := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", notFound))

	if !isDaytonaSandboxNotFound(wrapped) {
		t.Fatal("expected wrapped DaytonaNotFoundError to be recognized")
	}
	if isDaytonaSandboxNotFound(fmt.Errorf("nope")) {
		t.Fatal("expected plain error not to be recognized")
	}
}

// TestSandboxGoneWrapMatchesProductionForm guards the exact error construction
// used in NewDaytonaPTYUpstream: fmt.Errorf(..., errors.Join(ErrDaytonaSandboxGone,
// rawSDKNotFound)). classifyAttachErr keys on errors.Is(_, ErrDaytonaSandboxGone),
// so if that join or the %w verb regresses, the 4003 close path silently stops
// firing and the reconnect flood returns. This asserts both the sentinel match
// (what the handler checks) and that the raw typed error survives for logging.
func TestSandboxGoneWrapMatchesProductionForm(t *testing.T) {
	rawNotFound := daytonaerrors.NewDaytonaNotFoundError("Sandbox with ID or name x not found", nil)
	// Mirror daytona_upstream.go's NewDaytonaPTYUpstream wrap verbatim.
	got := fmt.Errorf("daytona get sandbox %q: %w", "x", errors.Join(ErrDaytonaSandboxGone, rawNotFound))

	if !errors.Is(got, ErrDaytonaSandboxGone) {
		t.Fatal("production wrap must satisfy errors.Is(_, ErrDaytonaSandboxGone) — 4003 path depends on it")
	}
	if !isDaytonaSandboxNotFound(got) {
		t.Fatal("production wrap must keep the typed DaytonaNotFoundError detectable")
	}
}
