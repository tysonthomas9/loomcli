package terminal

// Crash recovery for the on-disk recording layout (raw segment, line log,
// offset index, metadata), plus the atomic JSON file helpers those files
// share.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

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
		if os.IsNotExist(err) {
			return RecordingMeta{}, ErrRecordingNotFound
		}
		return RecordingMeta{}, fmt.Errorf("read recording metadata: %w", err)
	}
	return meta, nil
}

func persistRecordingIssueID(dir, issueID string) error {
	meta, err := readRecordingMeta(dir)
	if err != nil {
		return err
	}
	meta.IssueID = issueID
	if err := writeJSONAtomic(filepath.Join(dir, "meta.json"), meta); err != nil {
		return fmt.Errorf("persist recording issue ID: %w", err)
	}
	return nil
}
