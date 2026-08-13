package terminal

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
)

const (
	terminalRecordingKeyPrefix = "terminal:recording:"
	MaxHistoryRangeCount       = 1000
	recordingGenerationBytes   = 16
)

var (
	ErrRecordingNotFound = errors.New("terminal recording not found")
	ErrInvalidRecording  = errors.New("invalid terminal recording identifier")
)

// RecordingStore owns durable local terminal recordings and their small
// Redis pointer records. Payload bytes never enter Redis.
type RecordingStore struct {
	root  string
	redis *redis.Client

	mu          sync.Mutex
	active      map[SessionKey]*SessionRecorder
	starting    map[SessionKey]*recordingStartLease
	recovering  map[recordingRecoveryKey]*recordingRecoveryLease
	queueSize   int
	closed      bool
	onStarted   func(SessionKey, string, RecordingMeta)
	onCompleted func(SessionKey, string, RecordingMeta)
}

type recordingStartLease struct {
	done chan struct{}
}

type recordingRecoveryKey struct {
	key        SessionKey
	generation string
}

type recordingRecoveryLease struct {
	done     chan struct{}
	snapshot recorderSnapshot
	err      error
}

// currentRecordingPointer is the durable no-payload indirection from a reused
// terminal name to its newest PTY lifetime. On disk, every lifetime is stored
// at:
//
//	<root>/<workspace>/<session>/generations/<32-lowercase-hex>/
//
// Each directory contains one independent LOOMTRM\x01 raw segment and its
// derived files. The opaque 128-bit generation survives name reuse without
// relying on a process-local counter or wall-clock uniqueness. Within that
// directory, RecordingMeta.CheckpointGeneration remains the independent
// monotonic counter pairing meta.json with indexer.json during atomic updates.
type currentRecordingPointer struct {
	FormatVersion uint8  `json:"formatVersion"`
	Generation    string `json:"generation"`
}

// SetLifecycleHooks connects durable recordings to higher-level audit
// metadata. Hooks run asynchronously, never on the PTY drain path or recorder
// worker.
// The path passed to both hooks is the guarded recording directory.
func (s *RecordingStore) SetLifecycleHooks(
	onStarted func(SessionKey, string, RecordingMeta),
	onCompleted func(SessionKey, string, RecordingMeta),
) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.onStarted = onStarted
	s.onCompleted = onCompleted
	s.mu.Unlock()
}

func NewRecordingStore(root string, redisClient *redis.Client) *RecordingStore {
	return &RecordingStore{
		root: root, redis: redisClient,
		active:     make(map[SessionKey]*SessionRecorder),
		starting:   make(map[SessionKey]*recordingStartLease),
		recovering: make(map[recordingRecoveryKey]*recordingRecoveryLease),
		queueSize:  defaultRecordingQueue,
	}
}

// DefaultRecordingRoot returns <loom dir>/session-recordings.
//
// Resolved through bootstrap.LoomDir rather than $HOME directly so that
// recordings honor LOOM_CONFIG_DIR like every other piece of loom state,
// and so the "tests must NEVER touch the real ~/.loom" guard in LoomDir
// also covers an unbounded writer.
func DefaultRecordingRoot() (string, error) {
	dir := bootstrap.LoomDir()
	if dir == "" {
		return "", fmt.Errorf("resolve loom directory")
	}
	return filepath.Join(dir, "session-recordings"), nil
}

// SetQueueSizeForTest changes the bounded writer queue for deterministic
// backpressure tests. It must be called before StartRecording.
func (s *RecordingStore) SetQueueSizeForTest(size int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queueSize = size
}

