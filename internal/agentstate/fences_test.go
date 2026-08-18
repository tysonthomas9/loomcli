package agentstate

import (
	"strings"
	"testing"
)

// The merge is the only implementation in the tree — the scout leaf stages a
// raw block and both `loom scout diff` and `loom scout approve` come through
// here. agents.md is user-authored, so these cases are about not eating bytes.

func TestMergePendingFenceCreatesWhenCurrentIsEmpty(t *testing.T) {
	begin, end := FenceMarkers("scout")
	pending := begin + "\nnew\n" + end
	merged, err := MergePendingFence("", pending, "scout")
	if err != nil {
		t.Fatalf("MergePendingFence: %v", err)
	}
	if merged != pending+"\n" {
		t.Fatalf("merged = %q, want the block plus a trailing newline", merged)
	}
}

func TestMergePendingFenceAppendsBesideHumanBytes(t *testing.T) {
	begin, end := FenceMarkers("scout")
	current := "# Handwritten\n\nhuman only\n"
	merged, err := MergePendingFence(current, begin+"\nnew\n"+end, "scout")
	if err != nil {
		t.Fatalf("MergePendingFence: %v", err)
	}
	if got := merged[:len(current)]; got != current {
		t.Fatalf("prefix = %q, want the current file byte-identical", got)
	}
	if !strings.Contains(merged, "new") {
		t.Fatalf("merged = %q, want the staged content", merged)
	}
}

func TestMergePendingFenceReplacesOwnRegionAndPreservesOthers(t *testing.T) {
	westBegin, westEnd := FenceMarkers("scout-west")
	east := "<!-- loom:agent:scout-east:begin -->\nEAST\r\nbytes\n<!-- loom:agent:scout-east:end -->"
	current := "human-prefix\n" + east + "\nhuman-middle\n" +
		westBegin + "\nold\n" + westEnd + "\nhuman-suffix"

	merged, err := MergePendingFence(current, westBegin+"\nnew west\n"+westEnd, "scout-west")
	if err != nil {
		t.Fatalf("MergePendingFence: %v", err)
	}
	if !strings.Contains(merged, "new west") || strings.Contains(merged, "\nold\n") {
		t.Fatalf("merged = %q, want the west region replaced", merged)
	}
	// Another instance's region must survive byte-for-byte, CRLF included.
	if !strings.Contains(merged, east) {
		t.Fatalf("merged = %q, want the east region byte-identical", merged)
	}
	if !strings.HasPrefix(merged, "human-prefix\n") || !strings.HasSuffix(merged, "\nhuman-suffix") {
		t.Fatalf("merged = %q, want human bytes preserved at both ends", merged)
	}
}

func TestMergePendingFenceKeepsFirstPairAndDropsDuplicates(t *testing.T) {
	begin, end := FenceMarkers("scout-west")
	east := "<!-- loom:agent:scout-east:begin -->\neast exact\n<!-- loom:agent:scout-east:end -->"
	current := "head\n" + begin + "\nfirst old\n" + end +
		"\nbetween\n" + begin + "\nduplicate old\n" + end + "\n" + east + "\ntail"

	merged, err := MergePendingFence(current, begin+"\nwinner\n"+end, "scout-west")
	if err != nil {
		t.Fatalf("MergePendingFence: %v", err)
	}
	if strings.Count(merged, begin) != 1 {
		t.Fatalf("merged = %q, want exactly one begin marker", merged)
	}
	if strings.Contains(merged, "first old") || strings.Contains(merged, "duplicate old") {
		t.Fatalf("merged = %q, want both old regions gone", merged)
	}
	if !strings.Contains(merged, east) {
		t.Fatalf("merged = %q, want the east region intact", merged)
	}
}

// An unmatched begin marker is still this instance's, so it is replaced in
// place rather than leaving a second ambiguous begin behind.
func TestMergePendingFenceRecoversFromUnmatchedBeginMarker(t *testing.T) {
	begin, end := FenceMarkers("scout")
	current := "head\n" + begin + "\norphan\ntail"
	merged, err := MergePendingFence(current, begin+"\nfresh\n"+end, "scout")
	if err != nil {
		t.Fatalf("MergePendingFence: %v", err)
	}
	if strings.Count(merged, begin) != 1 || !strings.Contains(merged, "fresh") {
		t.Fatalf("merged = %q, want one recovered region", merged)
	}
	if !strings.HasPrefix(merged, "head\n") || !strings.HasSuffix(merged, "tail") {
		t.Fatalf("merged = %q, want surrounding bytes preserved", merged)
	}
}

func TestMergePendingFenceRejectsStagedContentWithNoCompletePair(t *testing.T) {
	begin, _ := FenceMarkers("scout")
	for name, pending := range map[string]string{
		"empty":           "",
		"no markers":      "just content",
		"unmatched begin": begin + "\ncontent",
		"other instance":  "<!-- loom:agent:other:begin -->\nx\n<!-- loom:agent:other:end -->",
	} {
		if _, err := MergePendingFence("current", pending, "scout"); err == nil {
			t.Fatalf("%s: MergePendingFence err = nil, want a refusal", name)
		}
	}
}

func TestStripFenceMarkerLinesKeepsContentAndOtherLines(t *testing.T) {
	begin, end := FenceMarkers("scout")
	got := StripFenceMarkerLines("human\n" + begin + "\ninside\n" + end + "\nmore")
	if got != "human\ninside\nmore" {
		t.Fatalf("StripFenceMarkerLines = %q", got)
	}
}
