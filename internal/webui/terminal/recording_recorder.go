package terminal

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const (
	recordingFormatVersion   = 1
	recordingFileHeaderSize  = 8
	recordingFrameHeaderSize = 13
	recordingFlushInterval   = time.Second
	defaultRecordingQueue    = 256
	maxRecordingFrameSize    = 64 << 20
)

var recordingFileHeader = [recordingFileHeaderSize]byte{'L', 'O', 'O', 'M', 'T', 'R', 'M', recordingFormatVersion}

// recordingStartedGrace is how long a session must live (absent any committed
// line) before the started lifecycle hook fires and its recording counts as
// non-trivial. Recordings that end sooner with no committed lines and no
// issue association are discarded, so crash-looping spawns don't accrete a
// generation directory and a session-history record per respawn. Variable so
// tests can shorten it.
var recordingStartedGrace = 5 * time.Second

type recordingEventKind uint8

const (
	recordingOutput      recordingEventKind = 1
	recordingResizeEvent recordingEventKind = 2
	// recordingBarrierEvent is an in-memory sync marker for attach replay:
	// the worker responds with the screen rendered at exactly this queue
	// position. It is never written to the raw segment.
	recordingBarrierEvent recordingEventKind = 3
)

type recordingEvent struct {
	kind            recordingEventKind
	payload         []byte
	timestamp       int64
	cols            uint16
	rows            uint16
	sequence        uint64
	barrierResponse chan attachReplay
}

type recorderQuery struct {
	after    uint64
	response chan recorderSnapshot
}

type recorderUpdate struct {
	issueID  string
	response chan error
}

type recorderReplayBaseline struct {
	checkpoint      []byte
	body            []byte
	throughSequence uint64
	cols            uint16
	rows            uint16
}

type recorderSnapshot struct {
	meta             RecordingMeta
	screen           []RecordingLine
	activeScreen     []RecordingLine
	pendingLines     []RecordingLine
	durableLineCount uint64
	cursorX          int
	cursorY          int
	dir              string
	err              error
}

// attachReplay is delivered to a new attachment once the recorder worker has
// processed every chunk enqueued before the attachment registered: the
// virtual-history coordinate plus a boundary-safe ANSI rendering of the live
// screen. Rendering from emulator cells (instead of replaying raw ring bytes)
// means the replay can never begin or end mid-escape-sequence, and it shows
// the current screen even when the raw output has rotated out of the ring.
type attachReplay struct {
	firstScreenLine uint64
	screen          []byte
}

type indexerCheckpoint struct {
	FormatVersion         uint8                  `json:"formatVersion"`
	Generation            uint64                 `json:"generation"`
	LastIndexedByteOffset uint64                 `json:"lastIndexedByteOffset"`
	LineCount             uint64                 `json:"lineCount"`
	ResizeCount           uint64                 `json:"resizeCount"`
	EmulatorState         recordingEmulatorState `json:"serializedEmulatorState"`
}

// SessionRecorder accepts PTY output without performing disk I/O on the
// caller. A bounded queue preserves the PTY hot path; overload is represented
// as a durable gap count rather than back-pressure.
type SessionRecorder struct {
	store *RecordingStore
	key   SessionKey
	dir   string

	events chan recordingEvent
	query  chan recorderQuery
	update chan recorderUpdate
	stop   chan struct{}
	done   chan struct{}

	closed        atomic.Bool
	droppedChunks atomic.Uint64
	unsafeGap     atomic.Bool
	closeOnce     sync.Once
	enqueueMu     sync.Mutex
	nextSequence  uint64
	beforeEnqueue func() // deterministic Append/Close interleaving test hook

	rawFile   *os.File
	rawBuf    *bufio.Writer
	linesFile *os.File
	linesBuf  *bufio.Writer
	idxFile   *os.File
	idxBuf    *bufio.Writer

	rawLen         uint64
	linesLen       uint64
	durableLines   uint64
	pendingLines   []RecordingLine
	meta           RecordingMeta
	startMeta      RecordingMeta
	emulator       *recordingEmulator
	geometrySeen   bool
	lastErr        error
	processedSeq   uint64
	rebasedThrough uint64
	progressMu     sync.Mutex
	replayWaiters  map[uint64][]chan attachReplay
	replayMu       sync.RWMutex
	replaySnapshot func() recorderReplayBaseline

	lifecycleMu      sync.Mutex
	lifecycleStarted chan struct{}
	lifecycleFired   bool
}

