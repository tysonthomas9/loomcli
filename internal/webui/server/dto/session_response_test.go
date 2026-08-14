package dto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestSessionResponse_JSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	endedAt := time.Date(2026, 4, 3, 12, 30, 0, 0, time.UTC)

	resp := SessionResponse{
		SessionID:        "sess-001",
		TaskID:           "task-42",
		EpicID:           "epic-10",
		AgentName:        "falcon",
		Backend:          "claude",
		Model:            "opus-4",
		Phase:            "implementation",
		StartedAt:        now,
		EndedAt:          &endedAt,
		DurationS:        1800.5,
		Status:           "completed",
		ExitCode:         0,
		InputTokens:      ptr(int64(50000)),
		OutputTokens:     ptr(int64(12000)),
		CacheReadTokens:  ptr(int64(3000)),
		CacheWriteTokens: ptr(int64(1500)),
		EstimatedCostUSD: ptr(0.42),
		FilesChanged:     3,
		LinesAdded:       100,
		LinesRemoved:     20,
		FilesTouched:     []string{"a.go", "b.go", "c.go"},
		AttemptNum:       1,
		ErrorClass:       "timeout",
		IsActive:         true,
		HasTranscript:    true,
		HasDiff:          true,
		LastError:        "context deadline exceeded",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got SessionResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.SessionID != resp.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, resp.SessionID)
	}
	if got.TaskID != resp.TaskID {
		t.Errorf("TaskID = %q, want %q", got.TaskID, resp.TaskID)
	}
	if got.EpicID != resp.EpicID {
		t.Errorf("EpicID = %q, want %q", got.EpicID, resp.EpicID)
	}
	if got.AgentName != resp.AgentName {
		t.Errorf("AgentName = %q, want %q", got.AgentName, resp.AgentName)
	}
	if got.Backend != resp.Backend {
		t.Errorf("Backend = %q, want %q", got.Backend, resp.Backend)
	}
	if got.Model != resp.Model {
		t.Errorf("Model = %q, want %q", got.Model, resp.Model)
	}
	if got.Phase != resp.Phase {
		t.Errorf("Phase = %q, want %q", got.Phase, resp.Phase)
	}
	if !got.StartedAt.Equal(resp.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, resp.StartedAt)
	}
	if got.EndedAt == nil || !got.EndedAt.Equal(endedAt) {
		t.Errorf("EndedAt = %v, want %v", got.EndedAt, endedAt)
	}
	if got.DurationS != resp.DurationS {
		t.Errorf("DurationS = %f, want %f", got.DurationS, resp.DurationS)
	}
	if got.Status != resp.Status {
		t.Errorf("Status = %q, want %q", got.Status, resp.Status)
	}
	if got.ExitCode != resp.ExitCode {
		t.Errorf("ExitCode = %d, want %d", got.ExitCode, resp.ExitCode)
	}
	if got.InputTokens == nil || *got.InputTokens != *resp.InputTokens {
		t.Errorf("InputTokens = %v, want %v", got.InputTokens, resp.InputTokens)
	}
	if got.OutputTokens == nil || *got.OutputTokens != *resp.OutputTokens {
		t.Errorf("OutputTokens = %v, want %v", got.OutputTokens, resp.OutputTokens)
	}
	if got.CacheReadTokens == nil || *got.CacheReadTokens != *resp.CacheReadTokens {
		t.Errorf("CacheReadTokens = %v, want %v", got.CacheReadTokens, resp.CacheReadTokens)
	}
	if got.CacheWriteTokens == nil || *got.CacheWriteTokens != *resp.CacheWriteTokens {
		t.Errorf("CacheWriteTokens = %v, want %v", got.CacheWriteTokens, resp.CacheWriteTokens)
	}
	if got.EstimatedCostUSD == nil || *got.EstimatedCostUSD != *resp.EstimatedCostUSD {
		t.Errorf("EstimatedCostUSD = %v, want %v", got.EstimatedCostUSD, resp.EstimatedCostUSD)
	}
	if got.FilesChanged != resp.FilesChanged {
		t.Errorf("FilesChanged = %d, want %d", got.FilesChanged, resp.FilesChanged)
	}
	if got.LinesAdded != resp.LinesAdded {
		t.Errorf("LinesAdded = %d, want %d", got.LinesAdded, resp.LinesAdded)
	}
	if got.LinesRemoved != resp.LinesRemoved {
		t.Errorf("LinesRemoved = %d, want %d", got.LinesRemoved, resp.LinesRemoved)
	}
	if len(got.FilesTouched) != 3 {
		t.Errorf("FilesTouched len = %d, want 3", len(got.FilesTouched))
	}
	if got.AttemptNum != resp.AttemptNum {
		t.Errorf("AttemptNum = %d, want %d", got.AttemptNum, resp.AttemptNum)
	}
	if got.ErrorClass != resp.ErrorClass {
		t.Errorf("ErrorClass = %q, want %q", got.ErrorClass, resp.ErrorClass)
	}
	if !got.IsActive {
		t.Error("IsActive = false, want true")
	}
	if !got.HasTranscript {
		t.Error("HasTranscript = false, want true")
	}
	if !got.HasDiff {
		t.Error("HasDiff = false, want true")
	}
	if got.LastError != resp.LastError {
		t.Errorf("LastError = %q, want %q", got.LastError, resp.LastError)
	}
}

