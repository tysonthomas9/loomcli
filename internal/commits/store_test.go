package commits

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCommitsPath(t *testing.T) {
	got := CommitsPath("/some/beads/dir")
	want := filepath.Join("/some/beads/dir", "commits.jsonl")
	if got != want {
		t.Errorf("CommitsPath() = %q, want %q", got, want)
	}
}

func TestLoadAll_NonexistentFile(t *testing.T) {
	dir := t.TempDir()
	records, err := LoadAll(dir)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if records != nil {
		t.Errorf("expected nil slice, got %v", records)
	}
}

func TestLoadAll_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, commitsFile), []byte(""), 0644)

	records, err := LoadAll(dir)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if records != nil {
		t.Errorf("expected nil slice for empty file, got %v", records)
	}
}

func TestLoadAll_SingleRecord(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	rec := Record{
		TaskID:    "TASK-1",
		SHA:       "abc123",
		Subject:   "fix bug",
		Author:    "alice",
		Timestamp: ts,
		Worktree:  "ember",
	}
	data, _ := json.Marshal(rec)
	data = append(data, '\n')
	os.WriteFile(filepath.Join(dir, commitsFile), data, 0644)

	records, err := LoadAll(dir)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].TaskID != "TASK-1" {
		t.Errorf("expected task ID TASK-1, got %s", records[0].TaskID)
	}
	if records[0].SHA != "abc123" {
		t.Errorf("expected SHA abc123, got %s", records[0].SHA)
	}
	if records[0].Subject != "fix bug" {
		t.Errorf("expected subject 'fix bug', got %s", records[0].Subject)
	}
	if records[0].Author != "alice" {
		t.Errorf("expected author alice, got %s", records[0].Author)
	}
	if !records[0].Timestamp.Equal(ts) {
		t.Errorf("expected timestamp %v, got %v", ts, records[0].Timestamp)
	}
	if records[0].Worktree != "ember" {
		t.Errorf("expected worktree ember, got %s", records[0].Worktree)
	}
}

func TestLoadAll_MultipleRecords(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	records := []Record{
		{TaskID: "TASK-1", SHA: "aaa111", Subject: "first", Author: "alice", Timestamp: ts},
		{TaskID: "TASK-2", SHA: "bbb222", Subject: "second", Author: "bob", Timestamp: ts.Add(time.Hour)},
		{TaskID: "TASK-1", SHA: "ccc333", Subject: "third", Author: "alice", Timestamp: ts.Add(2 * time.Hour)},
	}

	var content []byte
	for _, rec := range records {
		line, _ := json.Marshal(rec)
		content = append(content, line...)
		content = append(content, '\n')
	}
	os.WriteFile(filepath.Join(dir, commitsFile), content, 0644)

	loaded, err := LoadAll(dir)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 records, got %d", len(loaded))
	}
	if loaded[0].SHA != "aaa111" {
		t.Errorf("expected first SHA aaa111, got %s", loaded[0].SHA)
	}
	if loaded[1].SHA != "bbb222" {
		t.Errorf("expected second SHA bbb222, got %s", loaded[1].SHA)
	}
	if loaded[2].SHA != "ccc333" {
		t.Errorf("expected third SHA ccc333, got %s", loaded[2].SHA)
	}
}

func TestLoadAll_SkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	good, _ := json.Marshal(Record{TaskID: "TASK-1", SHA: "abc123", Subject: "ok", Author: "alice", Timestamp: ts})
	content := string(good) + "\n" + "this is not json\n" + string(good) + "\n"
	os.WriteFile(filepath.Join(dir, commitsFile), []byte(content), 0644)

	loaded, err := LoadAll(dir)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 records (skipping malformed), got %d", len(loaded))
	}
}

func TestLoadAll_SkipsBlankLines(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	good, _ := json.Marshal(Record{TaskID: "TASK-1", SHA: "abc123", Subject: "ok", Author: "alice", Timestamp: ts})
	content := "\n" + string(good) + "\n\n"
	os.WriteFile(filepath.Join(dir, commitsFile), []byte(content), 0644)

	loaded, err := LoadAll(dir)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 record, got %d", len(loaded))
	}
}

