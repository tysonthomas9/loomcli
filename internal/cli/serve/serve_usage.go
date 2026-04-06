package serve

import (
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

// UsageResponse is the JSON response for /api/usage.
type UsageResponse struct {
	TotalInputTokens      int64                 `json:"total_input_tokens"`
	TotalOutputTokens     int64                 `json:"total_output_tokens"`
	TotalCacheReadTokens  int64                 `json:"total_cache_read_tokens"`
	TotalCacheWriteTokens int64                 `json:"total_cache_write_tokens"`
	TotalCost             float64               `json:"total_cost"`
	SessionCount          int                   `json:"session_count"`
	ByAgent               []UsageAgentSummary   `json:"by_agent"`
	ByBackend             []UsageBackendSummary `json:"by_backend"`
	DailyCosts            []UsageDailyCost      `json:"daily_costs"`
	Sessions              []usage.SessionUsage  `json:"sessions"`
	Timestamp             time.Time             `json:"timestamp"`
}

// UsageAgentSummary aggregates usage for a single agent.
type UsageAgentSummary struct {
	Name         string  `json:"name"`
	Sessions     int     `json:"sessions"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalCost    float64 `json:"total_cost"`
}

// UsageBackendSummary aggregates usage for a single backend.
type UsageBackendSummary struct {
	Name      string  `json:"name"`
	Sessions  int     `json:"sessions"`
	TotalCost float64 `json:"total_cost"`
}

// UsageDailyCost represents cost for a single day.
type UsageDailyCost struct {
	Date     string  `json:"date"`
	Cost     float64 `json:"cost"`
	Sessions int     `json:"sessions"`
}

func handleUsage(w http.ResponseWriter, r *http.Request) {
	if usageStoreInstance == nil {
		http.Error(w, `{"error":"usage store not initialized"}`, http.StatusServiceUnavailable)
		return
	}

	// Parse query parameters into filter
	var f usage.Filter
	f.AgentName = r.URL.Query().Get("agent")
	f.Backend = r.URL.Query().Get("backend")
	f.EpicID = r.URL.Query().Get("epic")

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

	records, err := usageStoreInstance.Read(f)
	if err != nil {
		log.Printf("Error reading usage data: %v", err)
		http.Error(w, `{"error":"failed to read usage data"}`, http.StatusInternalServerError)
		return
	}
	if records == nil {
		records = []usage.SessionUsage{}
	}

	writeJSON(w, buildUsageResponse(records))
}

// buildUsageResponse builds a UsageResponse from raw session records.
func buildUsageResponse(records []usage.SessionUsage) UsageResponse {
	resp := UsageResponse{
		SessionCount: len(records),
		Sessions:     records,
		Timestamp:    time.Now(),
	}

	agentMap := make(map[string]*UsageAgentSummary)
	backendMap := make(map[string]*UsageBackendSummary)
	dailyMap := make(map[string]*UsageDailyCost)

	for _, rec := range records {
		resp.TotalInputTokens += rec.InputTokens
		resp.TotalOutputTokens += rec.OutputTokens
		resp.TotalCacheReadTokens += rec.CacheReadTokens
		resp.TotalCacheWriteTokens += rec.CacheWriteTokens
		resp.TotalCost += rec.EstimatedCostUSD

		// Per-agent
		a, ok := agentMap[rec.AgentName]
		if !ok {
			a = &UsageAgentSummary{Name: rec.AgentName}
			agentMap[rec.AgentName] = a
		}
		a.Sessions++
		a.InputTokens += rec.InputTokens
		a.OutputTokens += rec.OutputTokens
		a.TotalCost += rec.EstimatedCostUSD

		// Per-backend
		b, ok := backendMap[rec.Backend]
		if !ok {
			b = &UsageBackendSummary{Name: rec.Backend}
			backendMap[rec.Backend] = b
		}
		b.Sessions++
		b.TotalCost += rec.EstimatedCostUSD

		// Daily costs
		day := rec.StartedAt.Format("2006-01-02")
		dc, ok := dailyMap[day]
		if !ok {
			dc = &UsageDailyCost{Date: day}
			dailyMap[day] = dc
		}
		dc.Cost += rec.EstimatedCostUSD
		dc.Sessions++
	}

	// Build sorted slices
	resp.ByAgent = make([]UsageAgentSummary, 0, len(agentMap))
	for _, a := range agentMap {
		resp.ByAgent = append(resp.ByAgent, *a)
	}
	sort.Slice(resp.ByAgent, func(i, j int) bool {
		return resp.ByAgent[i].TotalCost > resp.ByAgent[j].TotalCost
	})

	resp.ByBackend = make([]UsageBackendSummary, 0, len(backendMap))
	for _, b := range backendMap {
		resp.ByBackend = append(resp.ByBackend, *b)
	}
	sort.Slice(resp.ByBackend, func(i, j int) bool {
		return resp.ByBackend[i].TotalCost > resp.ByBackend[j].TotalCost
	})

	resp.DailyCosts = make([]UsageDailyCost, 0, len(dailyMap))
	for _, dc := range dailyMap {
		resp.DailyCosts = append(resp.DailyCosts, *dc)
	}
	sort.Slice(resp.DailyCosts, func(i, j int) bool {
		return resp.DailyCosts[i].Date < resp.DailyCosts[j].Date
	})

	return resp
}
