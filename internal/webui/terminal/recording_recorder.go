package terminal

import (
	"bufio"
	"bytes"
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

type recordingEventKind uint8

const (
	recordingOutput      recordingEventKind = 1
	recordingResizeEvent recordingEventKind = 2
)

type recordingEvent struct {
	kind      recordingEventKind
	payload   []byte
	timestamp int64
	cols      uint16
	rows      uint16
}

type recorderSnapshot struct {
	meta   RecordingMeta
	screen []RecordingLine
	dir    string
	err    error
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
	query  chan chan recorderSnapshot
	stop   chan struct{}
	done   chan struct{}

	closed        atomic.Bool
	droppedChunks atomic.Uint64
	unsafeGap     atomic.Bool
	closeOnce     sync.Once
	enqueueMu     sync.Mutex
	beforeEnqueue func() // deterministic Append/Close interleaving test hook

	rawFile   *os.File
	rawBuf    *bufio.Writer
	linesFile *os.File
	linesBuf  *bufio.Writer
	idxFile   *os.File
	idxBuf    *bufio.Writer

	rawLen       uint64
	linesLen     uint64
	meta         RecordingMeta
	emulator     *recordingEmulator
	geometrySeen bool
	lastErr      error
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
		query:  make(chan chan recorderSnapshot),
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
		linesLen: linesLen,
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
	r.unsafeGap.Store(r.meta.Gaps > 0)

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
	case r.events <- recordingEvent{kind: recordingOutput, payload: chunk, timestamp: unixMilliNow()}:
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
	case r.events <- recordingEvent{kind: recordingResizeEvent, timestamp: unixMilliNow(), cols: cols, rows: rows}:
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
		return recorderSnapshot{meta: meta, dir: r.dir, err: err}
	}
	response := make(chan recorderSnapshot, 1)
	select {
	case r.query <- response:
		return <-response
	case <-r.done:
		meta, err := readRecordingMeta(r.dir)
		return recorderSnapshot{meta: meta, dir: r.dir, err: err}
	}
}

