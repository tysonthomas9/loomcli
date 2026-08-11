package terminal

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRecordingStoreRangeReadResizeAndTornTailRecovery(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Workspace: "ws-test", Name: "session-1"}
	store := NewRecordingStore(root, nil)
	recorder, err := store.StartRecording(key, 8, 2)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	recorder.Append([]byte("one\r\ntwo\r\nthree"))
	recorder.Append([]byte("\x1b]0;recording title\x07"))
	recorder.Resize(120, 1)
	recorder.Append([]byte("\r\nfour"))
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close recorder: %v", err)
	}

	history, err := store.History(context.Background(), key, 1, 2)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if !history.Closed || history.Cols != 120 || history.TotalLines != 4 {
		t.Fatalf("history summary = %#v", history)
	}
	got := []string{lineText(history.Lines[0]), lineText(history.Lines[1])}
	if !reflect.DeepEqual(got, []string{"two", "three"}) {
		t.Fatalf("range text = %#v, want [two three]", got)
	}
	if history.Lines[0].Cols != 8 || history.Lines[1].Cols != 120 {
		t.Fatalf("mixed-width line cols = [%d %d], want [8 120]", history.Lines[0].Cols, history.Lines[1].Cols)
	}
	meta, total, _, err := store.Meta(context.Background(), key)
	if err != nil {
		t.Fatalf("Meta: %v", err)
	}
	if total != 4 || len(meta.Resizes) != 1 || meta.Resizes[0].Cols != 120 || meta.Cols != 120 {
		t.Fatalf("resize metadata = %#v total=%d", meta, total)
	}
	if meta.UnhandledSequences.Count != 1 || meta.UnhandledSequences.Prefixes["OSC 0"] != 1 {
		t.Fatalf("unhandled sequence metadata = %#v", meta.UnhandledSequences)
	}

	dir, err := store.sessionDir(key)
	if err != nil {
		t.Fatalf("sessionDir: %v", err)
	}
	rawPath := filepath.Join(dir, "raw.seg")
	beforeRaw := fileSize(t, rawPath)
	raw, err := os.OpenFile(rawPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open raw tail: %v", err)
	}
	var tornHeader [recordingFrameHeaderSize]byte
	binary.BigEndian.PutUint32(tornHeader[:4], 99)
	if _, err := raw.Write(tornHeader[:6]); err != nil {
		t.Fatalf("append torn raw header: %v", err)
	}
	_ = raw.Close()
	linesPath := filepath.Join(dir, "lines.jsonl")
	beforeLines := fileSize(t, linesPath)
	lines, err := os.OpenFile(linesPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open line tail: %v", err)
	}
	if _, err := lines.WriteString(`{"i":4`); err != nil {
		t.Fatalf("append torn line: %v", err)
	}
	_ = lines.Close()
	// A crash can leave torn file tails only while the last durable metadata
	// snapshot is still open. Force that real recovery path rather than taking
	// the closed-generation fast path used by normal immutable reads.
	markRecordingOpenForRecoveryTest(t, dir)

	secondStore := NewRecordingStore(root, nil)
	recovered, err := secondStore.History(context.Background(), key, 0, 10)
	if err != nil {
		t.Fatalf("History after recovery: %v", err)
	}
	if recovered.TotalLines != 4 {
		t.Fatalf("recovered total = %d, want 4", recovered.TotalLines)
	}
	recoveredMeta, _, _, err := secondStore.Meta(context.Background(), key)
	if err != nil {
		t.Fatalf("Meta after recovery: %v", err)
	}
	if recoveredMeta.UnhandledSequences.Count != 1 || recoveredMeta.UnhandledSequences.Prefixes["OSC 0"] != 1 {
		t.Fatalf("recovered unhandled sequence metadata = %#v", recoveredMeta.UnhandledSequences)
	}
	if got := fileSize(t, rawPath); got != beforeRaw {
		t.Fatalf("raw size after recovery = %d, want %d", got, beforeRaw)
	}
	if got := fileSize(t, linesPath); got != beforeLines {
		t.Fatalf("line log size after recovery = %d, want %d", got, beforeLines)
	}
	idxInfo, err := os.Stat(filepath.Join(dir, "lines.idx"))
	if err != nil {
		t.Fatalf("stat rebuilt index: %v", err)
	}
	if idxInfo.Size() != int64(recovered.TotalLines*8) {
		t.Fatalf("index size = %d, want %d", idxInfo.Size(), recovered.TotalLines*8)
	}
}