// newSessionRecorder opens and crash-recovers the three segment files as one
// unit. Each step's error path has to unwind the files already opened, so
// splitting it would mean threading that cleanup through helpers.
//
//nolint:funlen // sequential open-and-recover sharing one unwind path
func newSessionRecorder(store *RecordingStore, key SessionKey, dir, generation string, startedAt int64, cols, rows uint16, queueSize int) (*SessionRecorder, error) {
	if queueSize <= 0 {
		queueSize = defaultRecordingQueue
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create recording directory: %w", err)
	}

	rawPath := filepath.Join(dir, "raw.seg")
	rawLen, freshRaw, err := recoverRawSegment(rawPath)
	if err != nil {
		return nil, err
	}
	linesPath := filepath.Join(dir, "lines.jsonl")
	idxPath := filepath.Join(dir, "lines.idx")
	lineCount, linesLen, err := recoverLineIndex(linesPath, idxPath)
	if err != nil {
		return nil, err
	}

	r := &SessionRecorder{
		store:  store,
		key:    key,
		dir:    dir,
		events: make(chan recordingEvent, queueSize),
		query:  make(chan recorderQuery),
		update: make(chan recorderUpdate),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
		rawLen: rawLen,
		meta: RecordingMeta{
			FormatVersion: recordingFormatVersion,
			Generation:    generation,
			SessionKey:    key.Name,
			StartedAt:     startedAt,
			Cols:          normalizedRecordingCols(cols),
			Rows:          normalizedRecordingRows(rows),
			LineCount:     lineCount,
			RawLen:        rawLen,
		},
		linesLen:      linesLen,
		durableLines:  lineCount,
		replayWaiters: make(map[uint64][]chan attachReplay),
	}
	metaErr := readJSONFile(filepath.Join(dir, "meta.json"), &r.meta)
	if metaErr != nil && !os.IsNotExist(metaErr) {
		return nil, fmt.Errorf("read recording metadata: %w", metaErr)
	}
	if metaErr == nil {
		if r.meta.FormatVersion != recordingFormatVersion {
			return nil, fmt.Errorf("recording metadata format version %d does not match raw format version %d", r.meta.FormatVersion, recordingFormatVersion)
		}
		if r.meta.Generation != generation {
			return nil, fmt.Errorf("recording metadata generation %q does not match directory generation %q", r.meta.Generation, generation)
		}
	}
	r.meta.FormatVersion = recordingFormatVersion
	r.meta.Generation = generation
	r.meta.SessionKey = key.Name
	if r.meta.StartedAt == 0 {
		r.meta.StartedAt = startedAt
		if r.meta.StartedAt == 0 {
			r.meta.StartedAt = unixMilliNow()
		}
	}
	if r.meta.Cols == 0 {
		r.meta.Cols = normalizedRecordingCols(cols)
	}
	if r.meta.Rows == 0 {
		r.meta.Rows = normalizedRecordingRows(rows)
	}
	r.meta.LineCount = lineCount
	r.meta.RawLen = rawLen
	r.meta.Closed = false
	r.droppedChunks.Store(r.meta.Gaps)
	// Re-arm only a gap that was still pending (not yet re-baselined) when the
	// previous process stopped; a historical Gaps count alone already has its
	// marker committed in lines.jsonl.
	r.unsafeGap.Store(r.meta.PendingGap)

	if err := r.openAppendFiles(rawPath, linesPath, idxPath); err != nil {
		r.closeFiles()
		return nil, err
	}
	if err := r.restoreIndexer(linesPath, idxPath); err != nil {
		r.closeFiles()
		return nil, err
	}
	if freshRaw {
		r.applyEvent(recordingEvent{
			kind: recordingResizeEvent, timestamp: r.meta.StartedAt,
			cols: normalizedRecordingCols(cols), rows: normalizedRecordingRows(rows),
		})
		if r.lastErr != nil {
			r.closeFiles()
			return nil, r.lastErr
		}
	}
	r.startMeta = r.meta

	go r.run()
	return r, nil
}

