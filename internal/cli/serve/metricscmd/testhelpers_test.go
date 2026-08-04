package metricscmd

import (
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

type AgentStatus = monitor.AgentStatus

// usageStoreInstance is a test-scoped usage store.
var usageStoreInstance *usage.Store

// staleDetectorInstance is a stub for stale detector tests.
var staleDetectorInstance interface{}

// AgentUsageSummary summarizes usage for one agent.
type AgentUsageSummary struct {
	Name      string  `json:"name"`
	Sessions  int     `json:"sessions"`
	TotalCost float64 `json:"total_cost"`
}

// UsageResponse is the response shape expected by usage tests.
type UsageResponse struct {
	SessionCount     int                  `json:"session_count"`
	Sessions         []usage.SessionUsage `json:"sessions"`
	TotalInputTokens int64                `json:"total_input_tokens"`
	TotalCost        float64              `json:"total_cost"`
	ByAgent          []AgentUsageSummary  `json:"by_agent"`
}

// handleUsage is a test-compat handler for usage endpoint tests.
func handleUsage(w http.ResponseWriter, r *http.Request) {
	if usageStoreInstance == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"usage store not configured"}`))
		return
	}

	// Parse query filters
	q := r.URL.Query()
	filter := usage.Filter{
		AgentName: q.Get("agent"),
		Backend:   q.Get("backend"),
	}
	if since := q.Get("since"); since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid since date"}`))
			return
		}
		filter.Since = t
	}
	if until := q.Get("until"); until != "" {
		t, err := time.Parse(time.RFC3339, until)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid until date"}`))
			return
		}
		filter.Until = t
	}

	records, err := usageStoreInstance.Read(filter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"failed to read usage"}`))
		return
	}

	if records == nil {
		records = []usage.SessionUsage{}
	}

	var totalInput int64
	var totalCost float64
	agentMap := make(map[string]*AgentUsageSummary)
	for _, rec := range records {
		totalInput += rec.InputTokens
		totalCost += rec.EstimatedCostUSD
		a, ok := agentMap[rec.AgentName]
		if !ok {
			a = &AgentUsageSummary{Name: rec.AgentName}
			agentMap[rec.AgentName] = a
		}
		a.Sessions++
		a.TotalCost += rec.EstimatedCostUSD
	}

	byAgent := make([]AgentUsageSummary, 0, len(agentMap))
	for _, a := range agentMap {
		byAgent = append(byAgent, *a)
	}

	resp := UsageResponse{
		SessionCount:     len(records),
		Sessions:         records,
		TotalInputTokens: totalInput,
		TotalCost:        totalCost,
		ByAgent:          byAgent,
	}

	writeJSON(w, resp)
}

// handleStaleDetector is a stub for stale detector endpoint tests.
func handleStaleDetector(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{
		"enabled": staleDetectorInstance != nil,
		"status":  "ok",
	})
}
