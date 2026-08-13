package terminal

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
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
	// Zero grace keeps this short-lived recording non-trivial so Close
	// finalizes the segment files instead of discarding the generation.
	oldGrace := recordingStartedGrace
	recordingStartedGrace = 0
	defer func() { recordingStartedGrace = oldGrace }()
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

func TestRecordingRawFormatQuarantinesUnversionedLayout(t *testing.T) {
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
	recorder, err := store.StartRecording(key, 80, 24)
	if err != nil {
		t.Fatalf("StartRecording after invalid raw layout: %v", err)
	}
	if recorder.meta.Generation == generation {
		t.Fatalf("fresh recorder reused invalid generation %q", generation)
	}
	quarantined, err := filepath.Glob(dir + ".quarantine-*")
	if err != nil {
		t.Fatalf("find quarantined generation: %v", err)
	}
	if len(quarantined) != 1 {
		t.Fatalf("quarantined generations = %#v, want one", quarantined)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close fresh recorder: %v", err)
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

func TestRecordingErrorIsSurfacedAsStoppedHistory(t *testing.T) {
	recorder := &SessionRecorder{
		meta:     RecordingMeta{Generation: strings.Repeat("a", recordingGenerationBytes*2)},
		emulator: newRecordingEmulator(80, 24, nil),
		lastErr:  errors.New("recording writer stopped"),
	}
	snapshot := recorder.currentSnapshot()
	data, err := json.Marshal(snapshot.meta)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if snapshot.meta.HistoryLimited != true || fields["recordingStopped"] != true {
		t.Fatalf("recording error metadata = %s", data)
	}
}

func TestStartRecordingDoesNotWaitForLifecycleHook(t *testing.T) {
	// Short grace so the deferred started hook fires promptly on the worker
	// ticker rather than after the 5s production default.
	oldGrace := recordingStartedGrace
	recordingStartedGrace = 50 * time.Millisecond
	defer func() { recordingStartedGrace = oldGrace }()
	store := NewRecordingStore(t.TempDir(), nil)
	key := SessionKey{Workspace: "ws-test", Name: "async-start-hook"}
	hookStarted := make(chan struct{})
	releaseHook := make(chan struct{})
	store.SetLifecycleHooks(func(SessionKey, string, RecordingMeta) {
		close(hookStarted)
		<-releaseHook
	}, nil)

	type startResult struct {
		recorder *SessionRecorder
		err      error
	}
	result := make(chan startResult, 1)
	go func() {
		recorder, err := store.StartRecording(key, 80, 24)
		result <- startResult{recorder: recorder, err: err}
	}()
	<-hookStarted

	var started startResult
	returnedBeforeRelease := false
	select {
	case started = <-result:
		returnedBeforeRelease = true
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseHook)
	if !returnedBeforeRelease {
		started = <-result
		t.Error("StartRecording blocked on the lifecycle hook")
	}
	if started.err != nil {
		t.Fatalf("StartRecording: %v", started.err)
	}
	if err := started.recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestRecorderCloseDoesNotWaitForCompletedLifecycleHook(t *testing.T) {
	// Zero grace keeps the instantly-closed recording non-trivial so the
	// completed lifecycle hook actually fires; a trivial recording is
	// discarded without one.
	oldGrace := recordingStartedGrace
	recordingStartedGrace = 0
	defer func() { recordingStartedGrace = oldGrace }()
	store := NewRecordingStore(t.TempDir(), nil)
	key := SessionKey{Workspace: "ws-test", Name: "async-complete-hook"}
	hookStarted := make(chan struct{})
	releaseHook := make(chan struct{})
	store.SetLifecycleHooks(nil, func(SessionKey, string, RecordingMeta) {
		close(hookStarted)
		<-releaseHook
	})
	recorder, err := store.StartRecording(key, 80, 24)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}

	closed := make(chan error, 1)
	go func() { closed <- recorder.Close() }()
	<-hookStarted
	returnedBeforeRelease := false
	var closeErr error
	select {
	case closeErr = <-closed:
		returnedBeforeRelease = true
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseHook)
	if !returnedBeforeRelease {
		closeErr = <-closed
		t.Error("Close blocked on the completed lifecycle hook")
	}
	if closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}
}

func TestRecorderSnapshotDoesNotForceDurabilityFlush(t *testing.T) {
	store := NewRecordingStore(t.TempDir(), nil)
	key := SessionKey{Workspace: "ws-test", Name: "query-without-flush"}
	recorder, err := store.StartRecording(key, 80, 24)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	metaPath := filepath.Join(recorder.dir, "meta.json")
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Fatalf("meta.json exists before query: %v", err)
	}

	if snapshot := recorder.snapshot(); snapshot.err != nil {
		t.Fatalf("snapshot: %v", snapshot.err)
	}
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Errorf("snapshot forced a durability flush; meta.json stat error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestStartRecordingNeverReturnsClosingRecorder(t *testing.T) {
	store := NewRecordingStore(t.TempDir(), nil)
	key := SessionKey{Workspace: "ws-test", Name: "closing-recorder"}
	first, err := store.StartRecording(key, 80, 24)
	if err != nil {
		t.Fatalf("first StartRecording: %v", err)
	}

	blockedResponse := make(chan recorderSnapshot)
	queryAccepted := make(chan struct{})
	go func() {
		first.query <- recorderQuery{response: blockedResponse}
		close(queryAccepted)
	}()
	<-queryAccepted
	closeResult := make(chan error, 1)
	go func() { closeResult <- first.Close() }()
	deadline := time.Now().Add(time.Second)
	for !first.closed.Load() {
		if time.Now().After(deadline) {
			t.Fatal("first recorder did not begin closing")
		}
		time.Sleep(time.Millisecond)
	}

	type startResult struct {
		recorder *SessionRecorder
		err      error
	}
	started := make(chan startResult, 1)
	go func() {
		recorder, startErr := store.StartRecording(key, 80, 24)
		started <- startResult{recorder: recorder, err: startErr}
	}()
	var early *startResult
	select {
	case result := <-started:
		early = &result
	case <-time.After(50 * time.Millisecond):
	}
	<-blockedResponse
	if err := <-closeResult; err != nil {
		t.Fatalf("close first recorder: %v", err)
	}
	var next startResult
	if early != nil {
		next = *early
	} else {
		next = <-started
	}
	if next.err != nil {
		t.Fatalf("second StartRecording: %v", next.err)
	}
	if next.recorder == first || next.recorder.closed.Load() {
		t.Errorf("StartRecording returned closing recorder %p", next.recorder)
	}
	if next.recorder != first {
		if err := next.recorder.Close(); err != nil {
			t.Fatalf("close second recorder: %v", err)
		}
	}
}

func TestOldGenerationRecoveryNeverEvictsLiveRecorder(t *testing.T) {
	// Zero grace: the old-generation fixture closes immediately and must
	// survive as a readable generation rather than being discarded as
	// trivial.
	oldGrace := recordingStartedGrace
	recordingStartedGrace = 0
	defer func() { recordingStartedGrace = oldGrace }()
	store := NewRecordingStore(t.TempDir(), nil)
	key := SessionKey{Workspace: "ws-test", Name: "old-generation-read"}
	live, err := store.StartRecording(key, 80, 24)
	if err != nil {
		t.Fatalf("start live recording: %v", err)
	}

	oldGeneration, oldDir, err := store.allocateGenerationDir(key)
	if err != nil {
		t.Fatalf("allocate old generation: %v", err)
	}
	old, err := newSessionRecorder(store, key, oldDir, oldGeneration, unixMilliNow()-1, 80, 24, defaultRecordingQueue)
	if err != nil {
		t.Fatalf("create old recording: %v", err)
	}
	old.Append([]byte("old generation\r\n"))
	if err := old.Close(); err != nil {
		t.Fatalf("close old recording fixture: %v", err)
	}
	markRecordingOpenForRecoveryTest(t, oldDir)

	if _, err := store.recordingSnapshot(t.Context(), key, oldGeneration); err != nil {
		t.Fatalf("recover old generation: %v", err)
	}
	store.mu.Lock()
	active := store.active[key]
	store.mu.Unlock()
	if active != live {
		t.Errorf("old-generation recovery replaced live recorder: got %p want %p", active, live)
	}
	if err := live.Close(); err != nil {
		t.Fatalf("close live recorder: %v", err)
	}
}

func TestStartRecordingRecoversReferencedGenerationWithoutMeta(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Workspace: "ws-test", Name: "missing-meta"}
	store := NewRecordingStore(root, nil)
	first, err := store.StartRecording(key, 8, 2)
	if err != nil {
		t.Fatalf("start first recording: %v", err)
	}
	first.Append([]byte("recover me\r\n"))
	if err := first.Close(); err != nil {
		t.Fatalf("close first recording: %v", err)
	}
	firstDir := first.dir
	if err := os.Remove(filepath.Join(firstDir, "meta.json")); err != nil {
		t.Fatalf("remove meta.json: %v", err)
	}

	restarted := NewRecordingStore(root, nil)
	second, err := restarted.StartRecording(key, 8, 2)
	if err != nil {
		t.Fatalf("StartRecording after missing meta.json: %v", err)
	}
	if second.meta.Generation == first.meta.Generation {
		t.Fatalf("new PTY reused recovered generation %q", second.meta.Generation)
	}
	recovered, err := readRecordingMeta(firstDir)
	if err != nil {
		t.Fatalf("read rebuilt meta.json: %v", err)
	}
	if !recovered.Closed || recovered.LineCount == 0 {
		t.Fatalf("recovered metadata = %#v, want closed recording with rows", recovered)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("close second recording: %v", err)
	}
}

