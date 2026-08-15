package events

import (
	"bufio"
	"encoding/json"
	"fmt"
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
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			var event Event
			if err := json.Unmarshal(scanner.Bytes(), &event); err == nil {
				out = append(out, event)
			}
		}
		scanErr := scanner.Err()
		closeErr := file.Close()
		if scanErr != nil {
			return nil, fmt.Errorf("read daemon event file: %w", scanErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close daemon event file: %w", closeErr)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	return out, nil
}
