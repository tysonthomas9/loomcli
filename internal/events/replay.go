package events

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ReplayFromFile reads a JSONL event file and feeds each event through
// the MetricsStore's handleEvent method for cold-start recovery.
// Malformed lines are skipped. Returns the count of successfully replayed events.
func (ms *MetricsStore) ReplayFromFile(path string) (int, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return 0, fmt.Errorf("open events file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	count := 0
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		ms.handleEvent(event)
		count++
	}
	if err := scanner.Err(); err != nil {
		return count, fmt.Errorf("scan events file: %w", err)
	}
	return count, nil
}
