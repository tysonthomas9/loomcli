package logstore

import (
	"bufio"
	"io"
	"os"
)

// ReadLastNLines reads the last N lines from the file.
// Returns lines and the starting line number.
func ReadLastNLines(filepath string, n int) ([]string, int64, error) {
	return readLastNLinesFromFile(filepath, n, nil, 0)
}

// readLastNLinesFromFile reads the last N lines using seek-from-end to avoid
// loading the entire file into memory. If secureDir is non-nil,
// uses openLogFileSecure to prevent symlink attacks.
//
// When beforeLine > 0, reads the last N lines that appear before the given
// line number (i.e., lines ending at beforeLine-1). This enables paginated
// backward scrolling through large log files.
func readLastNLinesFromFile(filepath string, n int, secureDir *string, beforeLine int64) ([]string, int64, error) {
	n = clampLineCount(n)

	file, err := openLogFile(filepath, secureDir)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	if stat.Size() == 0 {
		return nil, 1, nil
	}

	effectiveEnd, earlyStart, done, err := resolveEffectiveEnd(file, stat.Size(), beforeLine)
	if done || err != nil {
		return nil, earlyStart, err
	}

	readOffset := backwardScanForOffset(file, effectiveEnd, n)
	startLine, err := countLinesBeforeOffset(file, readOffset)
	if err != nil {
		return nil, 0, err
	}

	lines, err := scanLinesInRange(file, readOffset, effectiveEnd)
	if err != nil {
		return nil, 0, err
	}
	return lines, startLine, nil
}

// clampLineCount ensures n is within [1, LogReadMaxLines], defaulting to LogReadDefaultLines.
func clampLineCount(n int) int {
	if n <= 0 {
		return LogReadDefaultLines
	}
	if n > LogReadMaxLines {
		return LogReadMaxLines
	}
	return n
}

// openLogFile opens the file securely if secureDir is provided, otherwise opens normally.
func openLogFile(filepath string, secureDir *string) (*os.File, error) {
	if secureDir != nil {
		return openLogFileSecure(filepath, *secureDir)
	}
	return os.Open(filepath) //nolint:gosec // G304: path is validated by caller
}

// resolveEffectiveEnd determines the byte offset to use as the end of the scan range.
// If done is true, the caller should return earlyStart immediately.
func resolveEffectiveEnd(file *os.File, fileSize int64, beforeLine int64) (effectiveEnd int64, earlyStart int64, done bool, err error) {
	if beforeLine <= 0 {
		return fileSize, 0, false, nil
	}
	if beforeLine <= 1 {
		return 0, 1, true, nil
	}
	offset, err := findLineByteOffset(file, beforeLine)
	if err != nil {
		return 0, 0, true, err
	}
	if offset < 0 {
		return 0, beforeLine, true, nil
	}
	if offset == 0 {
		return 0, 1, true, nil
	}
	return offset, 0, false, nil
}

// backwardScanForOffset scans backward from effectiveEnd to find the byte offset
// where the last n lines begin. Returns the offset to start reading from.
func backwardScanForOffset(file *os.File, effectiveEnd int64, n int) int64 {
	readOffset := int64(0)
	newlineCount := 0
	buf := make([]byte, readChunkSize)
	pos := effectiveEnd
	firstByte := true

	for pos > 0 && newlineCount < n {
		chunkSize := int64(readChunkSize)
		if chunkSize > pos {
			chunkSize = pos
		}
		pos -= chunkSize

		nRead, err := file.ReadAt(buf[:chunkSize], pos)
		if err != nil && err != io.EOF {
			return 0
		}

		readOffset, newlineCount, firstByte = scanChunkBackward(
			buf[:nRead], pos, effectiveEnd, n,
			readOffset, newlineCount, firstByte,
		)
		if newlineCount >= n {
			break
		}
	}
	return readOffset
}

// scanChunkBackward processes a single chunk in reverse, counting newlines.
func scanChunkBackward(chunk []byte, chunkPos, effectiveEnd int64, target int, readOffset int64, nlCount int, firstByte bool) (int64, int, bool) {
	for i := len(chunk) - 1; i >= 0; i-- {
		if chunk[i] == '\n' {
			if firstByte && chunkPos+int64(i) == effectiveEnd-1 {
				firstByte = false
				continue
			}
			firstByte = false
			nlCount++
			if nlCount == target {
				return chunkPos + int64(i) + 1, nlCount, firstByte
			}
		} else {
			firstByte = false
		}
	}
	return readOffset, nlCount, firstByte
}

// countLinesBeforeOffset counts newlines in [0, readOffset) to compute the 1-based start line.
func countLinesBeforeOffset(file *os.File, readOffset int64) (int64, error) {
	if readOffset == 0 {
		return 1, nil
	}
	buf := make([]byte, readChunkSize)
	linesBeforeOffset := int64(0)
	countPos := int64(0)
	for countPos < readOffset {
		chunkSize := int64(readChunkSize)
		if countPos+chunkSize > readOffset {
			chunkSize = readOffset - countPos
		}
		nRead, err := file.ReadAt(buf[:chunkSize], countPos)
		if err != nil && err != io.EOF {
			return 0, err
		}
		for i := 0; i < nRead; i++ {
			if buf[i] == '\n' {
				linesBeforeOffset++
			}
		}
		countPos += int64(nRead)
	}
	return linesBeforeOffset + 1, nil
}

// scanLinesInRange reads lines from readOffset to effectiveEnd using a Scanner.
func scanLinesInRange(file *os.File, readOffset, effectiveEnd int64) ([]string, error) {
	if _, err := file.Seek(readOffset, io.SeekStart); err != nil {
		return nil, err
	}
	reader := io.LimitReader(file, effectiveEnd-readOffset)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// findLineByteOffset returns the byte offset where the given 1-based line
// number starts in the file. Returns -1 if lineNum exceeds the total number
// of lines in the file.
func findLineByteOffset(file *os.File, lineNum int64) (int64, error) {
	if lineNum <= 1 {
		return 0, nil
	}

	buf := make([]byte, readChunkSize)
	pos := int64(0)
	nlCount := int64(0)
	targetNL := lineNum - 1 // number of newlines before lineNum

	for {
		nRead, err := file.ReadAt(buf, pos)
		if nRead > 0 {
			for i := 0; i < nRead; i++ {
				if buf[i] == '\n' {
					nlCount++
					if nlCount == targetNL {
						return pos + int64(i) + 1, nil
					}
				}
			}
		}
		pos += int64(nRead)
		if err == io.EOF {
			return -1, nil // lineNum exceeds total lines
		}
		if err != nil {
			return 0, err
		}
	}
}