// StartRecording decides whether to resume the current generation or open a
// new one, which means reconciling the current.json pointer, the directory on
// disk, and any recorder still live for this key. The branches are that
// reconciliation.
//
//nolint:gocognit,funlen // the branches are the generation reconciliation
func (s *RecordingStore) StartRecording(key SessionKey, cols, rows uint16) (*SessionRecorder, error) {
	if s == nil {
		return nil, errors.New("recording store unavailable")
	}
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, errors.New("recording store closed")
		}
		if current := s.active[key]; current != nil {
			if current.closed.Load() {
				done := current.done
				s.mu.Unlock()
				<-done
				continue
			}
			s.mu.Unlock()
			return current, nil
		}
		if lease := s.starting[key]; lease != nil {
			done := lease.done
			s.mu.Unlock()
			<-done
			continue
		}
		lease := &recordingStartLease{done: make(chan struct{})}
		s.starting[key] = lease
		queueSize := s.queueSize
		s.mu.Unlock()

		recorder, err := s.startRecording(key, cols, rows, queueSize)

		s.mu.Lock()
		if s.starting[key] == lease {
			delete(s.starting, key)
		}
		if err == nil && !s.closed {
			recorder.prepareLifecycle()
			s.active[key] = recorder
		}
		closed := s.closed
		close(lease.done)
		s.mu.Unlock()

		if err != nil {
			return nil, err
		}
		if closed {
			_ = recorder.Close()
			return nil, errors.New("recording store closed")
		}
		// The started hook is deliberately NOT fired here: the recorder
		// fires it once the recording proves non-trivial (first committed
		// line, or outliving recordingStartedGrace), so stillborn sessions
		// never appear in the session-history store.
		return recorder, nil
	}
}

// discardGeneration removes a trivial recording's directory and, when
// current.json still points at it, the pointer file, so crash-looping spawns
// leave nothing on disk. Called from the recorder worker on close.
func (s *RecordingStore) discardGeneration(key SessionKey, generation, dir string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if current, err := s.readCurrentGeneration(key); err == nil && current == generation {
		_ = os.Remove(s.currentPointerPath(key))
	}
	s.mu.Unlock()
	_ = os.RemoveAll(dir)
}

func (s *RecordingStore) startRecording(key SessionKey, cols, rows uint16, queueSize int) (*SessionRecorder, error) {
	previousGeneration, pointerErr := s.readCurrentGeneration(key)
	var previousMeta RecordingMeta
	if pointerErr == nil {
		previousDir, err := s.generationDir(key, previousGeneration)
		if err != nil {
			return nil, err
		}
		previousMeta, err = readRecordingMeta(previousDir)
		if err != nil || !previousMeta.Closed {
			recovered, recoverErr := s.recoverGeneration(context.Background(), key, previousGeneration, previousDir)
			if recoverErr != nil {
				if quarantineErr := s.quarantineGeneration(previousDir); quarantineErr != nil {
					return nil, errors.Join(recoverErr, quarantineErr)
				}
				slog.Warn("quarantined unrecoverable terminal recording", "session", key.String(), "generation", previousGeneration, "err", recoverErr)
				previousMeta = RecordingMeta{}
			} else {
				previousMeta = recovered.meta
			}
		}
	} else if !errors.Is(pointerErr, ErrRecordingNotFound) {
		return nil, pointerErr
	}

	generation, dir, err := s.allocateGenerationDir(key)
	if err != nil {
		return nil, err
	}
	startedAt := unixMilliNow()
	if previousMeta.StartedAt >= startedAt {
		startedAt = previousMeta.StartedAt + 1
	}
	recorder, err := newSessionRecorder(s, key, dir, generation, startedAt, cols, rows, queueSize)
	if err != nil {
		return nil, err
	}
	pointer := currentRecordingPointer{FormatVersion: recordingFormatVersion, Generation: generation}
	if err := writeJSONAtomic(s.currentPointerPath(key), pointer); err != nil {
		_ = recorder.Close()
		return nil, fmt.Errorf("write current recording generation: %w", err)
	}
	return recorder, nil
}

func (s *RecordingStore) ActiveLineCount(key SessionKey) uint64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	recorder := s.active[key]
	s.mu.Unlock()
	if recorder == nil {
		meta, err := readRecordingMetaForKey(s, key)
		if err != nil {
			return 0
		}
		return meta.LineCount
	}
	snapshot := recorder.snapshot()
	return snapshot.meta.LineCount
}

func (s *RecordingStore) History(ctx context.Context, key SessionKey, from uint64, count int) (RecordingHistory, error) {
	return s.HistoryGeneration(ctx, key, "", from, count)
}

// HistoryGeneration reads one bounded range from the requested PTY lifetime.
// An empty generation resolves the current pointer for internal callers; HTTP
// range requests always pass the explicit generation learned from Meta.
func clampHistoryRangeCount(count int) int {
	if count <= 0 {
		return 1
	}
	if count > MaxHistoryRangeCount {
		return MaxHistoryRangeCount
	}
	return count
}