func TestSessionResponse_MinimalJSON(t *testing.T) {
	input := `{"session_id":"s1","agent_name":"a","status":"running","started_at":"2026-04-03T12:00:00Z"}`

	var got SessionResponse
	if err := json.Unmarshal([]byte(input), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.SessionID != "s1" {
		t.Errorf("SessionID = %q, want %q", got.SessionID, "s1")
	}
	if got.AgentName != "a" {
		t.Errorf("AgentName = %q, want %q", got.AgentName, "a")
	}
	if got.Status != "running" {
		t.Errorf("Status = %q, want %q", got.Status, "running")
	}
	// Optional fields should be zero-value
	if got.TaskID != "" {
		t.Errorf("TaskID = %q, want empty", got.TaskID)
	}
	if got.Model != "" {
		t.Errorf("Model = %q, want empty", got.Model)
	}
	if got.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", got.ExitCode)
	}
}

func TestSessionResponse_ExitCodeZeroPreserved(t *testing.T) {
	resp := SessionResponse{ExitCode: 0}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	val, ok := raw["exit_code"]
	if !ok {
		t.Fatal("exit_code omitted from JSON output")
	}
	if string(val) != "0" {
		t.Errorf("exit_code = %s, want 0", val)
	}
}

func TestSessionResponse_AttemptNumZeroPreserved(t *testing.T) {
	resp := SessionResponse{AttemptNum: 0}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	val, ok := raw["attempt_num"]
	if !ok {
		t.Fatal("attempt_num omitted from JSON output")
	}
	if string(val) != "0" {
		t.Errorf("attempt_num = %s, want 0", val)
	}
}

func TestSessionResponse_StatusAsString(t *testing.T) {
	resp := SessionResponse{Status: "completed"}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got SessionResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Status != "completed" {
		t.Errorf("Status = %q, want %q", got.Status, "completed")
	}
}