func TestLoadForTask_FiltersByTaskID(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	records := []Record{
		{TaskID: "TASK-1", SHA: "aaa111", Subject: "first", Author: "alice", Timestamp: ts},
		{TaskID: "TASK-2", SHA: "bbb222", Subject: "second", Author: "bob", Timestamp: ts.Add(time.Hour)},
		{TaskID: "TASK-1", SHA: "ccc333", Subject: "third", Author: "alice", Timestamp: ts.Add(2 * time.Hour)},
		{TaskID: "TASK-3", SHA: "ddd444", Subject: "fourth", Author: "carol", Timestamp: ts.Add(3 * time.Hour)},
	}

	var content []byte
	for _, rec := range records {
		line, _ := json.Marshal(rec)
		content = append(content, line...)
		content = append(content, '\n')
	}
	os.WriteFile(filepath.Join(dir, commitsFile), content, 0644)

	filtered, err := LoadForTask(dir, "TASK-1", 0)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("expected 2 records for TASK-1, got %d", len(filtered))
	}
	for _, rec := range filtered {
		if rec.TaskID != "TASK-1" {
			t.Errorf("expected all records to have task ID TASK-1, got %s", rec.TaskID)
		}
	}
}

func TestLoadForTask_NewestFirst(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	// Append in chronological order: oldest first
	records := []Record{
		{TaskID: "TASK-1", SHA: "aaa111", Subject: "first", Author: "alice", Timestamp: ts},
		{TaskID: "TASK-1", SHA: "bbb222", Subject: "second", Author: "alice", Timestamp: ts.Add(time.Hour)},
		{TaskID: "TASK-1", SHA: "ccc333", Subject: "third", Author: "alice", Timestamp: ts.Add(2 * time.Hour)},
	}

	var content []byte
	for _, rec := range records {
		line, _ := json.Marshal(rec)
		content = append(content, line...)
		content = append(content, '\n')
	}
	os.WriteFile(filepath.Join(dir, commitsFile), content, 0644)

	filtered, err := LoadForTask(dir, "TASK-1", 0)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(filtered) != 3 {
		t.Fatalf("expected 3 records, got %d", len(filtered))
	}
	// Newest first means the last appended record comes first
	if filtered[0].SHA != "ccc333" {
		t.Errorf("expected newest first (ccc333), got %s", filtered[0].SHA)
	}
	if filtered[1].SHA != "bbb222" {
		t.Errorf("expected second newest (bbb222), got %s", filtered[1].SHA)
	}
	if filtered[2].SHA != "aaa111" {
		t.Errorf("expected oldest last (aaa111), got %s", filtered[2].SHA)
	}
}

func TestLoadForTask_WithLimit(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	records := []Record{
		{TaskID: "TASK-1", SHA: "aaa111", Subject: "first", Author: "alice", Timestamp: ts},
		{TaskID: "TASK-1", SHA: "bbb222", Subject: "second", Author: "alice", Timestamp: ts.Add(time.Hour)},
		{TaskID: "TASK-1", SHA: "ccc333", Subject: "third", Author: "alice", Timestamp: ts.Add(2 * time.Hour)},
	}

	var content []byte
	for _, rec := range records {
		line, _ := json.Marshal(rec)
		content = append(content, line...)
		content = append(content, '\n')
	}
	os.WriteFile(filepath.Join(dir, commitsFile), content, 0644)

	filtered, err := LoadForTask(dir, "TASK-1", 2)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("expected 2 records with limit=2, got %d", len(filtered))
	}
	// Should return the 2 newest
	if filtered[0].SHA != "ccc333" {
		t.Errorf("expected newest (ccc333), got %s", filtered[0].SHA)
	}
	if filtered[1].SHA != "bbb222" {
		t.Errorf("expected second newest (bbb222), got %s", filtered[1].SHA)
	}
}

func TestLoadForTask_NonexistentFile(t *testing.T) {
	dir := t.TempDir()
	filtered, err := LoadForTask(dir, "TASK-1", 0)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if filtered != nil {
		t.Errorf("expected nil slice, got %v", filtered)
	}
}

