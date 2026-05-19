package sessions

import (
	"reflect"
	"testing"
)

func TestDiffStatsMetadataRoundTripAndEmptyValues(t *testing.T) {
	metadata := map[string]string{}
	EncodeDiffStatsMetadata(metadata, DiffStats{
		FilesChanged: 3,
		LinesAdded:   12,
		LinesRemoved: 4,
	}, []string{"internal/a.go", "README.md"}, true)

	got := DecodeDiffStatsMetadata(metadata)
	if got.FilesChanged != 3 || got.LinesAdded != 12 || got.LinesRemoved != 4 {
		t.Fatalf("decoded stats = %+v", got.DiffStats)
	}
	if !reflect.DeepEqual(got.FilesTouched, []string{"internal/a.go", "README.md"}) {
		t.Fatalf("files touched = %#v", got.FilesTouched)
	}
	if got.DiffPath != "diff.patch" {
		t.Fatalf("DiffPath = %q, want diff.patch", got.DiffPath)
	}

	empty := DecodeDiffStatsMetadata(nil)
	if empty.DiffStats != (DiffStats{}) || empty.FilesTouched != nil || empty.DiffPath != "" {
		t.Fatalf("nil metadata decoded as %+v", empty)
	}

	invalid := DecodeDiffStatsMetadata(map[string]string{
		MetadataFilesChanged: "not-an-int",
		MetadataFilesTouched: "",
	})
	if invalid.FilesChanged != 0 || invalid.FilesTouched != nil {
		t.Fatalf("invalid metadata decoded as %+v", invalid)
	}

	noOptional := map[string]string{}
	EncodeDiffStatsMetadata(noOptional, DiffStats{FilesChanged: 1}, nil, false)
	if _, ok := noOptional[MetadataFilesTouched]; ok {
		t.Fatalf("files_touched should be omitted when empty: %#v", noOptional)
	}
	if _, ok := noOptional[MetadataDiffPath]; ok {
		t.Fatalf("diff_path should be omitted when no patch is present: %#v", noOptional)
	}
}
