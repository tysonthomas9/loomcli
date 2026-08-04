package sessions

import (
	"strconv"
	"strings"
)

const (
	MetadataFilesChanged = "files_changed"
	MetadataLinesAdded   = "lines_added"
	MetadataLinesRemoved = "lines_removed"
	MetadataFilesTouched = "files_touched"
	MetadataDiffPath     = "diff_path"
)

// DiffStatsMetadata is the string-map representation stored on control-plane
// session metadata for consumers that project AgentSession into SessionRecord.
type DiffStatsMetadata struct {
	DiffStats
	FilesTouched []string
	DiffPath     string
}

func EncodeDiffStatsMetadata(metadata map[string]string, stats DiffStats, filesTouched []string, hasDiffPatch bool) {
	metadata[MetadataFilesChanged] = strconv.Itoa(stats.FilesChanged)
	metadata[MetadataLinesAdded] = strconv.Itoa(stats.LinesAdded)
	metadata[MetadataLinesRemoved] = strconv.Itoa(stats.LinesRemoved)
	if len(filesTouched) > 0 {
		metadata[MetadataFilesTouched] = strings.Join(filesTouched, "\n")
	}
	if hasDiffPatch {
		metadata[MetadataDiffPath] = "diff.patch"
	}
}

func DecodeDiffStatsMetadata(metadata map[string]string) DiffStatsMetadata {
	if metadata == nil {
		return DiffStatsMetadata{}
	}
	return DiffStatsMetadata{
		DiffStats: DiffStats{
			FilesChanged: metadataInt(metadata, MetadataFilesChanged),
			LinesAdded:   metadataInt(metadata, MetadataLinesAdded),
			LinesRemoved: metadataInt(metadata, MetadataLinesRemoved),
		},
		FilesTouched: metadataList(metadata, MetadataFilesTouched),
		DiffPath:     metadata[MetadataDiffPath],
	}
}

func metadataInt(metadata map[string]string, key string) int {
	n, _ := strconv.Atoi(metadata[key])
	return n
}

func metadataList(metadata map[string]string, key string) []string {
	if metadata[key] == "" {
		return nil
	}
	return strings.Split(metadata[key], "\n")
}
