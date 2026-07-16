package worker

import (
	"testing"
)

func TestBuildWorkerProfilePatch(t *testing.T) {
	t.Run("set max priority", func(t *testing.T) {
		patch, err := buildWorkerProfilePatch("max_priority", "3", false)
		if err != nil {
			t.Fatalf("build patch: %v", err)
		}
		if patch.MaxPriority == nil || *patch.MaxPriority != 3 || patch.ClearMaxPriority {
			t.Fatalf("patch = %+v, want max_priority 3", patch)
		}
	})

	t.Run("unset max priority", func(t *testing.T) {
		patch, err := buildWorkerProfilePatch("max_priority", "", true)
		if err != nil {
			t.Fatalf("build patch: %v", err)
		}
		if !patch.ClearMaxPriority || patch.MaxPriority != nil {
			t.Fatalf("patch = %+v, want clear max priority", patch)
		}
	})

	t.Run("reject invalid max priority", func(t *testing.T) {
		if _, err := buildWorkerProfilePatch("max_priority", "5", false); err == nil {
			t.Fatalf("build patch err = nil, want validation error")
		}
	})

	t.Run("set list and metadata", func(t *testing.T) {
		patch, err := buildWorkerProfilePatch("repos", "api, worker, ,ui", false)
		if err != nil {
			t.Fatalf("build repos patch: %v", err)
		}
		if patch.Repos == nil || len(*patch.Repos) != 3 || (*patch.Repos)[1] != "worker" {
			t.Fatalf("repos patch = %+v, want three trimmed repos", patch)
		}

		patch, err = buildWorkerProfilePatch("metadata", "tier=gold, queue=primary", false)
		if err != nil {
			t.Fatalf("build metadata patch: %v", err)
		}
		if patch.Metadata == nil || (*patch.Metadata)["tier"] != "gold" || (*patch.Metadata)["queue"] != "primary" {
			t.Fatalf("metadata patch = %+v, want tier and queue", patch)
		}
	})

	t.Run("reject immutable unset", func(t *testing.T) {
		if _, err := buildWorkerProfilePatch("role", "", true); err == nil {
			t.Fatalf("build patch err = nil, want immutable unset error")
		}
	})
}

func TestWorkerProfileParsingHelpers(t *testing.T) {
	metadata, err := parseWorkerProfileMetadata([]string{"tier=gold", "queue=primary"})
	if err != nil {
		t.Fatalf("parse metadata: %v", err)
	}
	if metadata["tier"] != "gold" || metadata["queue"] != "primary" {
		t.Fatalf("metadata = %+v, want parsed entries", metadata)
	}
	if _, err := parseWorkerProfileMetadata([]string{"missing-separator"}); err == nil {
		t.Fatalf("parse metadata err = nil, want validation error")
	}

	enabled, err := parseOptionalBool("true")
	if err != nil {
		t.Fatalf("parse bool: %v", err)
	}
	if enabled == nil || !*enabled {
		t.Fatalf("enabled = %v, want true pointer", enabled)
	}
	enabled, err = parseOptionalBool("")
	if err != nil {
		t.Fatalf("parse empty bool: %v", err)
	}
	if enabled != nil {
		t.Fatalf("enabled = %v, want nil", *enabled)
	}
}