// FirstScreenLine returns the immutable line count after synchronizing the
// recorder worker. Attach uses this once before replaying the live screen.
func (r *SessionRecorder) FirstScreenLine() uint64 {
	return r.snapshot().meta.LineCount
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

func (r *SessionRecorder) run() {
	ticker := time.NewTicker(recordingFlushInterval)
	defer ticker.Stop()
	defer close(r.done)
	for {
		select {
		case event := <-r.events:
			r.applyEvent(event)
		case response := <-r.query:
			r.drainPendingEvents()
			r.flush(false)
			response <- r.currentSnapshot()
		case <-ticker.C:
			r.flush(false)
		case <-r.stop:
			for {
				select {
				case event := <-r.events:
					r.applyEvent(event)
				default:
					r.finalize()
					r.store.removeActive(r.key, r)
					r.store.recordingCompleted(r.key, r.dir, r.meta)
					return
				}
			}
		}
	}
}

func (r *SessionRecorder) drainPendingEvents() {
	for {
		select {
		case event := <-r.events:
			r.applyEvent(event)
		default:
			return
		}
	}
}

func (r *SessionRecorder) applyEvent(event recordingEvent) {
	if r.lastErr != nil {
		return
	}
	if r.unsafeGap.Load() {
		r.emulator.CommitBlocked = true
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
	if r.unsafeGap.Load() {
		r.emulator.CommitBlocked = true
		return nil
	}
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
	return nil
}

func (r *SessionRecorder) noteDroppedEvent() {
	r.droppedChunks.Add(1)
	r.unsafeGap.Store(true)
}

func (r *SessionRecorder) currentSnapshot() recorderSnapshot {
	meta := r.meta
	meta.AltScreen = meta.AltScreen || r.emulator.EverAlt
	meta.Gaps = r.droppedChunks.Load()
	meta.UnhandledSequences = r.emulator.unhandledSequenceSummary()
	meta.HistoryLimited = r.emulator.CommitBlocked
	screen := r.emulator.screenRows()
	for i := range screen {
		screen[i].Index = meta.LineCount + uint64(i)
	}
	return recorderSnapshot{meta: meta, screen: screen, dir: r.dir, err: r.lastErr}
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
	r.meta.UnhandledSequences = r.emulator.unhandledSequenceSummary()
	r.meta.HistoryLimited = r.emulator.CommitBlocked
	for _, writer := range []*bufio.Writer{r.rawBuf, r.linesBuf, r.idxBuf} {
		if writer != nil {
			if err := writer.Flush(); err != nil {
				r.setError(fmt.Errorf("flush recording: %w", err))
			}
		}
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

// recoverRawSegment walks the raw segment frame by frame looking for the last
// intact one. Every branch is a distinct way a crash can tear a frame.
//
//nolint:gocognit,cyclop,funlen // one branch per way a frame can be torn
func recoverRawSegment(path string) (uint64, bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return 0, false, fmt.Errorf("open raw segment for recovery: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, false, fmt.Errorf("stat raw segment for recovery: %w", err)
	}
	if info.Size() == 0 {
		if _, err := f.Write(recordingFileHeader[:]); err != nil {
			return 0, false, fmt.Errorf("write raw format header: %w", err)
		}
		if err := f.Sync(); err != nil {
			return 0, false, fmt.Errorf("sync raw format header: %w", err)
		}
		return recordingFileHeaderSize, true, nil
	}
	var fileHeader [recordingFileHeaderSize]byte
	if _, err := io.ReadFull(f, fileHeader[:]); err != nil {
		return 0, false, fmt.Errorf("read raw format header: %w", err)
	}
	if !bytes.Equal(fileHeader[:], recordingFileHeader[:]) {
		return 0, false, fmt.Errorf("unsupported terminal recording raw format header %x", fileHeader)
	}
	offset := uint64(recordingFileHeaderSize)
	frameCount := 0
	header := make([]byte, recordingFrameHeaderSize)
	for {
		n, readErr := io.ReadFull(f, header)
		if errors.Is(readErr, io.EOF) && n == 0 {
			break
		}
		if readErr != nil {
			if err := f.Truncate(int64(offset)); err != nil {
				return 0, false, fmt.Errorf("truncate torn raw header: %w", err)
			}
			break
		}
		kind := recordingEventKind(header[0])
		length := binary.BigEndian.Uint32(header[1:5])
		valid := (kind == recordingOutput && length <= maxRecordingFrameSize) ||
			(kind == recordingResizeEvent && length == 4)
		if !valid || (frameCount == 0 && kind != recordingResizeEvent) {
			if err := f.Truncate(int64(offset)); err != nil {
				return 0, false, fmt.Errorf("truncate invalid raw event: %w", err)
			}
			break
		}
		if _, err := io.CopyN(io.Discard, f, int64(length)); err != nil {
			if truncateErr := f.Truncate(int64(offset)); truncateErr != nil {
				return 0, false, fmt.Errorf("truncate torn raw payload: %w", truncateErr)
			}
			break
		}
		offset += recordingFrameHeaderSize + uint64(length)
		frameCount++
	}
	return offset, frameCount == 0, nil
}

// recoverLineIndex truncates the lines file and its offset index back to a
// consistent pair after a crash, which reads as a single sequence.
//
//nolint:funlen // one recovery sequence over a file pair
func recoverLineIndex(linesPath, idxPath string) (uint64, uint64, error) {
	lines, err := os.OpenFile(linesPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return 0, 0, fmt.Errorf("open line log for recovery: %w", err)
	}
	defer lines.Close()
	reader := bufio.NewReader(lines)
	var offsets []uint64
	var offset uint64
	for {
		start := offset
		data, readErr := reader.ReadBytes('\n')
		if len(data) == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil || len(data) == 0 || data[len(data)-1] != '\n' {
			if err := lines.Truncate(int64(start)); err != nil {
				return 0, 0, fmt.Errorf("truncate torn line record: %w", err)
			}
			offset = start
			break
		}
		var line RecordingLine
		if err := json.Unmarshal(bytes.TrimSuffix(data, []byte{'\n'}), &line); err != nil || line.Index != uint64(len(offsets)) {
			if truncateErr := lines.Truncate(int64(start)); truncateErr != nil {
				return 0, 0, fmt.Errorf("truncate invalid line record: %w", truncateErr)
			}
			offset = start
			break
		}
		offsets = append(offsets, start)
		offset += uint64(len(data))
	}

	idx, err := os.OpenFile(idxPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, 0, fmt.Errorf("rebuild line index: %w", err)
	}
	writer := bufio.NewWriter(idx)
	var raw [8]byte
	for _, lineOffset := range offsets {
		binary.BigEndian.PutUint64(raw[:], lineOffset)
		if _, err := writer.Write(raw[:]); err != nil {
			_ = idx.Close()
			return 0, 0, fmt.Errorf("write rebuilt line index: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		_ = idx.Close()
		return 0, 0, fmt.Errorf("flush rebuilt line index: %w", err)
	}
	if err := idx.Close(); err != nil {
		return 0, 0, fmt.Errorf("close rebuilt line index: %w", err)
	}
	return uint64(len(offsets)), offset, nil
}

func readJSONFile(path string, destination any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, destination)
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func readRecordingMeta(dir string) (RecordingMeta, error) {
	var meta RecordingMeta
	err := readJSONFile(filepath.Join(dir, "meta.json"), &meta)
	if err != nil {
		return RecordingMeta{}, fmt.Errorf("read recording metadata: %w", err)
	}
	return meta, nil
}