func normalizedRecordingCols(cols uint16) uint16 {
	if cols == 0 {
		return 80
	}
	return cols
}

func normalizedRecordingRows(rows uint16) uint16 {
	if rows == 0 {
		return 24
	}
	return rows
}

func (r *SessionRecorder) openAppendFiles(rawPath, linesPath, idxPath string) error {
	var err error
	r.rawFile, err = os.OpenFile(rawPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open raw segment: %w", err)
	}
	r.linesFile, err = os.OpenFile(linesPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open line log: %w", err)
	}
	r.idxFile, err = os.OpenFile(idxPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open line index: %w", err)
	}
	r.rawBuf = bufio.NewWriterSize(r.rawFile, 64<<10)
	r.linesBuf = bufio.NewWriterSize(r.linesFile, 64<<10)
	r.idxBuf = bufio.NewWriterSize(r.idxFile, 16<<10)
	return nil
}

func (r *SessionRecorder) restoreIndexer(linesPath, idxPath string) error {
	checkpointPath := filepath.Join(r.dir, "indexer.json")
	var checkpoint indexerCheckpoint
	checkpointOK := readJSONFile(checkpointPath, &checkpoint) == nil &&
		checkpoint.FormatVersion == recordingFormatVersion &&
		checkpoint.Generation == r.meta.CheckpointGeneration &&
		checkpoint.LineCount == r.meta.LineCount &&
		checkpoint.ResizeCount == uint64(len(r.meta.Resizes)) &&
		checkpoint.LastIndexedByteOffset >= recordingFileHeaderSize &&
		checkpoint.LastIndexedByteOffset <= r.rawLen &&
		checkpoint.EmulatorState.Cols == int(r.meta.Cols) &&
		checkpoint.EmulatorState.Rows == int(r.meta.Rows)

	if checkpointOK {
		r.emulator = restoreRecordingEmulator(checkpoint.EmulatorState, r.writeCommittedLine)
		r.geometrySeen = true
		if err := r.replayRaw(checkpoint.LastIndexedByteOffset); err == nil {
			return nil
		}
	}

	// The checkpoint is optional acceleration. If it is missing or does not
	// exactly describe the durable line tail, rebuild derived rows from raw.seg.
	if err := r.resetLineFiles(linesPath, idxPath); err != nil {
		return err
	}
	r.meta.Resizes = nil
	r.geometrySeen = false
	r.emulator = newRecordingEmulator(80, 24, r.writeCommittedLine)
	return r.replayRaw(recordingFileHeaderSize)
}

func (r *SessionRecorder) resetLineFiles(linesPath, idxPath string) error {
	if r.linesFile != nil {
		_ = r.linesFile.Close()
	}
	if r.idxFile != nil {
		_ = r.idxFile.Close()
	}
	if err := os.Truncate(linesPath, 0); err != nil {
		return fmt.Errorf("truncate line log for rebuild: %w", err)
	}
	if err := os.Truncate(idxPath, 0); err != nil {
		return fmt.Errorf("truncate line index for rebuild: %w", err)
	}
	var err error
	r.linesFile, err = os.OpenFile(linesPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("reopen line log: %w", err)
	}
	r.idxFile, err = os.OpenFile(idxPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("reopen line index: %w", err)
	}
	r.linesBuf = bufio.NewWriterSize(r.linesFile, 64<<10)
	r.idxBuf = bufio.NewWriterSize(r.idxFile, 16<<10)
	r.linesLen = 0
	r.meta.LineCount = 0
	return nil
}