func TestConcurrentHistoryRecoveryUsesOneDetachedGenerationLease(t *testing.T) {
	root := t.TempDir()
	key := SessionKey{Workspace: "ws-test", Name: "concurrent-recovery"}
	seed := NewRecordingStore(root, nil)
	recorder, err := seed.StartRecording(key, 8, 2)
	if err != nil {
		t.Fatalf("seed StartRecording: %v", err)
	}
	recorder.Append([]byte("one\r\ntwo\r\nthree"))
	if err := recorder.Close(); err != nil {
		t.Fatalf("seed Close: %v", err)
	}
	generation := recorder.startMeta.Generation
	dir := recorder.dir
	markRecordingOpenForRecoveryTest(t, dir)

	restarted := NewRecordingStore(root, nil)
	const readers = 8
	start := make(chan struct{})
	errs := make(chan error, readers)
	var wg sync.WaitGroup
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			history, historyErr := restarted.HistoryGeneration(t.Context(), key, generation, 0, 100)
			if historyErr == nil && history.TotalLines != 3 {
				historyErr = fmt.Errorf("total lines = %d, want 3", history.TotalLines)
			}
			errs <- historyErr
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for historyErr := range errs {
		if historyErr != nil {
			t.Fatalf("concurrent recovery: %v", historyErr)
		}
	}
	restarted.mu.Lock()
	active := restarted.active[key]
	restarted.mu.Unlock()
	if active != nil {
		t.Fatalf("detached recovery leaked into active map: %p", active)
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
	waitForRecordingFile(t, filepath.Join(dir, "indexer.json"))
	staleCheckpoint, err := os.ReadFile(filepath.Join(dir, "indexer.json"))
	if err != nil {
		t.Fatalf("read stale checkpoint: %v", err)
	}

	recorder.Resize(12, 4)
	recorder.Append([]byte("\r\nE\x1b[H" + "X"))
	_ = recorder.FirstScreenLine()
	waitForRecordingFile(t, filepath.Join(dir, "meta.json"))
	time.Sleep(recordingFlushInterval + 50*time.Millisecond)

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
	// Zero grace so the source recording's Close finalizes its files (the
	// fixture copies them) instead of discarding the generation as trivial.
	oldGrace := recordingStartedGrace
	recordingStartedGrace = 0
	defer func() { recordingStartedGrace = oldGrace }()
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
	waitForRecordingFile(t, filepath.Join(dir, "meta.json"))

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
	// Zero grace so this short-lived recording is non-trivial and both
	// lifecycle hooks fire.
	oldGrace := recordingStartedGrace
	recordingStartedGrace = 0
	defer func() { recordingStartedGrace = oldGrace }()
	root := t.TempDir()
	key := SessionKey{Workspace: "ws-test", Name: "session-hooks"}
	store := NewRecordingStore(root, nil)
	type lifecycleCall struct {
		key  SessionKey
		dir  string
		meta RecordingMeta
	}
	started := make(chan lifecycleCall, 1)
	completed := make(chan lifecycleCall, 1)
	store.SetLifecycleHooks(
		func(gotKey SessionKey, dir string, meta RecordingMeta) {
			started <- lifecycleCall{key: gotKey, dir: dir, meta: meta}
		},
		func(gotKey SessionKey, dir string, meta RecordingMeta) {
			completed <- lifecycleCall{key: gotKey, dir: dir, meta: meta}
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
	startCall := <-started
	completeCall := <-completed
	if startCall.key != key || startCall.dir == "" || startCall.meta.StartedAt == 0 {
		t.Fatalf("start hook = %#v", startCall)
	}
	if completeCall.key != key || !completeCall.meta.Closed {
		t.Fatalf("complete hook = %#v", completeCall)
	}
	generation := startCall.meta.Generation
	if !validRecordingGeneration(generation) {
		t.Fatalf("started generation = %q, want a 32-character lowercase hex identity", generation)
	}
	wantPath := filepath.Join(root, key.Workspace, key.Name, "generations", generation)
	if startCall.dir != wantPath || completeCall.dir != wantPath {
		t.Fatalf("lifecycle paths = started %q completed %q, want %q", startCall.dir, completeCall.dir, wantPath)
	}
}

func TestRecordingStoreReusedSessionStartsDistinctReadableGeneration(t *testing.T) {
	// Zero grace so both short-lived generations persist and fire their
	// started hooks instead of being discarded as trivial.
	oldGrace := recordingStartedGrace
	recordingStartedGrace = 0
	defer func() { recordingStartedGrace = oldGrace }()
	root := t.TempDir()
	key := SessionKey{Workspace: "ws-test", Name: "reused-session"}
	store := NewRecordingStore(root, nil)
	recordingDirs := make(chan string, 2)
	store.SetLifecycleHooks(
		func(_ SessionKey, dir string, _ RecordingMeta) {
			recordingDirs <- dir
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
	firstDir := <-recordingDirs
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
	secondDir := <-recordingDirs
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

func waitForRecordingFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * recordingFlushInterval)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for periodic recording flush: %s", path)
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

func TestInstantExitRecordingIsDiscardedWithoutHistoryRecord(t *testing.T) {
	root := t.TempDir()
	s := NewRecordingStore(root, nil)
	var started, completed atomic.Int32
	s.SetLifecycleHooks(
		func(SessionKey, string, RecordingMeta) { started.Add(1) },
		func(SessionKey, string, RecordingMeta) { completed.Add(1) },
	)
	key := SessionKey{Workspace: "ws-test", Name: "stillborn"}

	recorder, err := s.StartRecording(key, 80, 24)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	time.Sleep(100 * time.Millisecond) // let any (wrongly) fired hooks land
	if got := started.Load(); got != 0 {
		t.Fatalf("onStarted fired %d times for a stillborn session, want 0", got)
	}
	if got := completed.Load(); got != 0 {
		t.Fatalf("onCompleted fired %d times for a stillborn session, want 0", got)
	}
	sessionRoot := filepath.Join(root, "ws-test", "stillborn")
	if _, err := os.Stat(filepath.Join(sessionRoot, "current.json")); !os.IsNotExist(err) {
		t.Fatalf("current.json still points at a discarded generation (stat err=%v)", err)
	}
	if entries, err := os.ReadDir(filepath.Join(sessionRoot, "generations")); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				t.Fatalf("stillborn generation dir %q survived discard", entry.Name())
			}
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("read generations dir: %v", err)
	}

	// The key must remain usable after a discard.
	again, err := s.StartRecording(key, 80, 24)
	if err != nil {
		t.Fatalf("StartRecording after discard: %v", err)
	}
	again.Append([]byte("fresh\n"))
	_ = again.Close()
}

func TestRecordingLifecycleDefersUntilFirstCommittedLine(t *testing.T) {
	root := t.TempDir()
	s := NewRecordingStore(root, nil)
	var started atomic.Int32
	s.SetLifecycleHooks(
		func(SessionKey, string, RecordingMeta) { started.Add(1) },
		nil,
	)
	key := SessionKey{Workspace: "ws-test", Name: "defers"}

	recorder, err := s.StartRecording(key, 20, 4)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	defer func() { _ = recorder.Close() }()

	recorder.snapshot() // drain the queue so any eager hook would have fired
	if got := started.Load(); got != 0 {
		t.Fatalf("onStarted fired %d times before any committed line, want 0", got)
	}

	// Scroll enough lines through a 4-row screen to commit durable history.
	for i := 0; i < 12; i++ {
		recorder.Append([]byte(fmt.Sprintf("line-%d\r\n", i)))
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		recorder.snapshot()
		if started.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := started.Load(); got != 1 {
		t.Fatalf("onStarted fired %d times after first committed line, want 1", got)
	}
}

func TestIdleRecordingLifecycleFiresAfterGrace(t *testing.T) {
	oldGrace := recordingStartedGrace
	recordingStartedGrace = 50 * time.Millisecond
	defer func() { recordingStartedGrace = oldGrace }()

	root := t.TempDir()
	s := NewRecordingStore(root, nil)
	var started, completed atomic.Int32
	s.SetLifecycleHooks(
		func(SessionKey, string, RecordingMeta) { started.Add(1) },
		func(SessionKey, string, RecordingMeta) { completed.Add(1) },
	)
	key := SessionKey{Workspace: "ws-test", Name: "idle-but-real"}

	recorder, err := s.StartRecording(key, 80, 24)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && started.Load() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if got := started.Load(); got != 1 {
		t.Fatalf("onStarted fired %d times after grace elapsed on an idle session, want 1", got)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && completed.Load() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if got := completed.Load(); got != 1 {
		t.Fatalf("onCompleted fired %d times for a graced session, want 1", got)
	}
	entries, err := os.ReadDir(filepath.Join(root, "ws-test", "idle-but-real", "generations"))
	if err != nil {
		t.Fatalf("read generations dir: %v", err)
	}
	dirs := 0
	for _, entry := range entries {
		if entry.IsDir() {
			dirs++
		}
	}
	if dirs != 1 {
		t.Fatalf("generation dirs = %d, want 1 retained (graced session is not trivial)", dirs)
	}
}
