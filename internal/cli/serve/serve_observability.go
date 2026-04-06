package serve

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// ObservabilityMetricsResponse wraps the metrics snapshot for the API.
type ObservabilityMetricsResponse struct {
	Success bool                    `json:"success"`
	Data    *events.MetricsSnapshot `json:"data,omitempty"`
	Error   string                  `json:"error,omitempty"`
}

// ObservabilityEventsResponse wraps paginated events for the API.
type ObservabilityEventsResponse struct {
	Success bool           `json:"success"`
	Data    []events.Event `json:"data"`
	Total   int            `json:"total"`
	Page    int            `json:"page"`
	PerPage int            `json:"per_page"`
	Error   string         `json:"error,omitempty"`
}

// EventReadOpts configures filtering and pagination for event reads.
type EventReadOpts struct {
	Page    int
	PerPage int
	Type    string
	Agent   string
	Since   string
}

// newMetricsCache creates a TTL cache for observability metrics snapshots.
func newMetricsCache(eventsDir string) *cachedValue[*events.MetricsSnapshot] {
	return newCachedValue[*events.MetricsSnapshot](30*time.Second, func() *events.MetricsSnapshot {
		store := events.NewMetricsStore(nil, events.DefaultRetention)
		if err := replayAllEvents(store, eventsDir); err != nil {
			log.Printf("observability metrics: replay error: %v", err)
		}
		snap := store.Snapshot()
		return &snap
	})
}

// handleObservabilityMetrics returns a MetricsSnapshot from a TTL cache,
// avoiding expensive disk replay on every request.
func handleObservabilityMetrics(eventsDir string, cache *cachedValue[*events.MetricsSnapshot]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if eventsDir == "" {
			w.WriteHeader(http.StatusServiceUnavailable)
			writeJSON(w, ObservabilityMetricsResponse{
				Success: false,
				Error:   "observability not configured",
			})
			return
		}

		snap := cache.get()
		writeJSON(w, ObservabilityMetricsResponse{
			Success: true,
			Data:    snap,
		})
	}
}

// replayAllEvents replays all JSONL files in the events directory into the store.
func replayAllEvents(store *events.MetricsStore, dir string) error {
	pattern := filepath.Join(dir, "events-*.jsonl")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("globbing events files: %w", err)
	}
	sort.Strings(files)
	for _, f := range files {
		if _, err := store.ReplayFromFile(f); err != nil {
			log.Printf("observability: skipping %s: %v", filepath.Base(f), err)
		}
	}
	return nil
}

// handleObservabilityEvents returns paginated events from JSONL files.
func handleObservabilityEvents(eventsDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		page, err := parseIntParam(q.Get("page"), 1)
		if err != nil || page < 1 {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, ObservabilityEventsResponse{Error: "invalid page parameter"})
			return
		}

		perPage, err := parseIntParam(q.Get("per_page"), 50)
		if err != nil || perPage < 1 {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, ObservabilityEventsResponse{Error: "invalid per_page parameter"})
			return
		}
		if perPage > 200 {
			perPage = 200
		}

		sinceStr := q.Get("since")
		if sinceStr != "" {
			if _, err := time.Parse(time.RFC3339, sinceStr); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, ObservabilityEventsResponse{Error: "invalid since parameter, expected RFC3339"})
				return
			}
		}

		opts := EventReadOpts{
			Page:    page,
			PerPage: perPage,
			Type:    q.Get("type"),
			Agent:   q.Get("agent"),
			Since:   sinceStr,
		}

		evts, total, err := readEventsFromJSONL(eventsDir, opts)
		if err != nil {
			log.Printf("observability events: read error: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, ObservabilityEventsResponse{Error: "failed to read events"})
			return
		}

		writeJSON(w, ObservabilityEventsResponse{
			Success: true,
			Data:    evts,
			Total:   total,
			Page:    page,
			PerPage: perPage,
		})
	}
}

// readEventsFromJSONL reads events from JSONL files, applies filters, and paginates.
// Events are returned in reverse chronological order (most recent first).
func readEventsFromJSONL(dir string, opts EventReadOpts) ([]events.Event, int, error) {
	if dir == "" {
		return []events.Event{}, 0, nil
	}

	pattern := filepath.Join(dir, "events-*.jsonl")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, 0, fmt.Errorf("globbing events files: %w", err)
	}
	if len(files) == 0 {
		return []events.Event{}, 0, nil
	}

	// Sort files by name descending (most recent date first)
	sort.Sort(sort.Reverse(sort.StringSlice(files)))

	var sinceTime time.Time
	if opts.Since != "" {
		sinceTime, _ = time.Parse(time.RFC3339, opts.Since) // already validated
	}

	var all []events.Event
	for _, f := range files {
		fileEvents, err := readJSONLFile(f)
		if err != nil {
			log.Printf("observability: skipping %s: %v", filepath.Base(f), err)
			continue
		}
		for i := range fileEvents {
			e := &fileEvents[i]
			if opts.Type != "" && string(e.Type) != opts.Type {
				continue
			}
			if opts.Agent != "" && e.Agent != opts.Agent {
				continue
			}
			if !sinceTime.IsZero() && e.Timestamp.Before(sinceTime) {
				continue
			}
			all = append(all, *e)
		}
	}

	// Sort by timestamp descending (most recent first)
	sort.Slice(all, func(i, j int) bool {
		return all[i].Timestamp.After(all[j].Timestamp)
	})

	total := len(all)

	// Paginate
	start := (opts.Page - 1) * opts.PerPage
	if start >= total {
		return []events.Event{}, total, nil
	}
	end := start + opts.PerPage
	if end > total {
		end = total
	}

	return all[start:end], total, nil
}

// readJSONLFile reads all events from a single JSONL file.
// Malformed lines are skipped.
func readJSONLFile(path string) ([]events.Event, error) {
	// #nosec G304 - path from controlled glob pattern
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var evts []events.Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	for scanner.Scan() {
		var e events.Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		evts = append(evts, e)
	}
	if err := scanner.Err(); err != nil {
		return evts, err
	}
	return evts, nil
}

// parseIntParam parses an integer query parameter with a default value.
func parseIntParam(s string, defaultVal int) (int, error) {
	if s == "" {
		return defaultVal, nil
	}
	return strconv.Atoi(s)
}

// resolveEventsDir resolves the events directory from daemon config.
func ResolveEventsDir() string {
	loomDir := cli.GetBeadsDir()
	if loomDir == "" {
		loomDir = "."
	}
	dc, err := config.LoadDaemonConfig(loomDir)
	if err != nil {
		return ""
	}
	if dc.Daemon.EventsDir == "" {
		return ""
	}
	return daemon.ResolveDaemonPath(loomDir, dc.Daemon.EventsDir)
}