func (r *SessionRecorder) replayRaw(from uint64) error {
	if from < recordingFileHeaderSize {
		return fmt.Errorf("raw replay offset %d precedes format header", from)
	}
	f, err := os.Open(filepath.Join(r.dir, "raw.seg"))
	if err != nil {
		return fmt.Errorf("open raw segment for replay: %w", err)
	}
	defer f.Close()
	if _, err := f.Seek(int64(from), io.SeekStart); err != nil {
		return fmt.Errorf("seek raw replay: %w", err)
	}
	offset := from
	header := make([]byte, recordingFrameHeaderSize)
	for offset < r.rawLen {
		if _, err := io.ReadFull(f, header); err != nil {
			return fmt.Errorf("read recovered frame header: %w", err)
		}
		kind := recordingEventKind(header[0])
		length := binary.BigEndian.Uint32(header[1:5])
		timestamp := int64(binary.BigEndian.Uint64(header[5:13]))
		payload := make([]byte, int(length))
		if _, err := io.ReadFull(f, payload); err != nil {
			return fmt.Errorf("read recovered frame payload: %w", err)
		}
		switch kind {
		case recordingOutput:
			if !r.geometrySeen {
				return fmt.Errorf("raw output frame at %d precedes initial geometry", offset)
			}
			if err := r.emulator.feed(payload, timestamp, offset); err != nil {
				return fmt.Errorf("replay raw frame at %d: %w", offset, err)
			}
		case recordingResizeEvent:
			if len(payload) != 4 {
				return fmt.Errorf("raw geometry frame at %d has length %d", offset, len(payload))
			}
			r.applyGeometry(binary.BigEndian.Uint16(payload[:2]), binary.BigEndian.Uint16(payload[2:]), timestamp, offset)
			if r.lastErr != nil {
				return r.lastErr
			}
		default:
			return fmt.Errorf("raw frame at %d has unknown event kind %d", offset, kind)
		}
		offset += recordingFrameHeaderSize + uint64(length)
	}
	return nil
}

// Append queues an output chunk and never waits for the disk writer.
func (r *SessionRecorder) Append(data []byte) {
	if r == nil || len(data) == 0 {
		return
	}
	chunk := append([]byte(nil), data...)
	r.enqueueMu.Lock()
	defer r.enqueueMu.Unlock()
	if r.closed.Load() {
		return
	}
	if r.beforeEnqueue != nil {
		r.beforeEnqueue()
	}
	select {
	case r.events <- recordingEvent{kind: recordingOutput, payload: chunk, timestamp: unixMilliNow(), sequence: r.nextSequence + 1}:
		r.nextSequence++
	default:
		r.noteDroppedEvent()
	}
}

// Resize records live geometry in the same ordered queue as output. It is
// asynchronous for the same reason as Append.
func (r *SessionRecorder) Resize(cols, rows uint16) {
	if r == nil || cols == 0 || rows == 0 {
		return
	}
	r.enqueueMu.Lock()
	defer r.enqueueMu.Unlock()
	if r.closed.Load() {
		return
	}
	select {
	case r.events <- recordingEvent{kind: recordingResizeEvent, timestamp: unixMilliNow(), cols: cols, rows: rows, sequence: r.nextSequence + 1}:
		r.nextSequence++
	default:
		r.noteDroppedEvent()
	}
}

func (r *SessionRecorder) snapshot() recorderSnapshot {
	if r == nil {
		return recorderSnapshot{err: errors.New("recording unavailable")}
	}
	if r.closed.Load() {
		<-r.done
		meta, err := readRecordingMeta(r.dir)
		return recorderSnapshot{meta: meta, durableLineCount: meta.LineCount, dir: r.dir, err: err}
	}
	after := r.enqueuedSequence()
	response := make(chan recorderSnapshot, 1)
	select {
	case r.query <- recorderQuery{after: after, response: response}:
		return <-response
	case <-r.done:
		meta, err := readRecordingMeta(r.dir)
		return recorderSnapshot{meta: meta, durableLineCount: meta.LineCount, dir: r.dir, err: err}
	}
}

func (r *SessionRecorder) enqueuedSequence() uint64 {
	if r == nil {
		return 0
	}
	r.enqueueMu.Lock()
	defer r.enqueueMu.Unlock()
	return r.nextSequence
}

// FirstScreenLine returns the immutable line count after synchronizing the
// recorder worker through every event already accepted by this call.
func (r *SessionRecorder) FirstScreenLine() uint64 {
	return (<-r.attachReplayBarrier()).firstScreenLine
}