func TestSessionResponse_OptionalFieldsOmitted(t *testing.T) {
	resp := SessionResponse{
		SessionID: "s1",
		AgentName: "a",
		Status:    "running",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	for _, field := range []string{
		"ended_at", "duration_s", "model", "phase",
		"epic_id", "error_class", "last_error", "files_touched",
	} {
		if _, ok := raw[field]; ok {
			t.Errorf("%s should be omitted when zero/nil, but present: %s", field, raw[field])
		}
	}
}

func TestSessionResponse_ComputedFieldsFalse(t *testing.T) {
	resp := SessionResponse{
		IsActive:      false,
		HasTranscript: false,
		HasDiff:       false,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	for _, field := range []string{"is_active", "has_transcript", "has_diff"} {
		val, ok := raw[field]
		if !ok {
			t.Errorf("%s should be present even when false, but omitted", field)
			continue
		}
		if string(val) != "false" {
			t.Errorf("%s = %s, want false", field, val)
		}
	}
}

func TestSessionResponse_ComputedFieldsTrue(t *testing.T) {
	resp := SessionResponse{
		IsActive:      true,
		HasTranscript: true,
		HasDiff:       true,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	for _, field := range []string{"is_active", "has_transcript", "has_diff"} {
		val, ok := raw[field]
		if !ok {
			t.Errorf("%s should be present, but omitted", field)
			continue
		}
		if string(val) != "true" {
			t.Errorf("%s = %s, want true", field, val)
		}
	}
}

func TestSessionResponse_FilesTouchedOmittedWhenNil(t *testing.T) {
	resp := SessionResponse{FilesTouched: nil}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	if _, ok := raw["files_touched"]; ok {
		t.Error("files_touched should be omitted when nil")
	}
}

func TestSessionResponse_FilesTouchedPresent(t *testing.T) {
	resp := SessionResponse{FilesTouched: []string{"a.go"}}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	val, ok := raw["files_touched"]
	if !ok {
		t.Fatal("files_touched should be present when populated")
	}
	if string(val) != `["a.go"]` {
		t.Errorf("files_touched = %s, want %s", val, `["a.go"]`)
	}
}

func TestSessionResponse_TokenFieldsZeroPreserved(t *testing.T) {
	resp := SessionResponse{
		InputTokens:      ptr(int64(0)),
		OutputTokens:     ptr(int64(0)),
		CacheReadTokens:  ptr(int64(0)),
		CacheWriteTokens: ptr(int64(0)),
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	for _, field := range []string{
		"input_tokens", "output_tokens",
		"cache_read_tokens", "cache_write_tokens",
	} {
		val, ok := raw[field]
		if !ok {
			t.Errorf("%s should be present even when zero, but omitted", field)
			continue
		}
		if string(val) != "0" {
			t.Errorf("%s = %s, want 0", field, val)
		}
	}
}

func TestSessionResponse_UnavailableUsageIsNull(t *testing.T) {
	data, err := json.Marshal(SessionResponse{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}
	for _, field := range []string{"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "estimated_cost_usd"} {
		if string(raw[field]) != "null" {
			t.Errorf("%s = %s, want null", field, raw[field])
		}
	}
}

func TestSessionResponseFromListItem_ReportedZeroUsageIsNumeric(t *testing.T) {
	response := SessionResponseFromListItem(service.SessionListItem{
		SessionRecord: sessions.SessionRecord{InputTokens: 1},
		Evidence:      service.SessionEvidence{Status: "ok", UsageStatus: "reported", Conflicts: []service.SessionEvidenceConflict{}},
	})
	if response.InputTokens == nil || *response.InputTokens != 1 {
		t.Fatalf("input_tokens = %v, want 1", response.InputTokens)
	}
	if response.OutputTokens == nil || *response.OutputTokens != 0 {
		t.Fatalf("output_tokens = %v, want reported zero", response.OutputTokens)
	}
}

func TestTranscriptEntry_JSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)

	entry := TranscriptEntry{
		Seq:       1,
		Timestamp: now,
		Role:      "assistant",
		Type:      "tool_use",
		Content:   "Reading file...",
		ToolName:  "Read",
		ToolInput: `{"file":"a.go"}`,
		Raw:       `{"original":"event"}`,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got TranscriptEntry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Seq != entry.Seq {
		t.Errorf("Seq = %d, want %d", got.Seq, entry.Seq)
	}
	if !got.Timestamp.Equal(entry.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, entry.Timestamp)
	}
	if got.Role != entry.Role {
		t.Errorf("Role = %q, want %q", got.Role, entry.Role)
	}
	if got.Type != entry.Type {
		t.Errorf("Type = %q, want %q", got.Type, entry.Type)
	}
	if got.Content != entry.Content {
		t.Errorf("Content = %q, want %q", got.Content, entry.Content)
	}
	if got.ToolName != entry.ToolName {
		t.Errorf("ToolName = %q, want %q", got.ToolName, entry.ToolName)
	}
	if got.ToolInput != entry.ToolInput {
		t.Errorf("ToolInput = %q, want %q", got.ToolInput, entry.ToolInput)
	}
	if got.Raw != entry.Raw {
		t.Errorf("Raw = %q, want %q", got.Raw, entry.Raw)
	}
}

func TestTranscriptEntry_OptionalFieldsOmitted(t *testing.T) {
	entry := TranscriptEntry{
		Seq:       1,
		Timestamp: time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC),
		Role:      "user",
		Type:      "text",
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}

	for _, field := range []string{"content", "tool_name", "tool_input", "raw"} {
		if _, ok := raw[field]; ok {
			t.Errorf("%s should be omitted when empty, but present: %s", field, raw[field])
		}
	}

	// Required fields present
	for _, field := range []string{"seq", "ts", "role", "type"} {
		if _, ok := raw[field]; !ok {
			t.Errorf("%s should be present", field)
		}
	}
}
