package workspace

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoomGitMirrorStatusIncludesFailureReason(t *testing.T) {
	ref := "refs/loom/ws/W/attempt/A/capture"
	summary := summarizeLoomGitMirror([]loomGitMirrorRow{
		{Ref: ref, State: "not_mirrored", Reason: "provider rejected: file too large"},
	})
	if summary.Mirrored != 0 || summary.Pending != 0 || len(summary.NotMirrored) != 1 ||
		!strings.Contains(summary.NotMirrored[0], ref) || !strings.Contains(summary.NotMirrored[0], "file too large") {
		t.Fatalf("failure missing from workspace status: %+v", summary)
	}
	var out bytes.Buffer
	if err := writeLoomGitMirrorStatus(&out, summary); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Loom Git: 0 mirrored, 0 pending, 1 not mirrored") ||
		!strings.Contains(out.String(), ref+": provider rejected: file too large") {
		t.Fatalf("workspace status omitted mirror failure: %q", out.String())
	}
}
