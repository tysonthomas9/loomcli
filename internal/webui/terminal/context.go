package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const contextFetchTimeout = 3 * time.Second

// TerminalContext holds project status data fetched from the loom server.
type TerminalContext struct {
	Stats  TerminalContextStats `json:"stats"`
	Agents []TerminalAgentInfo  `json:"agents"`
	Tasks  TerminalContextTasks `json:"tasks"`
}

// TerminalContextStats holds issue count statistics.
type TerminalContextStats struct {
	Open       int `json:"open"`
	Closed     int `json:"closed"`
	Total      int `json:"total"`
	InProgress int `json:"in_progress"`
	Review     int `json:"review"`
	Blocked    int `json:"blocked"`
}

// TerminalAgentInfo holds agent name and status.
type TerminalAgentInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// TerminalContextTasks holds task pipeline counts.
type TerminalContextTasks struct {
	NeedsPlanning    int `json:"needs_planning"`
	ReadyToImplement int `json:"ready_to_implement"`
	InProgress       int `json:"in_progress"`
	NeedReview       int `json:"need_review"`
	Backlog          int `json:"backlog"`
}

// FetchTerminalContext queries the loom server /api/status endpoint
// and returns the parsed context. Returns an error if the server is
// unavailable or returns invalid data.
func FetchTerminalContext(loomServerURL string) (*TerminalContext, error) {
	ctx, cancel := context.WithTimeout(context.Background(), contextFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loomServerURL+"/api/monitor/status", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from loom server", resp.StatusCode)
	}

	var tc TerminalContext
	if err := json.NewDecoder(resp.Body).Decode(&tc); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &tc, nil
}

// FormatContextBanner renders the terminal context as an ANSI box-drawing
// banner suitable for writing to a PTY.
func FormatContextBanner(tc *TerminalContext, workspaceName string) string {
	if workspaceName == "" {
		workspaceName = "(default)"
	}

	var lines []string

	// Tasks line
	taskParts := []string{
		fmt.Sprintf("%d open", tc.Stats.Open),
		fmt.Sprintf("%d blocked", tc.Stats.Blocked),
		fmt.Sprintf("%d review", tc.Stats.Review),
		fmt.Sprintf("%d in-progress", tc.Stats.InProgress),
	}
	lines = append(lines, "Tasks: "+strings.Join(taskParts, " \u00b7 "))

	// Agents line
	if len(tc.Agents) > 0 {
		var agentParts []string
		for _, a := range tc.Agents {
			agentParts = append(agentParts, fmt.Sprintf("%s (%s)", a.Name, a.Status))
		}
		lines = append(lines, "Agents: "+strings.Join(agentParts, " \u00b7 "))
	} else {
		lines = append(lines, "Agents: none active")
	}

	// Planning line
	planParts := []string{
		fmt.Sprintf("%d need plans", tc.Tasks.NeedsPlanning),
		fmt.Sprintf("%d ready to implement", tc.Tasks.ReadyToImplement),
	}
	lines = append(lines, "Planning: "+strings.Join(planParts, " \u00b7 "))

	// Compute max content width using rune count so that multi-byte
	// characters (e.g. middle-dot separator ·) are measured correctly.
	runeWidth := utf8.RuneCountInString
	headerLine := "Workspace: " + workspaceName + " "
	maxWidth := runeWidth(headerLine)
	for _, l := range lines {
		if w := runeWidth(l); w > maxWidth {
			maxWidth = w
		}
	}

	// Build the box.
	// Total box width in columns: maxWidth + 5
	//   top:    ┌─ <header><padding─>┐   = 3 + maxWidth + 1 + 1
	//   body:   │ <content><pad spaces>│ = 2 + maxWidth + 2 + 1
	//   bottom: └<───────────────────>┘ = 1 + (maxWidth+3) + 1
	var b strings.Builder

	// Top border
	b.WriteString("\u250c\u2500 ")
	b.WriteString(headerLine)
	for i := 0; i < maxWidth-runeWidth(headerLine)+1; i++ {
		b.WriteString("\u2500")
	}
	b.WriteString("\u2510\r\n")

	// Content lines
	for _, l := range lines {
		b.WriteString("\u2502 ")
		b.WriteString(l)
		pad := maxWidth - runeWidth(l) + 2
		for i := 0; i < pad; i++ {
			b.WriteByte(' ')
		}
		b.WriteString("\u2502\r\n")
	}

	// Bottom border
	b.WriteString("\u2514")
	for i := 0; i < maxWidth+3; i++ {
		b.WriteString("\u2500")
	}
	b.WriteString("\u2518\r\n")

	return b.String()
}
