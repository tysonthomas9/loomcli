package events

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// ReadJSONLDir reads the daemon's rotated events-*.jsonl files in
// chronological order. Malformed lines are skipped so one partial record from
// a crashed writer does not hide the remaining runtime trail.
func ReadJSONLDir(dir string) ([]Event, error) {
	if dir == "" {
		return []Event{}, nil
	}
	files, err := filepath.Glob(filepath.Join(dir, "events-*.jsonl*"))
	if err != nil {
		return nil, fmt.Errorf("glob daemon event files: %w", err)
	}
	sort.Strings(files)
	out := make([]Event, 0)
	for _, path := range files {
		file, err := os.Open(filepath.Clean(path)) //nolint:gosec // Paths originate from a controlled directory glob.
		if err != nil {
			return nil, fmt.Errorf("open daemon event file: %w", err)
		}
		reader := bufio.NewReader(file)
		for {
			line, readErr := reader.ReadBytes('\n')
			var event Event
			if err := json.Unmarshal(line, &event); err == nil {
				out = append(out, event)
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				_ = file.Close()
				return nil, fmt.Errorf("read daemon event file: %w", readErr)
			}
		}
		closeErr := file.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("close daemon event file: %w", closeErr)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	return out, nil
}
