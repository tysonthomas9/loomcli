package sessions

import (
	"regexp"
	"sync"
	"testing"
)

// idPattern validates the full session ID format:
// YYYYMMDD-HHMMSS-<agent>-<taskshort>-<8hexrand>
// taskshort may be empty, producing a double-dash segment.
var idPattern = regexp.MustCompile(
	`^\d{8}-\d{6}-[a-zA-Z0-9_.\-]+-[a-zA-Z0-9_.\-]*-[0-9a-f]{8}$`,
)

func TestGenerateSessionID_Format(t *testing.T) {
	tests := []struct {
		agent string
		task  string
	}{
		{"nova", "abc123"},
		{"ember", "xyz"},
		{"falcon.v2", "task-99"},
		{"agent_1", "short"},
	}

	for _, tt := range tests {
		id, err := GenerateSessionID(tt.agent, tt.task)
		if err != nil {
			t.Fatalf("GenerateSessionID(%q, %q) error: %v", tt.agent, tt.task, err)
		}
		if !idPattern.MatchString(id) {
			t.Errorf("GenerateSessionID(%q, %q) = %q, does not match pattern %s",
				tt.agent, tt.task, id, idPattern.String())
		}
	}
}

func TestGenerateSessionID_EmptyTaskShort(t *testing.T) {
	id, err := GenerateSessionID("nova", "")
	if err != nil {
		t.Fatalf("GenerateSessionID with empty task short error: %v", err)
	}

	// With empty taskShort, the ID should contain a double-dash segment
	// between the agent name and the hex suffix.
	doubleDash := regexp.MustCompile(`-nova--[0-9a-f]{8}$`)
	if !doubleDash.MatchString(id) {
		t.Errorf("expected double-dash segment for empty taskShort, got %q", id)
	}

	// Should still match the overall pattern (taskshort portion is empty).
	if !idPattern.MatchString(id) {
		t.Errorf("ID %q does not match pattern %s", id, idPattern.String())
	}
}

func TestGenerateSessionID_Uniqueness(t *testing.T) {
	const n = 1000

	ids := make([]string, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			ids[idx], errs[idx] = GenerateSessionID("agent", "task")
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d error: %v", i, err)
		}
	}

	seen := make(map[string]struct{}, n)
	for i, id := range ids {
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ID at index %d: %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

func TestGenerateSessionID_InvalidAgent(t *testing.T) {
	tests := []struct {
		name  string
		agent string
	}{
		{"empty", ""},
		{"spaces", "my agent"},
		{"slash", "agent/bad"},
		{"at sign", "agent@host"},
		{"colon", "agent:1"},
		{"exclamation", "agent!"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := GenerateSessionID(tt.agent, "task")
			if err == nil {
				t.Errorf("GenerateSessionID(%q, \"task\") expected error, got nil", tt.agent)
			}
		})
	}
}
