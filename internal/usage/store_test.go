package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewStore_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "dir")
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if s == nil {
		t.Fatal("NewStore returned nil")
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("NewStore did not create directory")
	}
}

func TestStore_Append_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	now := time.Now()
	record := SessionUsage{
		AgentName:   "test-agent",
		Backend:     "claude",
		InputTokens: 100,
		StartedAt:   now,
		EndedAt:     now.Add(time.Minute),
	}
	if err := s.Append(record); err != nil {
		t.Fatalf("Append: %v", err)
	}

	path := filepath.Join(dir, "usage.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("JSONL line should end with newline")
	}

	var parsed SessionUsage
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed.AgentName != "test-agent" {
		t.Errorf("AgentName = %q, want %q", parsed.AgentName, "test-agent")
	}
	if parsed.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", parsed.InputTokens)
	}
}

func TestStore_Append_MultipleRecords(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	now := time.Now()
	for i := range 3 {
		record := SessionUsage{
			AgentName:   "agent",
			InputTokens: int64((i + 1) * 100),
			StartedAt:   now,
			EndedAt:     now.Add(time.Minute),
		}
		if err := s.Append(record); err != nil {
			t.Fatalf("Append(%d): %v", i, err)
		}
	}

	records, err := s.Read(Filter{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("Read returned %d records, want 3", len(records))
	}
	if records[0].InputTokens != 100 {
		t.Errorf("records[0].InputTokens = %d, want 100", records[0].InputTokens)
	}
	if records[2].InputTokens != 300 {
		t.Errorf("records[2].InputTokens = %d, want 300", records[2].InputTokens)
	}
}

func TestStore_Read_NoFile(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	records, err := s.Read(Filter{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("Read on missing file returned %d records, want 0", len(records))
	}
}

func TestStore_Read_FilterByAgent(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	now := time.Now()
	_ = s.Append(SessionUsage{AgentName: "falcon", InputTokens: 100, StartedAt: now, EndedAt: now})
	_ = s.Append(SessionUsage{AgentName: "spark", InputTokens: 200, StartedAt: now, EndedAt: now})
	_ = s.Append(SessionUsage{AgentName: "falcon", InputTokens: 300, StartedAt: now, EndedAt: now})

	records, err := s.Read(Filter{AgentName: "falcon"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("Read(falcon) returned %d records, want 2", len(records))
	}
}

func TestStore_Read_FilterByBackend(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	now := time.Now()
	_ = s.Append(SessionUsage{Backend: "claude", InputTokens: 100, StartedAt: now, EndedAt: now})
	_ = s.Append(SessionUsage{Backend: "codex", InputTokens: 200, StartedAt: now, EndedAt: now})

	records, err := s.Read(Filter{Backend: "codex"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("Read(codex) returned %d records, want 1", len(records))
	}
	if records[0].InputTokens != 200 {
		t.Errorf("InputTokens = %d, want 200", records[0].InputTokens)
	}
}

func TestStore_Read_FilterByDateRange(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	t1 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 3, 3, 10, 0, 0, 0, time.UTC)

	_ = s.Append(SessionUsage{AgentName: "a", StartedAt: t1, EndedAt: t1.Add(time.Hour)})
	_ = s.Append(SessionUsage{AgentName: "b", StartedAt: t2, EndedAt: t2.Add(time.Hour)})
	_ = s.Append(SessionUsage{AgentName: "c", StartedAt: t3, EndedAt: t3.Add(time.Hour)})

	// Since t2 means only records starting at or after t2
	records, err := s.Read(Filter{Since: t2})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("Read(since t2) returned %d records, want 2", len(records))
	}
}

func TestStore_Read_FilterByTaskAndEpic(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	now := time.Now()
	_ = s.Append(SessionUsage{TaskID: "t1", EpicID: "e1", StartedAt: now, EndedAt: now})
	_ = s.Append(SessionUsage{TaskID: "t2", EpicID: "e1", StartedAt: now, EndedAt: now})
	_ = s.Append(SessionUsage{TaskID: "t1", EpicID: "e2", StartedAt: now, EndedAt: now})

	records, err := s.Read(Filter{TaskID: "t1"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("Read(t1) returned %d records, want 2", len(records))
	}

	records, err = s.Read(Filter{EpicID: "e1"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("Read(e1) returned %d records, want 2", len(records))
	}
}

func TestStore_Read_CorruptLineSkipped(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	now := time.Now()
	_ = s.Append(SessionUsage{AgentName: "good", StartedAt: now, EndedAt: now})

	// Write a corrupt line directly
	path := filepath.Join(dir, "usage.jsonl")
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("this is not json\n")
	f.Close()

	_ = s.Append(SessionUsage{AgentName: "also-good", StartedAt: now, EndedAt: now})

	records, err := s.Read(Filter{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("Read returned %d records, want 2 (corrupt line should be skipped)", len(records))
	}
}

func TestStore_ConcurrentAppends(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	n := 50
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			now := time.Now()
			record := SessionUsage{
				AgentName:   "concurrent",
				InputTokens: int64(idx),
				StartedAt:   now,
				EndedAt:     now,
			}
			if err := s.Append(record); err != nil {
				t.Errorf("concurrent Append(%d): %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	records, err := s.Read(Filter{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(records) != n {
		t.Errorf("Read returned %d records, want %d", len(records), n)
	}
}