func TestLoadForTask_NoMatchingTaskID(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	rec := Record{TaskID: "TASK-1", SHA: "aaa111", Subject: "first", Author: "alice", Timestamp: ts}
	data, _ := json.Marshal(rec)
	data = append(data, '\n')
	os.WriteFile(filepath.Join(dir, commitsFile), data, 0644)

	filtered, err := LoadForTask(dir, "TASK-999", 0)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(filtered) != 0 {
		t.Errorf("expected 0 records for non-matching task, got %d", len(filtered))
	}
}

func TestAppend_CreatesFileIfNotExists(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	rec := Record{
		TaskID:    "TASK-1",
		SHA:       "abc123",
		Subject:   "new commit",
		Author:    "alice",
		Timestamp: ts,
		Worktree:  "ember",
	}

	err := Append(dir, rec)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify file was created and contains the record
	path := CommitsPath(dir)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected file to exist, got: %v", err)
	}

	var loaded Record
	if err := json.Unmarshal(data[:len(data)-1], &loaded); err != nil {
		t.Fatalf("failed to unmarshal written record: %v", err)
	}
	if loaded.TaskID != "TASK-1" {
		t.Errorf("expected task ID TASK-1, got %s", loaded.TaskID)
	}
	if loaded.SHA != "abc123" {
		t.Errorf("expected SHA abc123, got %s", loaded.SHA)
	}
	if loaded.Worktree != "ember" {
		t.Errorf("expected worktree ember, got %s", loaded.Worktree)
	}
}

func TestAppend_AppendsToExistingFile(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	rec1 := Record{TaskID: "TASK-1", SHA: "aaa111", Subject: "first", Author: "alice", Timestamp: ts}
	rec2 := Record{TaskID: "TASK-2", SHA: "bbb222", Subject: "second", Author: "bob", Timestamp: ts.Add(time.Hour)}

	if err := Append(dir, rec1); err != nil {
		t.Fatalf("first append failed: %v", err)
	}
	if err := Append(dir, rec2); err != nil {
		t.Fatalf("second append failed: %v", err)
	}

	records, err := LoadAll(dir)
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].SHA != "aaa111" {
		t.Errorf("expected first record SHA aaa111, got %s", records[0].SHA)
	}
	if records[1].SHA != "bbb222" {
		t.Errorf("expected second record SHA bbb222, got %s", records[1].SHA)
	}
}

func TestAppend_RecordWithoutWorktree(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	rec := Record{
		TaskID:    "TASK-1",
		SHA:       "abc123",
		Subject:   "commit",
		Author:    "alice",
		Timestamp: ts,
		// Worktree intentionally omitted
	}

	if err := Append(dir, rec); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	records, err := LoadAll(dir)
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Worktree != "" {
		t.Errorf("expected empty worktree, got %s", records[0].Worktree)
	}
}

func TestAppend_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	original := Record{
		TaskID:    "TASK-42",
		SHA:       "deadbeef",
		Subject:   "fix critical bug in parser",
		Author:    "carol",
		Timestamp: ts,
		Worktree:  "nova",
	}

	if err := Append(dir, original); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	loaded, err := LoadForTask(dir, "TASK-42", 0)
	if err != nil {
		t.Fatalf("LoadForTask failed: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 record, got %d", len(loaded))
	}

	got := loaded[0]
	if got.TaskID != original.TaskID {
		t.Errorf("TaskID: got %s, want %s", got.TaskID, original.TaskID)
	}
	if got.SHA != original.SHA {
		t.Errorf("SHA: got %s, want %s", got.SHA, original.SHA)
	}
	if got.Subject != original.Subject {
		t.Errorf("Subject: got %s, want %s", got.Subject, original.Subject)
	}
	if got.Author != original.Author {
		t.Errorf("Author: got %s, want %s", got.Author, original.Author)
	}
	if !got.Timestamp.Equal(original.Timestamp) {
		t.Errorf("Timestamp: got %v, want %v", got.Timestamp, original.Timestamp)
	}
	if got.Worktree != original.Worktree {
		t.Errorf("Worktree: got %s, want %s", got.Worktree, original.Worktree)
	}
}
