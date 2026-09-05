package usagecmd

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

// Response is the JSON response for /api/usage.
type Response struct {
	TotalInputTokens      int64                `json:"total_input_tokens"`
	TotalOutputTokens     int64                `json:"total_output_tokens"`
	TotalCacheReadTokens  int64                `json:"total_cache_read_tokens"`
	TotalCacheWriteTokens int64                `json:"total_cache_write_tokens"`
	TotalCost             float64              `json:"total_cost"`
	SessionCount          int                  `json:"session_count"`
	ByAgent               []AgentSummary       `json:"by_agent"`
	ByBackend             []BackendSummary     `json:"by_backend"`
	DailyCosts            []DailyCost          `json:"daily_costs"`
	Sessions              []usage.SessionUsage `json:"sessions"`
	Timestamp             time.Time            `json:"timestamp"`
}

// AgentSummary aggregates usage for a single agent.
type AgentSummary struct {
	Name         string  `json:"name"`
	Sessions     int     `json:"sessions"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalCost    float64 `json:"total_cost"`
}

// BackendSummary aggregates usage for a single backend.
type BackendSummary struct {
	Name      string  `json:"name"`
	Sessions  int     `json:"sessions"`
	TotalCost float64 `json:"total_cost"`
}

// DailyCost represents cost for a single day.
type DailyCost struct {
	Date     string  `json:"date"`
	Cost     float64 `json:"cost"`
	Sessions int     `json:"sessions"`
}

// Reader is the read side of a usage ledger. Both the legacy usage.jsonl
// store and the sessions-backed reader satisfy it, so the handler does not
// care which ledger it is serving.
type Reader interface {
	Read(usage.Filter) ([]usage.SessionUsage, error)
}

// sessionsReader reads usage from <dir>/sessions/index.jsonl, the ledger every
// finalized agent run writes to.
type sessionsReader struct{ dir string }

func (r sessionsReader) Read(f usage.Filter) ([]usage.SessionUsage, error) {
	records, _, err := usage.ReadSessionUsage(r.dir, f)
	return records, err
}

// InitSessionsReader returns a Reader backed by the session index. It returns
// nil (yielding a 503 from HandleUsage) when the directory cannot be opened,
// matching InitStore's nil-safe contract.
func InitSessionsReader(dir string) Reader {
	if dir == "" {
		dir = "."
	}
	if _, _, err := usage.ReadSessionUsage(dir, usage.Filter{}); err != nil {
		log.Printf("Warning: failed to open sessions usage ledger: %v", err)
		return nil
	}
	return sessionsReader{dir: dir}
}

// InitStore creates a usage store from the given directory.
// Returns nil if the store cannot be created.
func InitStore(dir string) *usage.Store {
	if dir == "" {
		dir = "."
	}
	s, err := usage.NewStore(dir)
	if err != nil {
		log.Printf("Warning: failed to create usage store: %v", err)
		return nil
	}
	return s
}

// HandleUsage returns an http.HandlerFunc that reads usage data from the given
// reader. A nil reader yields 503.
func HandleUsage(reader Reader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if reader == nil {
			http.Error(w, `{"error":"usage store not initialized"}`, http.StatusServiceUnavailable)
			return
		}

		// Parse query parameters into filter
		var f usage.Filter
		f.AgentName = r.URL.Query().Get("agent")
		f.Backend = r.URL.Query().Get("backend")
		f.EpicID = r.URL.Query().Get("epic")
		f.Status = r.URL.Query().Get("status")

		if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
			t, err := time.Parse("2006-01-02", sinceStr)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]string{"error": "invalid since date format, expected YYYY-MM-DD"})
				return
			}
			f.Since = t
		}
		if untilStr := r.URL.Query().Get("until"); untilStr != "" {
			t, err := time.Parse("2006-01-02", untilStr)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]string{"error": "invalid until date format, expected YYYY-MM-DD"})
				return
			}
			f.Until = t.Add(24*time.Hour - time.Nanosecond)
		}

		records, err := reader.Read(f)
		if err != nil {
			log.Printf("Error reading usage data: %v", err)
			http.Error(w, `{"error":"failed to read usage data"}`, http.StatusInternalServerError)
			return
		}
		if records == nil {
			records = []usage.SessionUsage{}
		}

		writeJSON(w, buildResponse(records))
	}
}

// buildResponse builds a Response from raw session records.
func buildResponse(records []usage.SessionUsage) Response {
	resp := Response{
		SessionCount: len(records),
		Sessions:     records,
		Timestamp:    time.Now(),
	}

	agentMap := make(map[string]*AgentSummary)
	backendMap := make(map[string]*BackendSummary)
	dailyMap := make(map[string]*DailyCost)

	for _, rec := range records {
		accumulateTotals(&resp, rec)
		accumulateAgentUsage(agentMap, rec)
		accumulateBackendUsage(backendMap, rec)
		accumulateDailyCost(dailyMap, rec)
	}

	resp.ByAgent = sortedAgentSummaries(agentMap)
	resp.ByBackend = sortedBackendSummaries(backendMap)
	resp.DailyCosts = sortedDailyCosts(dailyMap)
	return resp
}

func accumulateTotals(resp *Response, rec usage.SessionUsage) {
	resp.TotalInputTokens += rec.InputTokens
	resp.TotalOutputTokens += rec.OutputTokens
	resp.TotalCacheReadTokens += rec.CacheReadTokens
	resp.TotalCacheWriteTokens += rec.CacheWriteTokens
	resp.TotalCost += rec.EstimatedCostUSD
}

func accumulateAgentUsage(m map[string]*AgentSummary, rec usage.SessionUsage) {
	a, ok := m[rec.AgentName]
	if !ok {
		a = &AgentSummary{Name: rec.AgentName}
		m[rec.AgentName] = a
	}
	a.Sessions++
	a.InputTokens += rec.InputTokens
	a.OutputTokens += rec.OutputTokens
	a.TotalCost += rec.EstimatedCostUSD
}

func accumulateBackendUsage(m map[string]*BackendSummary, rec usage.SessionUsage) {
	b, ok := m[rec.Backend]
	if !ok {
		b = &BackendSummary{Name: rec.Backend}
		m[rec.Backend] = b
	}
	b.Sessions++
	b.TotalCost += rec.EstimatedCostUSD
}

func accumulateDailyCost(m map[string]*DailyCost, rec usage.SessionUsage) {
	day := rec.StartedAt.Format("2006-01-02")
	dc, ok := m[day]
	if !ok {
		dc = &DailyCost{Date: day}
		m[day] = dc
	}
	dc.Cost += rec.EstimatedCostUSD
	dc.Sessions++
}

func sortedAgentSummaries(m map[string]*AgentSummary) []AgentSummary {
	s := make([]AgentSummary, 0, len(m))
	for _, a := range m {
		s = append(s, *a)
	}
	sort.Slice(s, func(i, j int) bool { return s[i].TotalCost > s[j].TotalCost })
	return s
}

func sortedBackendSummaries(m map[string]*BackendSummary) []BackendSummary {
	s := make([]BackendSummary, 0, len(m))
	for _, b := range m {
		s = append(s, *b)
	}
	sort.Slice(s, func(i, j int) bool { return s[i].TotalCost > s[j].TotalCost })
	return s
}

func sortedDailyCosts(m map[string]*DailyCost) []DailyCost {
	s := make([]DailyCost, 0, len(m))
	for _, dc := range m {
		s = append(s, *dc)
	}
	sort.Slice(s, func(i, j int) bool { return s[i].Date < s[j].Date })
	return s
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("Failed to encode JSON response: %v", err)
	}
}
