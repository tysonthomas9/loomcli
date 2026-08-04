package entity

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSessionStatus_IsValid(t *testing.T) {
	tests := []struct {
		status SessionStatus
		want   bool
	}{
		{SessionRunning, true},
		{SessionCompleted, true},
		{SessionFailed, true},
		{SessionAborted, true},
		{"", true},
		{"bogus", false},
		{"RUNNING", false},
		{"pending", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsValid(); got != tt.want {
				t.Errorf("SessionStatus(%q).IsValid() = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestTokenUsage_IsZero(t *testing.T) {
	tests := []struct {
		name  string
		usage TokenUsage
		want  bool
	}{
		{"all zeros", TokenUsage{}, true},
		{"input tokens non-zero", TokenUsage{InputTokens: 1}, false},
		{"output tokens non-zero", TokenUsage{OutputTokens: 1}, false},
		{"cache read tokens non-zero", TokenUsage{CacheReadTokens: 1}, false},
		{"cache write tokens non-zero", TokenUsage{CacheWriteTokens: 1}, false},
		{"estimated cost non-zero", TokenUsage{EstimatedCostUSD: 0.01}, false},
		{"all non-zero", TokenUsage{
			InputTokens:      100,
			OutputTokens:     200,
			CacheReadTokens:  50,
			CacheWriteTokens: 25,
			EstimatedCostUSD: 0.05,
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.usage.IsZero(); got != tt.want {
				t.Errorf("TokenUsage.IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTokenUsage_Total(t *testing.T) {
	tests := []struct {
		name  string
		usage TokenUsage
		want  int64
	}{
		{"all zeros", TokenUsage{}, 0},
		{"mixed values", TokenUsage{
			InputTokens:      100,
			OutputTokens:     200,
			CacheReadTokens:  50,
			CacheWriteTokens: 25,
		}, 375},
		{"cost not included in total", TokenUsage{
			InputTokens:      100,
			OutputTokens:     200,
			CacheReadTokens:  50,
			CacheWriteTokens: 25,
			EstimatedCostUSD: 999.99,
		}, 375},
		{"single field", TokenUsage{InputTokens: 42}, 42},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.usage.Total(); got != tt.want {
				t.Errorf("TokenUsage.Total() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDiffStats_IsZero(t *testing.T) {
	tests := []struct {
		name  string
		stats DiffStats
		want  bool
	}{
		{"all zeros", DiffStats{}, true},
		{"files changed non-zero", DiffStats{FilesChanged: 1}, false},
		{"lines added non-zero", DiffStats{LinesAdded: 1}, false},
		{"lines removed non-zero", DiffStats{LinesRemoved: 1}, false},
		{"all non-zero", DiffStats{FilesChanged: 3, LinesAdded: 10, LinesRemoved: 5}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.stats.IsZero(); got != tt.want {
				t.Errorf("DiffStats.IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDiffStats_TotalLines(t *testing.T) {
	tests := []struct {
		name  string
		stats DiffStats
		want  int
	}{
		{"all zeros", DiffStats{}, 0},
		{"only added", DiffStats{LinesAdded: 10}, 10},
		{"only removed", DiffStats{LinesRemoved: 5}, 5},
		{"sum of added and removed", DiffStats{LinesAdded: 10, LinesRemoved: 5}, 15},
		{"files changed not included", DiffStats{FilesChanged: 99, LinesAdded: 10, LinesRemoved: 5}, 15},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.stats.TotalLines(); got != tt.want {
				t.Errorf("DiffStats.TotalLines() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSession_Validate(t *testing.T) {
	now := time.Now()
	validSession := func() *Session {
		return &Session{
			SessionID:  "sess-001",
			AgentName:  "polecat",
			Backend:    "claude",
			Status:     SessionRunning,
			StartedAt:  now,
			AttemptNum: 1,
		}
	}

	t.Run("valid session with all fields passes", func(t *testing.T) {
		ended := now.Add(5 * time.Minute)
		s := validSession()
		s.TaskID = "task-001"
		s.EpicID = "epic-001"
		s.Model = "opus"
		s.Phase = "planning"
		s.EndedAt = &ended
		s.DurationS = 300.0
		s.ExitCode = 0
		s.ErrorClass = ""
		s.TokenUsage = TokenUsage{InputTokens: 100, OutputTokens: 200}
		s.DiffStats = DiffStats{FilesChanged: 3, LinesAdded: 50, LinesRemoved: 10}
		if err := s.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("minimal valid session passes", func(t *testing.T) {
		s := &Session{
			SessionID: "sess-002",
			AgentName: "polecat",
			Backend:   "claude",
			StartedAt: now,
		}
		if err := s.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty SessionID fails", func(t *testing.T) {
		s := validSession()
		s.SessionID = ""
		err := s.Validate()
		if err == nil {
			t.Error("expected error for empty SessionID")
		} else if !strings.Contains(err.Error(), "session_id is required") {
			t.Errorf("error %q should contain %q", err.Error(), "session_id is required")
		}
	})

	t.Run("empty AgentName fails", func(t *testing.T) {
		s := validSession()
		s.AgentName = ""
		err := s.Validate()
		if err == nil {
			t.Error("expected error for empty AgentName")
		} else if !strings.Contains(err.Error(), "agent_name is required") {
			t.Errorf("error %q should contain %q", err.Error(), "agent_name is required")
		}
	})

	t.Run("empty Backend fails", func(t *testing.T) {
		s := validSession()
		s.Backend = ""
		err := s.Validate()
		if err == nil {
			t.Error("expected error for empty Backend")
		} else if !strings.Contains(err.Error(), "backend is required") {
			t.Errorf("error %q should contain %q", err.Error(), "backend is required")
		}
	})

	t.Run("invalid status fails", func(t *testing.T) {
		s := validSession()
		s.Status = "bogus"
		err := s.Validate()
		if err == nil {
			t.Error("expected error for invalid status")
		} else if !strings.Contains(err.Error(), "invalid session status") {
			t.Errorf("error %q should contain %q", err.Error(), "invalid session status")
		}
	})

	t.Run("valid status passes", func(t *testing.T) {
		for _, status := range []SessionStatus{SessionRunning, SessionCompleted, SessionFailed, SessionAborted} {
			s := validSession()
			s.Status = status
			if err := s.Validate(); err != nil {
				t.Errorf("unexpected error for status %q: %v", status, err)
			}
		}
	})

	t.Run("empty status passes", func(t *testing.T) {
		s := validSession()
		s.Status = ""
		if err := s.Validate(); err != nil {
			t.Errorf("unexpected error for empty status: %v", err)
		}
	})

	t.Run("zero StartedAt fails", func(t *testing.T) {
		s := validSession()
		s.StartedAt = time.Time{}
		err := s.Validate()
		if err == nil {
			t.Error("expected error for zero StartedAt")
		} else if !strings.Contains(err.Error(), "started_at is required") {
			t.Errorf("error %q should contain %q", err.Error(), "started_at is required")
		}
	})

	t.Run("EndedAt before StartedAt fails", func(t *testing.T) {
		s := validSession()
		before := now.Add(-1 * time.Minute)
		s.EndedAt = &before
		err := s.Validate()
		if err == nil {
			t.Error("expected error for EndedAt before StartedAt")
		} else if !strings.Contains(err.Error(), "ended_at must not be before started_at") {
			t.Errorf("error %q should contain %q", err.Error(), "ended_at must not be before started_at")
		}
	})

	t.Run("EndedAt equal to StartedAt passes", func(t *testing.T) {
		s := validSession()
		same := s.StartedAt
		s.EndedAt = &same
		if err := s.Validate(); err != nil {
			t.Errorf("unexpected error for EndedAt == StartedAt: %v", err)
		}
	})

	t.Run("EndedAt after StartedAt passes", func(t *testing.T) {
		s := validSession()
		after := now.Add(5 * time.Minute)
		s.EndedAt = &after
		if err := s.Validate(); err != nil {
			t.Errorf("unexpected error for EndedAt after StartedAt: %v", err)
		}
	})

	t.Run("nil EndedAt passes", func(t *testing.T) {
		s := validSession()
		s.EndedAt = nil
		if err := s.Validate(); err != nil {
			t.Errorf("unexpected error for nil EndedAt: %v", err)
		}
	})

	t.Run("negative DurationS fails", func(t *testing.T) {
		s := validSession()
		s.DurationS = -1.0
		err := s.Validate()
		if err == nil {
			t.Error("expected error for negative DurationS")
		} else if !strings.Contains(err.Error(), "duration_s must not be negative") {
			t.Errorf("error %q should contain %q", err.Error(), "duration_s must not be negative")
		}
	})

	t.Run("zero DurationS passes", func(t *testing.T) {
		s := validSession()
		s.DurationS = 0
		if err := s.Validate(); err != nil {
			t.Errorf("unexpected error for zero DurationS: %v", err)
		}
	})

	t.Run("negative AttemptNum fails", func(t *testing.T) {
		s := validSession()
		s.AttemptNum = -1
		err := s.Validate()
		if err == nil {
			t.Error("expected error for negative AttemptNum")
		} else if !strings.Contains(err.Error(), "attempt_num must not be negative") {
			t.Errorf("error %q should contain %q", err.Error(), "attempt_num must not be negative")
		}
	})

	t.Run("zero AttemptNum passes", func(t *testing.T) {
		s := validSession()
		s.AttemptNum = 0
		if err := s.Validate(); err != nil {
			t.Errorf("unexpected error for zero AttemptNum: %v", err)
		}
	})

	t.Run("empty TaskID passes", func(t *testing.T) {
		s := validSession()
		s.TaskID = ""
		if err := s.Validate(); err != nil {
			t.Errorf("unexpected error for empty TaskID: %v", err)
		}
	})

	t.Run("phase planning passes", func(t *testing.T) {
		s := validSession()
		s.Phase = "planning"
		if err := s.Validate(); err != nil {
			t.Errorf("unexpected error for phase 'planning': %v", err)
		}
	})

	t.Run("phase implementation passes", func(t *testing.T) {
		s := validSession()
		s.Phase = "implementation"
		if err := s.Validate(); err != nil {
			t.Errorf("unexpected error for phase 'implementation': %v", err)
		}
	})

	t.Run("empty phase passes", func(t *testing.T) {
		s := validSession()
		s.Phase = ""
		if err := s.Validate(); err != nil {
			t.Errorf("unexpected error for empty phase: %v", err)
		}
	})

	t.Run("unknown phase fails", func(t *testing.T) {
		s := validSession()
		s.Phase = "unknown"
		err := s.Validate()
		if err == nil {
			t.Error("expected error for unknown phase")
		} else if !strings.Contains(err.Error(), "phase must be") {
			t.Errorf("error %q should contain %q", err.Error(), "phase must be")
		}
	})
}

func TestSession_SetDefaults(t *testing.T) {
	t.Run("empty fields get defaults", func(t *testing.T) {
		s := &Session{}
		s.SetDefaults()
		if s.Status != SessionRunning {
			t.Errorf("Status = %q, want %q", s.Status, SessionRunning)
		}
		if s.AttemptNum != 1 {
			t.Errorf("AttemptNum = %d, want %d", s.AttemptNum, 1)
		}
	})

	t.Run("existing Status preserved", func(t *testing.T) {
		s := &Session{Status: SessionCompleted}
		s.SetDefaults()
		if s.Status != SessionCompleted {
			t.Errorf("Status = %q, want %q", s.Status, SessionCompleted)
		}
	})

	t.Run("existing AttemptNum preserved", func(t *testing.T) {
		s := &Session{AttemptNum: 3}
		s.SetDefaults()
		if s.AttemptNum != 3 {
			t.Errorf("AttemptNum = %d, want %d", s.AttemptNum, 3)
		}
	})
}

func TestSession_IsActive(t *testing.T) {
	tests := []struct {
		name   string
		status SessionStatus
		want   bool
	}{
		{"running is active", SessionRunning, true},
		{"completed is not active", SessionCompleted, false},
		{"failed is not active", SessionFailed, false},
		{"aborted is not active", SessionAborted, false},
		{"empty is not active", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Session{Status: tt.status}
			if got := s.IsActive(); got != tt.want {
				t.Errorf("Session{Status: %q}.IsActive() = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestSession_IsTerminal(t *testing.T) {
	tests := []struct {
		name   string
		status SessionStatus
		want   bool
	}{
		{"completed is terminal", SessionCompleted, true},
		{"failed is terminal", SessionFailed, true},
		{"aborted is terminal", SessionAborted, true},
		{"running is not terminal", SessionRunning, false},
		{"empty is not terminal", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Session{Status: tt.status}
			if got := s.IsTerminal(); got != tt.want {
				t.Errorf("Session{Status: %q}.IsTerminal() = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestSession_Succeeded(t *testing.T) {
	tests := []struct {
		name     string
		status   SessionStatus
		exitCode int
		want     bool
	}{
		{"completed with exit 0", SessionCompleted, 0, true},
		{"completed with exit 1", SessionCompleted, 1, false},
		{"failed with exit 0", SessionFailed, 0, false},
		{"failed with exit 1", SessionFailed, 1, false},
		{"running with exit 0", SessionRunning, 0, false},
		{"aborted with exit 0", SessionAborted, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Session{Status: tt.status, ExitCode: tt.exitCode}
			if got := s.Succeeded(); got != tt.want {
				t.Errorf("Session{Status: %q, ExitCode: %d}.Succeeded() = %v, want %v",
					tt.status, tt.exitCode, got, tt.want)
			}
		})
	}
}

func TestSession_JSON_Serialization(t *testing.T) {
	t.Run("token usage and diff stats nested as objects", func(t *testing.T) {
		now := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
		s := &Session{
			SessionID: "sess-json-001",
			AgentName: "polecat",
			Backend:   "claude",
			StartedAt: now,
			Status:    SessionRunning,
			TokenUsage: TokenUsage{
				InputTokens:      100,
				OutputTokens:     200,
				CacheReadTokens:  50,
				CacheWriteTokens: 25,
				EstimatedCostUSD: 0.05,
			},
			DiffStats: DiffStats{
				FilesChanged: 3,
				LinesAdded:   50,
				LinesRemoved: 10,
			},
		}

		data, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("json.Unmarshal to map failed: %v", err)
		}

		// Verify token_usage is present and is an object
		tu, ok := raw["token_usage"]
		if !ok {
			t.Fatal("expected token_usage key in JSON")
		}
		var tuMap map[string]any
		if err := json.Unmarshal(tu, &tuMap); err != nil {
			t.Fatalf("token_usage is not a JSON object: %v", err)
		}
		if tuMap["input_tokens"] != float64(100) {
			t.Errorf("token_usage.input_tokens = %v, want 100", tuMap["input_tokens"])
		}
		if tuMap["output_tokens"] != float64(200) {
			t.Errorf("token_usage.output_tokens = %v, want 200", tuMap["output_tokens"])
		}

		// Verify diff_stats is present and is an object
		ds, ok := raw["diff_stats"]
		if !ok {
			t.Fatal("expected diff_stats key in JSON")
		}
		var dsMap map[string]any
		if err := json.Unmarshal(ds, &dsMap); err != nil {
			t.Fatalf("diff_stats is not a JSON object: %v", err)
		}
		if dsMap["files_changed"] != float64(3) {
			t.Errorf("diff_stats.files_changed = %v, want 3", dsMap["files_changed"])
		}
		if dsMap["lines_added"] != float64(50) {
			t.Errorf("diff_stats.lines_added = %v, want 50", dsMap["lines_added"])
		}
		if dsMap["lines_removed"] != float64(10) {
			t.Errorf("diff_stats.lines_removed = %v, want 10", dsMap["lines_removed"])
		}
	})

	t.Run("nil EndedAt omitted from JSON", func(t *testing.T) {
		now := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
		s := &Session{
			SessionID: "sess-json-002",
			AgentName: "polecat",
			Backend:   "claude",
			StartedAt: now,
			Status:    SessionRunning,
			EndedAt:   nil,
		}

		data, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("json.Unmarshal to map failed: %v", err)
		}

		if _, ok := raw["ended_at"]; ok {
			t.Error("expected ended_at to be omitted when nil")
		}
	})

	t.Run("non-nil EndedAt present in JSON", func(t *testing.T) {
		now := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
		ended := now.Add(5 * time.Minute)
		s := &Session{
			SessionID: "sess-json-003",
			AgentName: "polecat",
			Backend:   "claude",
			StartedAt: now,
			Status:    SessionCompleted,
			EndedAt:   &ended,
		}

		data, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("json.Unmarshal to map failed: %v", err)
		}

		if _, ok := raw["ended_at"]; !ok {
			t.Error("expected ended_at to be present when non-nil")
		}
	})

	t.Run("round-trip preserves values", func(t *testing.T) {
		now := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
		ended := now.Add(10 * time.Minute)
		original := &Session{
			SessionID:  "sess-rt-001",
			TaskID:     "task-001",
			EpicID:     "epic-001",
			AgentName:  "polecat",
			Backend:    "claude",
			Model:      "opus",
			Phase:      "planning",
			StartedAt:  now,
			EndedAt:    &ended,
			DurationS:  600.0,
			Status:     SessionCompleted,
			ExitCode:   0,
			ErrorClass: "",
			TokenUsage: TokenUsage{
				InputTokens:      1000,
				OutputTokens:     500,
				CacheReadTokens:  200,
				CacheWriteTokens: 100,
				EstimatedCostUSD: 0.15,
			},
			DiffStats: DiffStats{
				FilesChanged: 5,
				LinesAdded:   120,
				LinesRemoved: 30,
			},
			AttemptNum: 2,
		}

		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}

		var decoded Session
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("json.Unmarshal failed: %v", err)
		}

		if decoded.SessionID != original.SessionID {
			t.Errorf("SessionID = %q, want %q", decoded.SessionID, original.SessionID)
		}
		if decoded.TaskID != original.TaskID {
			t.Errorf("TaskID = %q, want %q", decoded.TaskID, original.TaskID)
		}
		if decoded.AgentName != original.AgentName {
			t.Errorf("AgentName = %q, want %q", decoded.AgentName, original.AgentName)
		}
		if decoded.Backend != original.Backend {
			t.Errorf("Backend = %q, want %q", decoded.Backend, original.Backend)
		}
		if decoded.Status != original.Status {
			t.Errorf("Status = %q, want %q", decoded.Status, original.Status)
		}
		if decoded.TokenUsage.InputTokens != original.TokenUsage.InputTokens {
			t.Errorf("TokenUsage.InputTokens = %d, want %d", decoded.TokenUsage.InputTokens, original.TokenUsage.InputTokens)
		}
		if decoded.TokenUsage.OutputTokens != original.TokenUsage.OutputTokens {
			t.Errorf("TokenUsage.OutputTokens = %d, want %d", decoded.TokenUsage.OutputTokens, original.TokenUsage.OutputTokens)
		}
		if decoded.DiffStats.FilesChanged != original.DiffStats.FilesChanged {
			t.Errorf("DiffStats.FilesChanged = %d, want %d", decoded.DiffStats.FilesChanged, original.DiffStats.FilesChanged)
		}
		if decoded.AttemptNum != original.AttemptNum {
			t.Errorf("AttemptNum = %d, want %d", decoded.AttemptNum, original.AttemptNum)
		}
		if decoded.EndedAt == nil {
			t.Fatal("EndedAt should not be nil after round-trip")
		}
		if !decoded.EndedAt.Equal(*original.EndedAt) {
			t.Errorf("EndedAt = %v, want %v", decoded.EndedAt, original.EndedAt)
		}
	})
}
