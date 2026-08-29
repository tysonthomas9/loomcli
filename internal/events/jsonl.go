package events

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// EventReadOpts configures filtering and pagination for JSONL event reads.
type EventReadOpts struct {
	Page    int
	PerPage int
	Type    string
	Agent   string
	Since   string
}

// ReadEventsFromJSONL reads host-local event files, applies filters, and
// returns events newest first. It is shared by serve observability and other
// server-side consumers so JSONL discovery and malformed-line handling do not
// drift between endpoints.
func ReadEventsFromJSONL(dir string, opts EventReadOpts) ([]Event, int, error) {
	if dir == "" {
		return []Event{}, 0, nil
	}

	files, err := globEventFiles(dir)
	if err != nil {
		return nil, 0, err
	}
	if len(files) == 0 {
		return []Event{}, 0, nil
	}

	all := collectFilteredEvents(files, opts)
	sort.Slice(all, func(i, j int) bool {
		return all[i].Timestamp.After(all[j].Timestamp)
	})

	return paginateEvents(all, opts.Page, opts.PerPage), len(all), nil
}

func globEventFiles(dir string) ([]string, error) {
	pattern := filepath.Join(dir, "events-*.jsonl")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("globbing events files: %w", err)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	return files, nil
}

func collectFilteredEvents(files []string, opts EventReadOpts) []Event {
	var sinceTime time.Time
	if opts.Since != "" {
		sinceTime, _ = time.Parse(time.RFC3339, opts.Since)
	}

	var all []Event
	for _, file := range files {
		fileEvents, err := ReadJSONLFile(file)
		if err != nil {
			log.Printf("events: skipping %s: %v", filepath.Base(file), err)
			continue
		}
		for i := range fileEvents {
			event := &fileEvents[i]
			if opts.Type != "" && string(event.Type) != opts.Type {
				continue
			}
			if opts.Agent != "" && event.Agent != opts.Agent {
				continue
			}
			if !sinceTime.IsZero() && event.Timestamp.Before(sinceTime) {
				continue
			}
			all = append(all, *event)
		}
	}
	return all
}

func paginateEvents(all []Event, page, perPage int) []Event {
	total := len(all)
	start := (page - 1) * perPage
	if start >= total {
		return []Event{}
	}
	end := start + perPage
	if end > total {
		end = total
	}
	return all[start:end]
}

// ReadJSONLFile reads all valid events from one JSONL file. Malformed lines
// are skipped so one partial write does not hide the rest of the local trace.
func ReadJSONLFile(path string) ([]Event, error) {
	// #nosec G304 -- callers pass paths found beneath the configured events dir.
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var result []Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		result = append(result, event)
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	return result, nil
}
