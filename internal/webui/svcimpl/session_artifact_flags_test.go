package svcimpl

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestFillControlPlaneArtifactFlagsRemoteOnlyUsesRefs(t *testing.T) {
	tests := []struct {
		name          string
		metadata      map[string]string
		hasTranscript bool
		hasDiff       bool
	}{
		{
			name: "paths only are not evidence",
			metadata: map[string]string{
				"transcript_path": "/daemon/transcript.jsonl",
				"diff_path":       "/daemon/diff.patch",
			},
		},
		{
			name: "refs are evidence",
			metadata: map[string]string{
				"transcript_ref": "artifact://transcript-1",
				"patch_ref":      "artifact://patch-1",
			},
			hasTranscript: true,
			hasDiff:       true,
		},
		{
			name: "paths and refs use refs",
			metadata: map[string]string{
				"transcript_path": "/daemon/transcript.jsonl",
				"transcript_ref":  "artifact://transcript-1",
				"diff_path":       "/daemon/diff.patch",
				"diff_ref":        "artifact://patch-1",
			},
			hasTranscript: true,
			hasDiff:       true,
		},
		{
			// The daemon uploads a transcript artifact but no patch artifact, so
			// this is the shape a real daemon session arrives in: the transcript
			// is claimable and the diff is not.
			name: "daemon session has a transcript ref and no patch ref",
			metadata: map[string]string{
				"transcript_ref": "artifact://transcript-1",
				"diff_path":      "diff.patch",
				"files_changed":  "4",
			},
			hasTranscript: true,
		},
		{
			// Only the keys controlPlaneDiffArtifactRef resolves count. An
			// unrecognized ref key is not something the reader can follow.
			name: "unknown ref keys are not evidence",
			metadata: map[string]string{
				"transcript_artifact": "artifact://transcript-1",
				"patch_path":          "artifact://patch-1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := &service.SessionListItem{}
			rec := &domain.AgentSession{SessionID: "session-1", Metadata: tt.metadata}
			fillControlPlaneArtifactFlags(item, nil, rec)
			if item.HasTranscript != tt.hasTranscript || item.HasDiff != tt.hasDiff {
				t.Fatalf("flags = %v/%v, want %v/%v", item.HasTranscript, item.HasDiff, tt.hasTranscript, tt.hasDiff)
			}
		})
	}
}