// attachReplayBarrier registers an attach point: the returned channel yields
// the history coordinate and rendered screen reflecting exactly the output
// enqueued before this call — never more, or the client would double-apply
// chunks it also receives live. If the worker is behind, the waiter fires in
// markProcessed at the exact registered sequence; if it is caught up, a
// barrier event is enqueued while enqueueMu keeps the (empty) queue frozen,
// so the worker renders at exactly this stream position.
func (r *SessionRecorder) attachReplayBarrier() <-chan attachReplay {
	response := make(chan attachReplay, 1)
	if r == nil {
		response <- attachReplay{}
		return response
	}
	r.enqueueMu.Lock()
	defer r.enqueueMu.Unlock()
	if r.closed.Load() {
		// Recorder is stopping: answer from the durable snapshot once the
		// worker exits.
		go func() {
			snap := r.snapshot()
			response <- attachReplay{
				firstScreenLine: snap.meta.LineCount,
				screen:          renderRecorderScreen(snap.activeScreen, snap.cursorX, snap.cursorY),
			}
		}()
		return response
	}
	sequence := r.nextSequence
	r.progressMu.Lock()
	reached := r.processedSeq >= sequence
	if !reached {
		r.replayWaiters[sequence] = append(r.replayWaiters[sequence], response)
	}
	r.progressMu.Unlock()
	if reached {
		select {
		case r.events <- recordingEvent{kind: recordingBarrierEvent, sequence: sequence, barrierResponse: response}:
		default:
			// Unreachable in practice: reached implies the queue is empty
			// and enqueueMu blocks new events. Degrade to a snapshot
			// round-trip rather than risking a stuck attach.
			go func() {
				snap := r.snapshot()
				response <- attachReplay{
					firstScreenLine: snap.meta.LineCount,
					screen:          renderRecorderScreen(snap.activeScreen, snap.cursorX, snap.cursorY),
				}
			}()
		}
	}
	return response
}

// markProcessed runs on the recorder worker, so reading meta/emulator state
// here is safe; the screen is rendered at most once per call, and only when a
// waiter's registered sequence has just been reached.
func (r *SessionRecorder) markProcessed(sequence uint64) {
	r.progressMu.Lock()
	r.processedSeq = sequence
	var rendered []byte
	renderReady := false
	for target, waiters := range r.replayWaiters {
		if target > sequence {
			continue
		}
		if !renderReady {
			rendered = renderRecorderScreen(r.emulator.activeScreenRows(), r.emulator.CursorX, r.emulator.CursorY)
			renderReady = true
		}
		for _, waiter := range waiters {
			waiter <- attachReplay{firstScreenLine: r.meta.LineCount, screen: rendered}
		}
		delete(r.replayWaiters, target)
	}
	r.progressMu.Unlock()
}

// Close drains queued events, commits the final normal-buffer screen, and
// durably marks the recording closed. It is safe to call repeatedly.
func (r *SessionRecorder) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.enqueueMu.Lock()
		r.closed.Store(true)
		close(r.stop)
		r.enqueueMu.Unlock()
	})
	<-r.done
	return r.lastErr
}

func (r *SessionRecorder) setReplaySource(source func() recorderReplayBaseline) {
	if r == nil {
		return
	}
	r.replayMu.Lock()
	r.replaySnapshot = source
	r.replayMu.Unlock()
}

func (r *SessionRecorder) run() {
	ticker := time.NewTicker(recordingFlushInterval)
	defer ticker.Stop()
	defer close(r.done)
	for {
		select {
		case event := <-r.events:
			r.handleEvent(event)
			r.maybeStartLifecycle()
		case query := <-r.query:
			r.processThrough(query.after)
			query.response <- r.currentSnapshot()
		case update := <-r.update:
			r.meta.IssueID = update.issueID
			r.flush(false)
			update.response <- r.lastErr
			r.maybeStartLifecycle()
		case <-ticker.C:
			r.flush(false)
			r.maybeStartLifecycle()
		case <-r.stop:
			for {
				select {
				case event := <-r.events:
					r.handleEvent(event)
				default:
					r.progressMu.Lock()
					processed := r.processedSeq
					r.progressMu.Unlock()
					if r.trivialRecording() {
						// Nothing worth keeping: drop the generation
						// instead of finalizing it, and skip both
						// lifecycle hooks (no record was ever created).
						r.closeFiles()
						r.store.discardGeneration(r.key, r.meta.Generation, r.dir)
						r.store.removeActive(r.key, r)
						return
					}
					r.startLifecycle()
					r.rebaselinePendingGap(unixMilliNow(), processed)
					r.finalize()
					r.store.removeActive(r.key, r)
					r.completeLifecycle()
					return
				}
			}
		}
	}
}

