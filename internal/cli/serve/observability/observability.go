package observability

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
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// MetricsResponse wraps the metrics snapshot for the API.
type MetricsResponse struct {
	Success bool                    `json:"success"`
	Data    *events.MetricsSnapshot `json:"data,omitempty"`
	Error   string                  `json:"error,omitempty"`
}

// EventsResponse wraps paginated events for the API.
type EventsResponse struct {
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

// CachedValue wraps a collection function with TTL caching and request
// coalescing (singleflight).
type CachedValue[T any] struct {
	ttl       time.Duration
	collectFn func() T

	mu       sync.Mutex
	cached   T
	cachedAt time.Time
	inflight bool
	waitCh   chan struct{}
}

// NewCachedValue creates a cached value with the given TTL and collection function.
func NewCachedValue[T any](ttl time.Duration, fn func() T) *CachedValue[T] {
	return &CachedValue[T]{
		ttl:       ttl,
		collectFn: fn,
	}
}

// Get returns cached data if fresh, otherwise triggers a single collection
// that all concurrent callers share.
func (c *CachedValue[T]) Get() T {
	c.mu.Lock()

	if !c.cachedAt.IsZero() && time.Since(c.cachedAt) < c.ttl {
		data := c.cached
		c.mu.Unlock()
		return data
	}

	if c.inflight {
		ch := c.waitCh
		c.mu.Unlock()
		<-ch
		c.mu.Lock()
		data := c.cached
		c.mu.Unlock()
		return data
	}

	c.inflight = true
	ch := make(chan struct{})
	c.waitCh = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.inflight = false
		close(ch)
		c.mu.Unlock()
	}()

	data := c.collectFn()

	c.mu.Lock()
	c.cached = data
	c.cachedAt = time.Now()
	c.mu.Unlock()

	return data
}

// NewMetricsCache creates a TTL cache for observability metrics snapshots.
func NewMetricsCache(eventsDir string) *CachedValue[*events.MetricsSnapshot] {
	return NewCachedValue[*events.MetricsSnapshot](30*time.Second, func() *events.MetricsSnapshot {
		store := events.NewMetricsStore(nil, events.DefaultRetention)
		if err := ReplayAllEvents(store, eventsDir); err != nil {
			log.Printf("observability metrics: replay error: %v", err)
		}
		snap := store.Snapshot()
		return &snap
	})
}

// HandleMetrics returns a MetricsSnapshot from a TTL cache,
// avoiding expensive disk replay on every request.
func HandleMetrics(eventsDir string, cache *CachedValue[*events.MetricsSnapshot]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if eventsDir == "" {
			w.WriteHeader(http.StatusServiceUnavailable)
			writeJSON(w, MetricsResponse{
				Success: false,
				Error:   "observability not configured",
			})
			return
		}

		snap := cache.Get()
		writeJSON(w, MetricsResponse{
			Success: true,
			Data:    snap,
		})
	}
}

// ReplayAllEvents replays all JSONL files in the events directory into the store.
func ReplayAllEvents(store *events.MetricsStore, dir string) error {
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

// HandleEvents returns paginated events from JSONL files.
func HandleEvents(eventsDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts, err := parseEventReadOpts(r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, EventsResponse{Error: err.Error()})
			return
		}

		evts, total, err := ReadEventsFromJSONL(eventsDir, opts)
		if err != nil {
			log.Printf("observability events: read error: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, EventsResponse{Error: "failed to read events"})
			return
		}

		writeJSON(w, EventsResponse{
			Success: true,
			Data:    evts,
			Total:   total,
			Page:    opts.Page,
			PerPage: opts.PerPage,
		})
	}
}

// parseEventReadOpts validates and parses query parameters into EventReadOpts.
func parseEventReadOpts(r *http.Request) (EventReadOpts, error) {
	q := r.URL.Query()

	page, err := ParseIntParam(q.Get("page"), 1)
	if err != nil || page < 1 {
		return EventReadOpts{}, fmt.Errorf("invalid page parameter")
	}

	perPage, err := ParseIntParam(q.Get("per_page"), 50)
	if err != nil || perPage < 1 {
		return EventReadOpts{}, fmt.Errorf("invalid per_page parameter")
	}
	if perPage > 200 {
		perPage = 200
	}

	sinceStr := q.Get("since")
	if sinceStr != "" {
		if _, err := time.Parse(time.RFC3339, sinceStr); err != nil {
			return EventReadOpts{}, fmt.Errorf("invalid since parameter, expected RFC3339")
		}
	}

	return EventReadOpts{
		Page:    page,
		PerPage: perPage,
		Type:    q.Get("type"),
		Agent:   q.Get("agent"),
		Since:   sinceStr,
	}, nil
}

// ReadEventsFromJSONL reads events from JSONL files, applies filters, and paginates.
// Events are returned in reverse chronological order (most recent first).
func ReadEventsFromJSONL(dir string, opts EventReadOpts) ([]events.Event, int, error) {
	if dir == "" {
		return []events.Event{}, 0, nil
	}

	files, err := globEventFiles(dir)
	if err != nil {
		return nil, 0, err
	}
	if len(files) == 0 {
		return []events.Event{}, 0, nil
	}

	all := collectFilteredEvents(files, opts)

	sort.Slice(all, func(i, j int) bool {
		return all[i].Timestamp.After(all[j].Timestamp)
	})

	return paginateEvents(all, opts.Page, opts.PerPage), len(all), nil
}

// globEventFiles returns event JSONL files sorted most-recent-first.
func globEventFiles(dir string) ([]string, error) {
	pattern := filepath.Join(dir, "events-*.jsonl")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("globbing events files: %w", err)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	return files, nil
}

// collectFilteredEvents reads all event files and applies type/agent/since filters.
func collectFilteredEvents(files []string, opts EventReadOpts) []events.Event {
	var sinceTime time.Time
	if opts.Since != "" {
		sinceTime, _ = time.Parse(time.RFC3339, opts.Since)
	}

	var all []events.Event
	for _, f := range files {
		fileEvents, err := ReadJSONLFile(f)
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
	return all
}

// paginateEvents returns a page of events from a sorted slice.
func paginateEvents(all []events.Event, page, perPage int) []events.Event {
	total := len(all)
	start := (page - 1) * perPage
	if start >= total {
		return []events.Event{}
	}
	end := start + perPage
	if end > total {
		end = total
	}
	return all[start:end]
}

// ReadJSONLFile reads all events from a single JSONL file.
// Malformed lines are skipped.
func ReadJSONLFile(path string) ([]events.Event, error) {
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

// ParseIntParam parses an integer query parameter with a default value.
func ParseIntParam(s string, defaultVal int) (int, error) {
	if s == "" {
		return defaultVal, nil
	}
	return strconv.Atoi(s)
}

// ResolveEventsDir resolves the serve-owned local observability journal.
func ResolveEventsDir() string {
	loomDir := cli.GetWorkspaceRuntimeDir()
	if loomDir == "" {
		return ""
	}
	return filepath.Join(loomDir, "events")
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("Failed to encode JSON response: %v", err)
	}
}