func (s *RecordingStore) HistoryGeneration(ctx context.Context, key SessionKey, generation string, from uint64, count int) (RecordingHistory, error) {
	count = clampHistoryRangeCount(count)
	snapshot, err := s.recordingSnapshot(ctx, key, generation)
	if err != nil {
		return RecordingHistory{}, err
	}
	committed := snapshot.meta.LineCount
	durable := minUint64(snapshot.durableLineCount, committed)
	total := committed + uint64(len(snapshot.screen))
	if snapshot.meta.Closed {
		total = committed
	}
	if from > total {
		from = total
	}
	end := minUint64(total, from+uint64(count))
	lines := make([]RecordingLine, 0, end-from)
	if from < durable {
		durableEnd := minUint64(end, durable)
		diskLines, readErr := readIndexedRecordingLines(snapshot.dir, from, durableEnd, durable)
		if readErr != nil {
			return RecordingHistory{}, readErr
		}
		lines = append(lines, diskLines...)
	}
	if end > durable && from < committed {
		pendingStart := maxUint64(from, durable) - durable
		pendingEnd := minUint64(end, committed) - durable
		if pendingEnd > uint64(len(snapshot.pendingLines)) {
			return RecordingHistory{}, fmt.Errorf("recording pending line range short read: got %d want %d", len(snapshot.pendingLines), pendingEnd)
		}
		lines = append(lines, snapshot.pendingLines[pendingStart:pendingEnd]...)
	}
	if end > committed && !snapshot.meta.Closed {
		screenStart := maxUint64(from, committed) - committed
		screenEnd := end - committed
		lines = append(lines, snapshot.screen[screenStart:screenEnd]...)
	}
	// Raw byte offsets remain an internal coordinate. The API's line model is
	// intentionally index/timestamp/runs only.
	for i := range lines {
		lines[i].Offset = ""
	}
	return RecordingHistory{
		Generation: snapshot.meta.Generation,
		Lines:      lines, TotalLines: total, FirstScreenLine: committed,
		UpToDate: end >= total, Closed: snapshot.meta.Closed,
		Cols: snapshot.meta.Cols, Immutable: end <= committed,
	}, nil
}

func (s *RecordingStore) Meta(ctx context.Context, key SessionKey) (RecordingMeta, uint64, uint64, error) {
	snapshot, err := s.recordingSnapshot(ctx, key, "")
	if err != nil {
		return RecordingMeta{}, 0, 0, err
	}
	firstScreen := snapshot.meta.LineCount
	total := firstScreen + uint64(len(snapshot.screen))
	if snapshot.meta.Closed {
		total = firstScreen
	}
	return snapshot.meta, total, firstScreen, nil
}

func (s *RecordingStore) OpenRaw(ctx context.Context, key SessionKey) (*os.File, RecordingMeta, error) {
	snapshot, err := s.recordingSnapshot(ctx, key, "")
	if err != nil {
		return nil, RecordingMeta{}, err
	}
	dir := snapshot.dir
	path := filepath.Join(dir, "raw.seg")
	if !pathWithin(s.root, path) {
		return nil, RecordingMeta{}, ErrInvalidRecording
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, RecordingMeta{}, ErrRecordingNotFound
		}
		return nil, RecordingMeta{}, fmt.Errorf("open raw recording: %w", err)
	}
	meta, err := readRecordingMeta(dir)
	if err != nil {
		_ = file.Close()
		return nil, RecordingMeta{}, err
	}
	return file, meta, nil
}