func (r *SessionRecorder) processThrough(sequence uint64) {
	for {
		r.progressMu.Lock()
		processed := r.processedSeq
		r.progressMu.Unlock()
		if processed >= sequence {
			return
		}
		event := <-r.events
		r.handleEvent(event)
	}
}

// handleEvent dispatches one queued event on the recorder worker. Barrier
// events respond with the screen rendered at exactly this point in the
// stream and are never applied or persisted; everything else advances the
// emulator and the processed-sequence watermark.
func (r *SessionRecorder) handleEvent(event recordingEvent) {
	if event.kind == recordingBarrierEvent {
		event.barrierResponse <- attachReplay{
			firstScreenLine: r.meta.LineCount,
			screen:          renderRecorderScreen(r.emulator.activeScreenRows(), r.emulator.CursorX, r.emulator.CursorY),
		}
		return
	}
	r.applyEvent(event)
	r.markProcessed(event.sequence)
}

func (r *SessionRecorder) applyEvent(event recordingEvent) {
	if r.lastErr != nil {
		return
	}
	if r.unsafeGap.Swap(false) {
		if err := r.rebaselineAfterGap(event.timestamp, event.sequence); err != nil {
			r.setError(err)
			return
		}
	}
	if event.sequence != 0 && event.sequence <= r.rebasedThrough {
		return
	}
	switch event.kind {
	case recordingOutput:
		if len(event.payload) > maxRecordingFrameSize {
			r.noteDroppedEvent()
			return
		}
		if !r.geometrySeen {
			r.setError(errors.New("terminal output arrived before initial recording geometry"))
			return
		}
		frameOffset, err := r.writeRawEvent(recordingOutput, event.timestamp, event.payload)
		if err != nil {
			r.setError(err)
			return
		}
		if err := r.emulator.feed(event.payload, event.timestamp, frameOffset); err != nil {
			r.setError(err)
		}
	case recordingResizeEvent:
		var payload [4]byte
		binary.BigEndian.PutUint16(payload[:2], event.cols)
		binary.BigEndian.PutUint16(payload[2:], event.rows)
		frameOffset, err := r.writeRawEvent(recordingResizeEvent, event.timestamp, payload[:])
		if err != nil {
			r.setError(err)
			return
		}
		r.applyGeometry(event.cols, event.rows, event.timestamp, frameOffset)
	}
}

func (r *SessionRecorder) writeRawEvent(kind recordingEventKind, timestamp int64, payload []byte) (uint64, error) {
	frameOffset := r.rawLen
	header := make([]byte, recordingFrameHeaderSize)
	header[0] = byte(kind)
	binary.BigEndian.PutUint32(header[1:5], uint32(len(payload)))
	binary.BigEndian.PutUint64(header[5:13], uint64(timestamp))
	if _, err := r.rawBuf.Write(header); err != nil {
		return 0, fmt.Errorf("write raw event header: %w", err)
	}
	if _, err := r.rawBuf.Write(payload); err != nil {
		return 0, fmt.Errorf("write raw event payload: %w", err)
	}
	r.rawLen += recordingFrameHeaderSize + uint64(len(payload))
	r.meta.RawLen = r.rawLen
	return frameOffset, nil
}

func (r *SessionRecorder) applyGeometry(cols, rows uint16, timestamp int64, frameOffset uint64) {
	if cols == 0 || rows == 0 {
		r.setError(fmt.Errorf("invalid recording geometry %dx%d", cols, rows))
		return
	}
	initial := !r.geometrySeen
	r.geometrySeen = true
	if !initial {
		r.meta.Resizes = append(r.meta.Resizes, RecordingResize{
			Timestamp: timestamp, Cols: cols, Rows: rows,
		})
	}
	r.meta.Cols, r.meta.Rows = cols, rows
	r.emulator.LastTimestamp = timestamp
	r.emulator.LastFrameOff = frameOffset
	r.emulator.resize(cols, rows)
	if r.emulator.err != nil {
		r.setError(r.emulator.err)
	}
}

