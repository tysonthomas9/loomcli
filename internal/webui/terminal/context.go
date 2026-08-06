package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/netbase"
)

const contextFetchTimeout = 3 * time.Second

var contextHTTPClient = &http.Client{Transport: netbase.Transport()}

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

	resp, err := contextHTTPClient.Do(req)
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

	lines := buildBannerLines(tc)
	headerLine := "Workspace: " + workspaceName + " "
	maxWidth := maxRuneWidth(headerLine, lines)

	var b strings.Builder
	writeTopBorder(&b, headerLine, maxWidth)
	writeContentLines(&b, lines, maxWidth)
	writeBottomBorder(&b, maxWidth)
	return b.String()
}

// buildBannerLines constructs the content lines for the context banner.
func buildBannerLines(tc *TerminalContext) []string {
	taskParts := []string{
		fmt.Sprintf("%d open", tc.Stats.Open),
		fmt.Sprintf("%d blocked", tc.Stats.Blocked),
		fmt.Sprintf("%d review", tc.Stats.Review),
		fmt.Sprintf("%d in-progress", tc.Stats.InProgress),
	}

	var lines []string
	lines = append(lines, "Tasks: "+strings.Join(taskParts, " \u00b7 "))

	if len(tc.Agents) > 0 {
		var agentParts []string
		for _, a := range tc.Agents {
			agentParts = append(agentParts, fmt.Sprintf("%s (%s)", a.Name, a.Status))
		}
		lines = append(lines, "Agents: "+strings.Join(agentParts, " \u00b7 "))
	} else {
		lines = append(lines, "Agents: none active")
	}

	planParts := []string{
		fmt.Sprintf("%d need plans", tc.Tasks.NeedsPlanning),
		fmt.Sprintf("%d ready to implement", tc.Tasks.ReadyToImplement),
	}
	lines = append(lines, "Planning: "+strings.Join(planParts, " \u00b7 "))
	return lines
}

// maxRuneWidth returns the maximum rune width among the header and all lines.
func maxRuneWidth(headerLine string, lines []string) int {
	m := utf8.RuneCountInString(headerLine)
	for _, l := range lines {
		if w := utf8.RuneCountInString(l); w > m {
			m = w
		}
	}
	return m
}

// writeTopBorder writes the top border of the box to the builder.
func writeTopBorder(b *strings.Builder, headerLine string, maxWidth int) {
	b.WriteString("\u250c\u2500 ")
	b.WriteString(headerLine)
	for i := 0; i < maxWidth-utf8.RuneCountInString(headerLine)+1; i++ {
		b.WriteString("\u2500")
	}
	b.WriteString("\u2510\r\n")
}

// writeContentLines writes the padded content lines of the box.
func writeContentLines(b *strings.Builder, lines []string, maxWidth int) {
	for _, l := range lines {
		b.WriteString("\u2502 ")
		b.WriteString(l)
		for i := 0; i < maxWidth-utf8.RuneCountInString(l)+2; i++ {
			b.WriteByte(' ')
		}
		b.WriteString("\u2502\r\n")
	}
}

// writeBottomBorder writes the bottom border of the box to the builder.
func writeBottomBorder(b *strings.Builder, maxWidth int) {
	b.WriteString("\u2514")
	for i := 0; i < maxWidth+3; i++ {
		b.WriteString("\u2500")
	}
	b.WriteString("\u2518\r\n")
}