// recordingSnapshot resolves a consistent view across the live recorder and
// the on-disk generation. The ordering of the reads is what makes the snapshot
// coherent, so the steps have to stay in one place.
//
//nolint:funlen // the read ordering is what makes the snapshot coherent
func (s *RecordingStore) recordingSnapshot(ctx context.Context, key SessionKey, generation string) (recorderSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return recorderSnapshot{}, err
	}
	s.mu.Lock()
	if current := s.active[key]; current != nil && (generation == "" || current.meta.Generation == generation) {
		s.mu.Unlock()
		snapshot := current.snapshot()
		return snapshot, snapshot.err
	}
	if s.closed {
		s.mu.Unlock()
		return recorderSnapshot{}, errors.New("recording store closed")
	}
	s.mu.Unlock()

	if generation == "" {
		var err error
		generation, err = s.readCurrentGeneration(key)
		if err != nil {
			return recorderSnapshot{}, err
		}
	}
	dir, err := s.generationDir(key, generation)
	if err != nil {
		return recorderSnapshot{}, err
	}
	if _, statErr := os.Stat(filepath.Join(dir, "raw.seg")); statErr != nil {
		if os.IsNotExist(statErr) {
			return recorderSnapshot{}, ErrRecordingNotFound
		}
		return recorderSnapshot{}, fmt.Errorf("stat terminal recording: %w", statErr)
	}
	meta, metaErr := readRecordingMeta(dir)
	if metaErr == nil && meta.Closed {
		return recorderSnapshot{meta: meta, durableLineCount: meta.LineCount, dir: dir}, nil
	}
	if metaErr != nil && !errors.Is(metaErr, ErrRecordingNotFound) {
		return recorderSnapshot{}, metaErr
	}

	// No live PTY owns this recording. Recover derived state from the raw tail
	// and finalize the last screen so a post-restart read has a final row count.
	return s.recoverGeneration(ctx, key, generation, dir)
}

func (s *RecordingStore) recoverGeneration(ctx context.Context, key SessionKey, generation, dir string) (recorderSnapshot, error) {
	recoveryKey := recordingRecoveryKey{key: key, generation: generation}
	s.mu.Lock()
	if lease := s.recovering[recoveryKey]; lease != nil {
		done := lease.done
		s.mu.Unlock()
		select {
		case <-done:
			return lease.snapshot, lease.err
		case <-ctx.Done():
			return recorderSnapshot{}, ctx.Err()
		}
	}
	lease := &recordingRecoveryLease{done: make(chan struct{})}
	s.recovering[recoveryKey] = lease
	queueSize := s.queueSize
	s.mu.Unlock()

	recorder, err := newSessionRecorder(s, key, dir, generation, 0, 80, 24, queueSize)
	if err == nil {
		err = recorder.Close()
	}
	if err == nil {
		var meta RecordingMeta
		meta, err = readRecordingMeta(dir)
		if err == nil {
			lease.snapshot = recorderSnapshot{meta: meta, durableLineCount: meta.LineCount, dir: dir}
		}
	}
	lease.err = err

	s.mu.Lock()
	delete(s.recovering, recoveryKey)
	close(lease.done)
	s.mu.Unlock()
	return lease.snapshot, lease.err
}

func (s *RecordingStore) quarantineGeneration(dir string) error {
	for attempt := range 8 {
		quarantine := fmt.Sprintf("%s.quarantine-%d-%d", dir, time.Now().UnixNano(), attempt)
		if err := os.Rename(dir, quarantine); err == nil {
			return nil
		} else if !os.IsExist(err) {
			return fmt.Errorf("quarantine terminal recording: %w", err)
		}
	}
	return errors.New("allocate terminal recording quarantine path")
}

func (s *RecordingStore) removeActive(key SessionKey, recorder *SessionRecorder) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.active[key] == recorder {
		delete(s.active, key)
	}
	s.mu.Unlock()
}

func (s *RecordingStore) recordingCompleted(key SessionKey, dir string, meta RecordingMeta) {
	if s == nil {
		return
	}
	s.mu.Lock()
	onCompleted := s.onCompleted
	s.mu.Unlock()
	if onCompleted != nil {
		onCompleted(key, dir, meta)
	}
}

func (s *RecordingStore) recordingStarted(key SessionKey, dir string, meta RecordingMeta) {
	if s == nil {
		return
	}
	s.mu.Lock()
	onStarted := s.onStarted
	s.mu.Unlock()
	if onStarted != nil {
		onStarted(key, dir, meta)
	}
}

// SetIssueID durably associates a recording generation with the issue whose
// terminal tab launched it. The lifecycle hook calls this off the recorder
// worker so Redis lookup latency never blocks PTY output.
func (s *RecordingStore) SetIssueID(key SessionKey, generation, issueID string) error {
	if s == nil || issueID == "" {
		return nil
	}
	s.mu.Lock()
	recorder := s.active[key]
	if recorder != nil && recorder.startMeta.Generation != generation {
		recorder = nil
	}
	s.mu.Unlock()
	if recorder != nil {
		return recorder.setIssueID(issueID)
	}
	dir, err := s.generationDir(key, generation)
	if err != nil {
		return err
	}
	return persistRecordingIssueID(dir, issueID)
}

