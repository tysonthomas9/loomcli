//go:build sessionsreplay

package svcimpl

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// X3: remote-only metadata names an expected path, not transcript content.
func TestSessionsReplayX3RemoteTranscriptFlag(t *testing.T) {
	item := &service.SessionListItem{}
	record := &domain.AgentSession{
		SessionID: "remote-only",
		Metadata:  map[string]string{"transcript_path": "/remote/expected/transcript.jsonl"},
	}
	fillControlPlaneArtifactFlags(item, nil, record)
	if item.HasTranscript {
		t.Fatal("X3: remote-only session claims transcript evidence from an expected path")
	}
}