func (r *SessionRecorder) writeCommittedLine(runs []RecordingRun, timestamp int64, frameOffset uint64) error {
	line := RecordingLine{
		Index: r.meta.LineCount, Timestamp: timestamp,
		Offset: OpaqueRecordingOffset(frameOffset), Cols: uint16(r.emulator.Cols), Runs: runs,
	}
	data, err := json.Marshal(line)
	if err != nil {
		return fmt.Errorf("marshal committed line: %w", err)
	}
	var idx [8]byte
	binary.BigEndian.PutUint64(idx[:], r.linesLen)
	if _, err := r.idxBuf.Write(idx[:]); err != nil {
		return fmt.Errorf("write line index: %w", err)
	}
	if _, err := r.linesBuf.Write(data); err != nil {
		return fmt.Errorf("write committed line: %w", err)
	}
	if err := r.linesBuf.WriteByte('\n'); err != nil {
		return fmt.Errorf("terminate committed line: %w", err)
	}
	r.linesLen += uint64(len(data) + 1)
	r.meta.LineCount++
	r.pendingLines = append(r.pendingLines, line)
	return nil
}

func (r *SessionRecorder) noteDroppedEvent() {
	r.droppedChunks.Add(1)
	r.unsafeGap.Store(true)
}

func (r *SessionRecorder) rebaselinePendingGap(timestamp int64, sequence uint64) {
	if !r.unsafeGap.Swap(false) || r.lastErr != nil {
		return
	}
	if err := r.rebaselineAfterGap(timestamp, sequence); err != nil {
		r.setError(err)
	}
}

func (r *SessionRecorder) rebaselineAfterGap(timestamp int64, sequence uint64) error {
	r.replayMu.RLock()
	source := r.replaySnapshot
	r.replayMu.RUnlock()
	baseline := recorderReplayBaseline{
		throughSequence: sequence,
		cols:            r.meta.Cols,
		rows:            r.meta.Rows,
	}
	if source != nil {
		baseline = source()
	} else if sequence > 0 {
		// A standalone recorder has no PTY replay ring. Re-seed to a clear
		// screen, then still apply the current accepted event.
		baseline.throughSequence = sequence - 1
	}
	if baseline.cols == 0 {
		baseline.cols = normalizedRecordingCols(r.meta.Cols)
	}
	if baseline.rows == 0 {
		baseline.rows = normalizedRecordingRows(r.meta.Rows)
	}
	if baseline.throughSequence < sequence {
		baseline.throughSequence = sequence
	}

	// The marker lives in lines.jsonl/meta rather than a new raw frame kind.
	// The re-seed itself is an ordinary output frame, so the v1 validity scan
	// and replay paths need no new-kind coupling.
	r.emulator.CommitBlocked = false
	if err := r.writeCommittedLine(
		[]RecordingRun{{Text: "[terminal history gap: output dropped]"}},
		timestamp,
		r.rawLen,
	); err != nil {
		return fmt.Errorf("write recording gap marker: %w", err)
	}
	replay := make([]byte, 0, len(baseline.checkpoint)+len(screenResetSeq)+len(baseline.body))
	replay = append(replay, baseline.checkpoint...)
	replay = append(replay, screenResetSeq...)
	replay = append(replay, baseline.body...)
	frameOffset, err := r.writeRawEvent(recordingOutput, timestamp, replay)
	if err != nil {
		return fmt.Errorf("write recording gap baseline: %w", err)
	}
	r.emulator = newRecordingEmulator(baseline.cols, baseline.rows, r.writeCommittedLine)
	r.geometrySeen = true
	r.meta.Cols, r.meta.Rows = baseline.cols, baseline.rows
	if err := r.emulator.feed(replay, timestamp, frameOffset); err != nil {
		return fmt.Errorf("apply recording gap baseline: %w", err)
	}
	r.rebasedThrough = baseline.throughSequence
	r.meta.HistoryLimited = true
	return nil
}

func (r *SessionRecorder) historyLimited() bool {
	return r.meta.HistoryLimited || r.emulator.CommitBlocked || r.droppedChunks.Load() > 0 || r.lastErr != nil
}