func TestRecordingRawFormatHeaderAndInitialGeometry(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Workspace: "ws-test", Name: "raw-format"}
	store := NewRecordingStore(root, nil)
	recorder, err := store.StartRecording(key, 91, 37)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	recorder.Append([]byte("hello"))
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(store.mustSessionDir(key), "raw.seg"))
	if err != nil {
		t.Fatalf("read raw: %v", err)
	}
	if len(raw) < recordingFileHeaderSize+recordingFrameHeaderSize+4 || !bytes.Equal(raw[:recordingFileHeaderSize], recordingFileHeader[:]) {
		t.Fatalf("raw format header = %x, want %x", raw[:minInt(len(raw), recordingFileHeaderSize)], recordingFileHeader)
	}
	frame := raw[recordingFileHeaderSize:]
	if recordingEventKind(frame[0]) != recordingResizeEvent || binary.BigEndian.Uint32(frame[1:5]) != 4 {
		t.Fatalf("first raw event header = %x, want geometry length 4", frame[:recordingFrameHeaderSize])
	}
	payload := frame[recordingFrameHeaderSize : recordingFrameHeaderSize+4]
	if binary.BigEndian.Uint16(payload[:2]) != 91 || binary.BigEndian.Uint16(payload[2:]) != 37 {
		t.Fatalf("initial geometry payload = %x, want 91x37", payload)
	}
}

func TestRecordingRawFormatRefusesUnversionedLayout(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Workspace: "ws-test", Name: "old-raw"}
	store := NewRecordingStore(root, nil)
	generation := strings.Repeat("0", recordingGenerationBytes*2)
	dir := installCurrentGenerationForTest(t, store, key, generation)
	if err := os.WriteFile(filepath.Join(dir, "raw.seg"), make([]byte, recordingFrameHeaderSize), 0o600); err != nil {
		t.Fatalf("write unversioned raw: %v", err)
	}
	if err := writeJSONAtomic(filepath.Join(dir, "meta.json"), RecordingMeta{
		FormatVersion: recordingFormatVersion,
		Generation:    generation,
		SessionKey:    key.Name,
		StartedAt:     unixMilliNow(),
		Cols:          80,
		Rows:          24,
	}); err != nil {
		t.Fatalf("write recording metadata: %v", err)
	}
	if _, err := store.StartRecording(key, 80, 24); err == nil {
		t.Fatal("StartRecording accepted an unversioned raw layout")
	}
}

func TestSessionRecorderAppendDropsInsteadOfBlocking(t *testing.T) {
	recorder := &SessionRecorder{events: make(chan recordingEvent, 1)}
	recorder.Append([]byte("first"))
	recorder.Append([]byte("second"))
	if got := recorder.droppedChunks.Load(); got != 1 {
		t.Fatalf("dropped chunks = %d, want 1", got)
	}
	if got := len(recorder.events); got != 1 {
		t.Fatalf("queued chunks = %d, want 1", got)
	}
}