func (s *RecordingStore) updatePointer(key SessionKey, dir string, meta RecordingMeta) {
	if s == nil || s.redis == nil {
		return
	}
	pointer := RecordingPointer{
		Dir: dir, Generation: meta.Generation, LineCount: meta.LineCount, RawLen: meta.RawLen,
		StartedAt: meta.StartedAt, UpdatedAt: unixMilliNow(),
	}
	data, err := json.Marshal(pointer)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = s.redis.Set(ctx, recordingRedisKey(key), data, 0).Err()
}

// ClosedRecording identifies a finalized generation discovered during the
// startup reconciliation sweep.
type ClosedRecording struct {
	Key  SessionKey
	Dir  string
	Meta RecordingMeta
}

// ClosedRecordings returns valid finalized generations without mutating them.
func (s *RecordingStore) ClosedRecordings() ([]ClosedRecording, error) {
	if s == nil {
		return nil, nil
	}
	var recordings []ClosedRecording
	err := filepath.WalkDir(s.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "meta.json" {
			return nil
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 5 || parts[2] != "generations" || !validRecordingGeneration(parts[3]) {
			return nil
		}
		meta, err := readRecordingMeta(filepath.Dir(path))
		if err != nil || !meta.Closed || meta.Generation != parts[3] || meta.IssueID == "" {
			return nil
		}
		recordings = append(recordings, ClosedRecording{
			Key: SessionKey{Workspace: parts[0], Name: parts[1]},
			Dir: filepath.Dir(path), Meta: meta,
		})
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	return recordings, err
}

func (s *RecordingStore) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	recorders := make([]*SessionRecorder, 0, len(s.active))
	for _, recorder := range s.active {
		recorders = append(recorders, recorder)
	}
	s.mu.Unlock()
	var errs []error
	for _, recorder := range recorders {
		if err := recorder.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *RecordingStore) sessionRootDir(key SessionKey) (string, error) {
	if s.root == "" || !validRecordingComponent(key.Workspace) || !validRecordingComponent(key.Name) {
		return "", ErrInvalidRecording
	}
	dir := filepath.Join(s.root, key.Workspace, key.Name)
	if !pathWithin(s.root, dir) {
		return "", ErrInvalidRecording
	}
	return dir, nil
}

func (s *RecordingStore) generationDir(key SessionKey, generation string) (string, error) {
	if !validRecordingGeneration(generation) {
		return "", ErrInvalidRecording
	}
	root, err := s.sessionRootDir(key)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "generations", generation)
	if !pathWithin(s.root, dir) {
		return "", ErrInvalidRecording
	}
	return dir, nil
}

func (s *RecordingStore) sessionDir(key SessionKey) (string, error) {
	generation, err := s.readCurrentGeneration(key)
	if err != nil {
		return "", err
	}
	return s.generationDir(key, generation)
}

func (s *RecordingStore) mustSessionDir(key SessionKey) string {
	dir, _ := s.sessionDir(key)
	return dir
}

func (s *RecordingStore) currentPointerPath(key SessionKey) string {
	root, _ := s.sessionRootDir(key)
	return filepath.Join(root, "current.json")
}

func (s *RecordingStore) readCurrentGeneration(key SessionKey) (string, error) {
	root, err := s.sessionRootDir(key)
	if err != nil {
		return "", err
	}
	var pointer currentRecordingPointer
	if err := readJSONFile(filepath.Join(root, "current.json"), &pointer); err != nil {
		if os.IsNotExist(err) {
			return "", ErrRecordingNotFound
		}
		return "", fmt.Errorf("read current recording generation: %w", err)
	}
	if pointer.FormatVersion != recordingFormatVersion || !validRecordingGeneration(pointer.Generation) {
		return "", ErrInvalidRecording
	}
	return pointer.Generation, nil
}

func (s *RecordingStore) allocateGenerationDir(key SessionKey) (string, string, error) {
	root, err := s.sessionRootDir(key)
	if err != nil {
		return "", "", err
	}
	generationsRoot := filepath.Join(root, "generations")
	if err := os.MkdirAll(generationsRoot, 0o700); err != nil {
		return "", "", fmt.Errorf("create recording generations directory: %w", err)
	}
	for range 8 {
		generation, err := newRecordingGeneration()
		if err != nil {
			return "", "", err
		}
		dir := filepath.Join(generationsRoot, generation)
		if err := os.Mkdir(dir, 0o700); err == nil {
			return generation, dir, nil
		} else if !os.IsExist(err) {
			return "", "", fmt.Errorf("create recording generation directory: %w", err)
		}
	}
	return "", "", errors.New("allocate unique recording generation")
}

func newRecordingGeneration() (string, error) {
	raw := make([]byte, recordingGenerationBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate recording identity: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func validRecordingGeneration(generation string) bool {
	if len(generation) != recordingGenerationBytes*2 || generation != strings.ToLower(generation) {
		return false
	}
	decoded, err := hex.DecodeString(generation)
	return err == nil && len(decoded) == recordingGenerationBytes
}

// validRecordingComponent reports whether value is safe to use as a single
// path segment under the recording root.
//
// The rejected set must be written as an interpreted string. In a raw literal
// the escapes are not escapes: `/\\\x00` is the seven bytes / \ \ \ x 0 0, so
// the forbidden set silently became {/, \, x, 0} and every session or
// workspace whose name contained an "x" or a "0" -- lead-codex-1, for one --
// was refused its own history, while the intended NUL guard did not exist.
func validRecordingComponent(value string) bool {
	return value != "" && value != "." && value != ".." &&
		!strings.ContainsAny(value, "/\\\x00")
}

func pathWithin(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func recordingRedisKey(key SessionKey) string {
	return terminalRecordingKeyPrefix + key.Workspace + ":" + key.Name
}

func readRecordingMetaForKey(store *RecordingStore, key SessionKey) (RecordingMeta, error) {
	dir, err := store.sessionDir(key)
	if err != nil {
		return RecordingMeta{}, err
	}
	return readRecordingMeta(dir)
}

// readIndexedRecordingLines seeks the index, reads the byte range it points
// at, and decodes it. The three steps share the offsets they compute.
//
//nolint:funlen // three steps sharing computed offsets
func readIndexedRecordingLines(dir string, from, end, lineCount uint64) ([]RecordingLine, error) {
	if end <= from {
		return []RecordingLine{}, nil
	}
	idx, err := os.Open(filepath.Join(dir, "lines.idx"))
	if err != nil {
		return nil, fmt.Errorf("open recording line index: %w", err)
	}
	defer idx.Close()
	startOffset, err := readIndexOffset(idx, from)
	if err != nil {
		return nil, err
	}
	var endOffset uint64
	if end < lineCount {
		endOffset, err = readIndexOffset(idx, end)
		if err != nil {
			return nil, err
		}
	} else {
		info, statErr := os.Stat(filepath.Join(dir, "lines.jsonl"))
		if statErr != nil {
			return nil, fmt.Errorf("stat recording line log: %w", statErr)
		}
		endOffset = uint64(info.Size())
	}

	linesFile, err := os.Open(filepath.Join(dir, "lines.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("open recording line log: %w", err)
	}
	defer linesFile.Close()
	if _, err := linesFile.Seek(int64(startOffset), io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek recording line log: %w", err)
	}
	reader := bufio.NewReader(io.LimitReader(linesFile, int64(endOffset-startOffset)))
	result := make([]RecordingLine, 0, end-from)
	for uint64(len(result)) < end-from {
		data, readErr := reader.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, fmt.Errorf("read recording line: %w", readErr)
		}
		if len(data) == 0 {
			break
		}
		var line RecordingLine
		if err := json.Unmarshal(bytesTrimNewline(data), &line); err != nil {
			return nil, fmt.Errorf("decode recording line: %w", err)
		}
		result = append(result, line)
	}
	if uint64(len(result)) != end-from {
		return nil, fmt.Errorf("recording line range short read: got %d want %d", len(result), end-from)
	}
	return result, nil
}

func readIndexOffset(file *os.File, index uint64) (uint64, error) {
	var raw [8]byte
	if _, err := file.ReadAt(raw[:], int64(index*8)); err != nil {
		return 0, fmt.Errorf("read recording line index %d: %w", index, err)
	}
	return binary.BigEndian.Uint64(raw[:]), nil
}

func bytesTrimNewline(data []byte) []byte {
	if len(data) > 0 && data[len(data)-1] == '\n' {
		return data[:len(data)-1]
	}
	return data
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