func (r *SessionRecorder) currentSnapshot() recorderSnapshot {
	meta := r.meta
	meta.AltScreen = meta.AltScreen || r.emulator.EverAlt
	meta.Gaps = r.droppedChunks.Load()
	meta.PendingGap = r.unsafeGap.Load()
	meta.UnhandledSequences = r.emulator.unhandledSequenceSummary()
	meta.HistoryLimited = r.historyLimited()
	meta.RecordingStopped = r.lastErr != nil
	screen := r.emulator.screenRows()
	for i := range screen {
		screen[i].Index = meta.LineCount + uint64(i)
	}
	pending := append([]RecordingLine(nil), r.pendingLines...)
	return recorderSnapshot{
		meta: meta, screen: screen,
		activeScreen:     r.emulator.activeScreenRows(),
		pendingLines:     pending,
		durableLineCount: r.durableLines,
		cursorX:          r.emulator.CursorX,
		cursorY:          r.emulator.CursorY,
		dir:              r.dir, err: r.lastErr,
	}
}

func (r *SessionRecorder) finalize() {
	if r.lastErr == nil && !r.emulator.CommitBlocked && r.emulator.LastTimestamp != 0 {
		for _, line := range r.emulator.screenRows() {
			if err := r.writeCommittedLine(line.Runs, line.Timestamp, r.emulator.LastFrameOff); err != nil {
				r.setError(err)
				break
			}
		}
		r.emulator.Screen = makeRecordingScreen(r.emulator.Rows, r.emulator.Cols)
		r.emulator.CursorX, r.emulator.CursorY = 0, 0
		r.emulator.Primary = nil
		r.emulator.Alt = false
		r.emulator.LastTimestamp = 0
	}
	r.meta.Closed = true
	r.flush(true)
	r.closeFiles()
}

func (r *SessionRecorder) flush(syncFiles bool) {
	r.meta.RawLen = r.rawLen
	r.meta.AltScreen = r.meta.AltScreen || r.emulator.EverAlt
	r.meta.Gaps = r.droppedChunks.Load()
	r.meta.PendingGap = r.unsafeGap.Load()
	r.meta.UnhandledSequences = r.emulator.unhandledSequenceSummary()
	r.meta.HistoryLimited = r.historyLimited()
	r.meta.RecordingStopped = r.lastErr != nil
	flushed := true
	for _, writer := range []*bufio.Writer{r.rawBuf, r.linesBuf, r.idxBuf} {
		if writer != nil {
			if err := writer.Flush(); err != nil {
				flushed = false
				r.setError(fmt.Errorf("flush recording: %w", err))
			}
		}
	}
	if flushed {
		r.durableLines = r.meta.LineCount
		r.pendingLines = nil
	}
	if syncFiles {
		for _, file := range []*os.File{r.rawFile, r.linesFile, r.idxFile} {
			if file != nil {
				if err := file.Sync(); err != nil {
					r.setError(fmt.Errorf("sync recording: %w", err))
				}
			}
		}
	}
	if r.emulator.parserGround() {
		generation := r.meta.CheckpointGeneration + 1
		checkpoint := indexerCheckpoint{
			FormatVersion:         recordingFormatVersion,
			Generation:            generation,
			LastIndexedByteOffset: r.rawLen,
			LineCount:             r.meta.LineCount,
			ResizeCount:           uint64(len(r.meta.Resizes)),
			EmulatorState:         r.emulator.recordingEmulatorState,
		}
		if err := writeJSONAtomic(filepath.Join(r.dir, "indexer.json"), checkpoint); err != nil {
			r.setError(fmt.Errorf("write indexer checkpoint: %w", err))
		} else {
			r.meta.CheckpointGeneration = generation
		}
	}
	// The checkpoint is installed first and both files carry the same
	// generation. A crash between renames leaves a mismatched pair that recovery
	// rejects instead of combining geometry from one state with cells from the
	// other.
	if err := writeJSONAtomic(filepath.Join(r.dir, "meta.json"), r.meta); err != nil {
		r.setError(fmt.Errorf("write recording metadata: %w", err))
	}
	r.store.updatePointer(r.key, r.dir, r.meta)
}

func (r *SessionRecorder) setError(err error) {
	if err != nil && r.lastErr == nil {
		r.lastErr = err
		r.meta.HistoryLimited = true
		r.meta.RecordingStopped = true
		slog.Error("terminal recording failed", "session", r.key.String(), "err", err)
	}
}

func (r *SessionRecorder) closeFiles() {
	for _, file := range []*os.File{r.rawFile, r.linesFile, r.idxFile} {
		if file != nil {
			_ = file.Close()
		}
	}
}