func TestRecordingRecoveryWithoutCheckpointPreservesCommittedRowsAcrossGeometryChanges(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Workspace: "ws-test", Name: "missing-checkpoint"}
	store := NewRecordingStore(root, nil)
	recorder, err := store.StartRecording(key, 8, 3)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	recorder.Append([]byte("A\r\nB\r\nC"))
	recorder.Resize(8, 1)
	recorder.Resize(12, 3)
	recorder.Append([]byte("\x1b[H" + "X"))
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	dir := store.mustSessionDir(key)
	want, err := os.ReadFile(filepath.Join(dir, "lines.jsonl"))
	if err != nil {
		t.Fatalf("read uninterrupted lines: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "indexer.json")); err != nil {
		t.Fatalf("remove indexer checkpoint: %v", err)
	}
	markRecordingOpenForRecoveryTest(t, dir)

	recoveredStore := NewRecordingStore(root, nil)
	if _, err := recoveredStore.History(context.Background(), key, 0, 100); err != nil {
		t.Fatalf("recover History: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "lines.jsonl"))
	if err != nil {
		t.Fatalf("read recovered lines: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("recovered committed rows changed without checkpoint\nwant=%s\n got=%s", want, got)
	}
}

func TestRecordingRecoveryRejectsStaleCheckpointAcrossGeometryChanges(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Workspace: "ws-test", Name: "stale-checkpoint"}
	store := NewRecordingStore(root, nil)
	recorder, err := store.StartRecording(key, 8, 3)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	recorder.Append([]byte("A\r\nB\r\nC\r\nD"))
	if got := recorder.FirstScreenLine(); got != 1 {
		t.Fatalf("committed lines before resize = %d, want 1", got)
	}
	dir := store.mustSessionDir(key)
	staleCheckpoint, err := os.ReadFile(filepath.Join(dir, "indexer.json"))
	if err != nil {
		t.Fatalf("read stale checkpoint: %v", err)
	}

	recorder.Resize(12, 4)
	recorder.Append([]byte("\r\nE\x1b[H" + "X"))
	_ = recorder.FirstScreenLine()

	crashRoot := t.TempDir()
	crashStore := NewRecordingStore(crashRoot, nil)
	meta, err := readRecordingMeta(dir)
	if err != nil {
		t.Fatalf("read source generation metadata: %v", err)
	}
	crashDir := installCurrentGenerationForTest(t, crashStore, key, meta.Generation)
	for _, name := range []string{"raw.seg", "lines.jsonl", "lines.idx", "meta.json"} {
		data, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			t.Fatalf("read %s for crash copy: %v", name, readErr)
		}
		if writeErr := os.WriteFile(filepath.Join(crashDir, name), data, 0o600); writeErr != nil {
			t.Fatalf("write %s crash copy: %v", name, writeErr)
		}
	}
	if err := os.WriteFile(filepath.Join(crashDir, "indexer.json"), staleCheckpoint, 0o600); err != nil {
		t.Fatalf("write stale checkpoint: %v", err)
	}

	if err := recorder.Close(); err != nil {
		t.Fatalf("Close uninterrupted recorder: %v", err)
	}
	want, err := os.ReadFile(filepath.Join(dir, "lines.jsonl"))
	if err != nil {
		t.Fatalf("read uninterrupted lines: %v", err)
	}
	if _, err := crashStore.History(context.Background(), key, 0, 100); err != nil {
		t.Fatalf("recover stale-checkpoint History: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(crashDir, "lines.jsonl"))
	if err != nil {
		t.Fatalf("read stale-checkpoint recovery: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("recovered committed rows changed with stale checkpoint\nwant=%s\n got=%s", want, got)
	}
}

func TestRecordingRecoveryRejectsCheckpointWithWrongRows(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Workspace: "ws-test", Name: "wrong-checkpoint-rows"}
	store := NewRecordingStore(root, nil)
	recorder, err := store.StartRecording(key, 8, 3)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	recorder.Append([]byte("A\r\nB\r\nC"))
	_ = recorder.FirstScreenLine()
	dir := store.mustSessionDir(key)

	crashRoot := t.TempDir()
	crashStore := NewRecordingStore(crashRoot, nil)
	meta, err := readRecordingMeta(dir)
	if err != nil {
		t.Fatalf("read source generation metadata: %v", err)
	}
	crashDir := installCurrentGenerationForTest(t, crashStore, key, meta.Generation)
	for _, name := range []string{"raw.seg", "lines.jsonl", "lines.idx", "meta.json"} {
		data, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		if writeErr := os.WriteFile(filepath.Join(crashDir, name), data, 0o600); writeErr != nil {
			t.Fatalf("write %s: %v", name, writeErr)
		}
	}
	var checkpoint indexerCheckpoint
	if err := readJSONFile(filepath.Join(dir, "indexer.json"), &checkpoint); err != nil {
		t.Fatalf("read checkpoint: %v", err)
	}
	checkpoint.EmulatorState.Rows = 1
	if err := writeJSONAtomic(filepath.Join(crashDir, "indexer.json"), checkpoint); err != nil {
		t.Fatalf("write wrong-row checkpoint: %v", err)
	}

	if err := recorder.Close(); err != nil {
		t.Fatalf("Close uninterrupted recorder: %v", err)
	}
	want, err := os.ReadFile(filepath.Join(dir, "lines.jsonl"))
	if err != nil {
		t.Fatalf("read uninterrupted lines: %v", err)
	}
	if _, err := crashStore.History(context.Background(), key, 0, 100); err != nil {
		t.Fatalf("recover wrong-row History: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(crashDir, "lines.jsonl"))
	if err != nil {
		t.Fatalf("read recovered lines: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("wrong-row checkpoint changed recovered rows\nwant=%s\n got=%s", want, got)
	}
}

func TestRecordingStoreLifecycleHooks(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Workspace: "ws-test", Name: "session-hooks"}
	store := NewRecordingStore(root, nil)
	var started, completed int
	var startedPath, completedPath, generation string
	store.SetLifecycleHooks(
		func(gotKey SessionKey, dir string, meta RecordingMeta) {
			started++
			startedPath = dir
			generation = meta.Generation
			if gotKey != key || dir == "" || meta.StartedAt == 0 {
				t.Fatalf("start hook = key=%#v dir=%q meta=%#v", gotKey, dir, meta)
			}
		},
		func(gotKey SessionKey, dir string, meta RecordingMeta) {
			completed++
			completedPath = dir
			if gotKey != key || !meta.Closed {
				t.Fatalf("complete hook = key=%#v meta=%#v", gotKey, meta)
			}
		},
	)
	recorder, err := store.StartRecording(key, 80, 24)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	recorder.Append([]byte("recorded\r\n"))
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if started != 1 || completed != 1 {
		t.Fatalf("hook calls = started %d completed %d, want 1 each", started, completed)
	}
	if !validRecordingGeneration(generation) {
		t.Fatalf("started generation = %q, want a 32-character lowercase hex identity", generation)
	}
	wantPath := filepath.Join(root, key.Workspace, key.Name, "generations", generation)
	if startedPath != wantPath || completedPath != wantPath {
		t.Fatalf("lifecycle paths = started %q completed %q, want %q", startedPath, completedPath, wantPath)
	}
}

func TestRecordingStoreReusedSessionStartsDistinctReadableGeneration(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Workspace: "ws-test", Name: "reused-session"}
	store := NewRecordingStore(root, nil)
	var recordingDirs []string
	store.SetLifecycleHooks(
		func(_ SessionKey, dir string, _ RecordingMeta) {
			recordingDirs = append(recordingDirs, dir)
		},
		nil,
	)

	first, err := store.StartRecording(key, 80, 2)
	if err != nil {
		t.Fatalf("start first recording: %v", err)
	}
	first.Append([]byte("first-generation-only\r\n"))
	if err := first.Close(); err != nil {
		t.Fatalf("close first recording: %v", err)
	}
	if len(recordingDirs) != 1 {
		t.Fatalf("recording start paths after first PTY = %#v, want one", recordingDirs)
	}
	firstDir := recordingDirs[0]
	firstMeta, err := readRecordingMeta(firstDir)
	if err != nil {
		t.Fatalf("read first recording metadata: %v", err)
	}

	// StartedAt has millisecond precision. Ensure this assertion specifically
	// detects preservation of the first recording's timestamp, not a same-tick
	// timestamp allocated independently by a correct implementation.
	time.Sleep(2 * time.Millisecond)
	second, err := store.StartRecording(key, 80, 2)
	if err != nil {
		t.Fatalf("start second recording: %v", err)
	}
	second.Append([]byte("second-generation-only\r\n"))
	if err := second.Close(); err != nil {
		t.Fatalf("close second recording: %v", err)
	}
	if len(recordingDirs) != 2 {
		t.Fatalf("recording start paths after second PTY = %#v, want two", recordingDirs)
	}
	secondDir := recordingDirs[1]
	if secondDir == firstDir {
		t.Fatalf("second PTY reused first recording directory %q", firstDir)
	}
	secondMeta, err := readRecordingMeta(secondDir)
	if err != nil {
		t.Fatalf("read second recording metadata: %v", err)
	}
	if secondMeta.StartedAt <= firstMeta.StartedAt {
		t.Fatalf("second StartedAt = %d, want later than first StartedAt %d", secondMeta.StartedAt, firstMeta.StartedAt)
	}

	if firstMeta.Generation == secondMeta.Generation {
		t.Fatalf("PTY lifetimes share generation %q", firstMeta.Generation)
	}
	firstHistory, err := store.HistoryGeneration(context.Background(), key, firstMeta.Generation, 0, MaxHistoryRangeCount)
	if err != nil {
		t.Fatalf("read first generation: %v", err)
	}
	secondHistory, err := store.HistoryGeneration(context.Background(), key, secondMeta.Generation, 0, MaxHistoryRangeCount)
	if err != nil {
		t.Fatalf("read second generation: %v", err)
	}
	firstText := recordingLinesText(firstHistory.Lines)
	secondText := recordingLinesText(secondHistory.Lines)
	if !strings.Contains(firstText, "first-generation-only") || strings.Contains(firstText, "second-generation-only") {
		t.Fatalf("first generation rows mixed PTY lifetimes: %q", firstText)
	}
	if !strings.Contains(secondText, "second-generation-only") || strings.Contains(secondText, "first-generation-only") {
		t.Fatalf("second generation rows mixed PTY lifetimes: %q", secondText)
	}
}

func installCurrentGenerationForTest(t *testing.T, store *RecordingStore, key SessionKey, generation string) string {
	t.Helper()
	dir, err := store.generationDir(key, generation)
	if err != nil {
		t.Fatalf("resolve generation directory: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create generation directory: %v", err)
	}
	if err := writeJSONAtomic(store.currentPointerPath(key), currentRecordingPointer{
		FormatVersion: recordingFormatVersion,
		Generation:    generation,
	}); err != nil {
		t.Fatalf("write current generation pointer: %v", err)
	}
	return dir
}

func markRecordingOpenForRecoveryTest(t *testing.T, dir string) {
	t.Helper()
	meta, err := readRecordingMeta(dir)
	if err != nil {
		t.Fatalf("read recording metadata: %v", err)
	}
	meta.Closed = false
	if err := writeJSONAtomic(filepath.Join(dir, "meta.json"), meta); err != nil {
		t.Fatalf("mark crash fixture open: %v", err)
	}
}

func recordingLinesText(lines []RecordingLine) string {
	var result strings.Builder
	for _, line := range lines {
		if result.Len() > 0 {
			result.WriteByte('\n')
		}
		result.WriteString(lineText(line))
	}
	return result.String()
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}

// TestValidRecordingComponentRejectsOnlyPathSeparators pins the character set
// that gates every recording path.
//
// The rejected set was once written as a raw literal, `/\\\x00`, whose escapes
// are not escapes: the forbidden characters silently became {/, \, x, 0}. Any
// terminal or workspace whose name contained an "x" or a "0" -- every
// lead-codex-* session -- was permanently denied durable history with
// "invalid terminal recording identifier", and the intended NUL guard was
// absent entirely.
func TestValidRecordingComponentRejectsOnlyPathSeparators(t *testing.T) {
	t.Parallel()

	valid := []string{
		"LOCALMODE--lead-codex-1", // contains 'x'
		"LOCALMODE--lead-shell-1",
		"workspace-v0", // contains '0'
		"x",
		"0",
		"term_ad8aa551-2943-4999-8d5c-d11823973e72",
		"Ünïcødé-session",
	}
	for _, name := range valid {
		if !validRecordingComponent(name) {
			t.Errorf("validRecordingComponent(%q) = false, want true", name)
		}
	}

	invalid := []string{
		"",
		".",
		"..",
		"a/b",
		`a\b`,
		"a\x00b",
	}
	for _, name := range invalid {
		if validRecordingComponent(name) {
			t.Errorf("validRecordingComponent(%q) = true, want false", name)
		}
	}
}
