package worker

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
)

func TestBuildAgentServicePatch(t *testing.T) {
	t.Run("desired state", func(t *testing.T) {
		patch, err := buildAgentServicePatch("desired_state", "paused", false)
		if err != nil {
			t.Fatalf("buildAgentServicePatch: %v", err)
		}
		if patch.DesiredState == nil || *patch.DesiredState != agents.DesiredPaused {
			t.Fatalf("desired_state = %#v, want paused", patch.DesiredState)
		}
	})

	t.Run("max instances validates", func(t *testing.T) {
		patch, err := buildAgentServicePatch("max_instances", "3", false)
		if err != nil {
			t.Fatalf("buildAgentServicePatch: %v", err)
		}
		if patch.MaxInstances == nil || *patch.MaxInstances != 3 {
			t.Fatalf("max_instances = %#v, want 3", patch.MaxInstances)
		}
		if _, err := buildAgentServicePatch("max_instances", "0", false); err == nil {
			t.Fatal("max_instances=0 err = nil, want error")
		}
	})

	t.Run("lists and metadata", func(t *testing.T) {
		patch, err := buildAgentServicePatch("event_sources", "github, slack, ,ci", false)
		if err != nil {
			t.Fatalf("buildAgentServicePatch event_sources: %v", err)
		}
		if patch.EventSources == nil || len(*patch.EventSources) != 3 || (*patch.EventSources)[1] != "slack" {
			t.Fatalf("event_sources = %#v, want parsed list", patch.EventSources)
		}

		patch, err = buildAgentServicePatch("metadata", "tier=gold, queue=primary", false)
		if err != nil {
			t.Fatalf("buildAgentServicePatch metadata: %v", err)
		}
		if patch.Metadata == nil || (*patch.Metadata)["tier"] != "gold" || (*patch.Metadata)["queue"] != "primary" {
			t.Fatalf("metadata = %#v, want parsed metadata", patch.Metadata)
		}
	})

	t.Run("unset optional and required", func(t *testing.T) {
		patch, err := buildAgentServicePatch("lease_id", "", true)
		if err != nil {
			t.Fatalf("buildAgentServicePatch lease_id unset: %v", err)
		}
		if patch.LeaseID == nil || *patch.LeaseID != "" {
			t.Fatalf("lease_id = %#v, want empty string patch", patch.LeaseID)
		}
		if _, err := buildAgentServicePatch("kind", "", true); err == nil {
			t.Fatal("unset kind err = nil, want error")
		}
	})
}

func TestAgentServiceParsingHelpers(t *testing.T) {
	if kind, err := parseAgentServiceKind("lead"); err != nil || kind != agents.AgentKindLead {
		t.Fatalf("parseAgentServiceKind = %q err=%v, want lead", kind, err)
	}
	if _, err := parseAgentServiceKind("bad"); err == nil {
		t.Fatal("parseAgentServiceKind bad err = nil, want error")
	}
	if state, err := parseAgentServiceDesiredState("running", false); err != nil || state != agents.DesiredRunning {
		t.Fatalf("parseAgentServiceDesiredState = %q err=%v, want running", state, err)
	}
	if state, err := parseAgentServiceDesiredState("", true); err != nil || state != "" {
		t.Fatalf("parseAgentServiceDesiredState empty = %q err=%v, want empty nil error", state, err)
	}
}
