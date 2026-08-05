package monitor

import (
	"strings"
	"testing"
)

func TestTruncateToWidth_NoTruncation(t *testing.T) {
	result := truncateToWidth("hello", 10)
	if result != "hello" {
		t.Errorf("truncateToWidth(%q, 10) = %q, want %q", "hello", result, "hello")
	}
}

func TestTruncateToWidth_ExactFit(t *testing.T) {
	result := truncateToWidth("hello", 5)
	if result != "hello" {
		t.Errorf("truncateToWidth(%q, 5) = %q, want %q", "hello", result, "hello")
	}
}

func TestTruncateToWidth_Truncated(t *testing.T) {
	result := truncateToWidth("hello world", 8)
	if !strings.HasSuffix(result, "...") {
		t.Errorf("truncateToWidth(%q, 8) = %q, expected suffix '...'", "hello world", result)
	}
	if displayWidth(result) > 8 {
		t.Errorf("truncateToWidth(%q, 8) display width = %d, want <= 8", "hello world", displayWidth(result))
	}
}

func TestPadRight_Shorter(t *testing.T) {
	result := padRight("hi", 10)
	if displayWidth(result) != 10 {
		t.Errorf("padRight(%q, 10) display width = %d, want 10", "hi", displayWidth(result))
	}
	if !strings.HasPrefix(result, "hi") {
		t.Errorf("padRight(%q, 10) = %q, should start with 'hi'", "hi", result)
	}
}

func TestPadRight_ExactWidth(t *testing.T) {
	result := padRight("hello", 5)
	if result != "hello" {
		t.Errorf("padRight(%q, 5) = %q, want %q", "hello", result, "hello")
	}
}

func TestPadRight_Longer(t *testing.T) {
	result := padRight("hello world", 5)
	if result != "hello world" {
		t.Errorf("padRight(%q, 5) = %q, want %q (no truncation)", "hello world", result, "hello world")
	}
}

func TestCenterText_Shorter(t *testing.T) {
	result := centerText("hi", 10)
	if displayWidth(result) != 10 {
		t.Errorf("centerText(%q, 10) display width = %d, want 10", "hi", displayWidth(result))
	}
	if !strings.Contains(result, "hi") {
		t.Errorf("centerText(%q, 10) = %q, should contain 'hi'", "hi", result)
	}
}

func TestCenterText_ExactWidth(t *testing.T) {
	result := centerText("hello", 5)
	if result != "hello" {
		t.Errorf("centerText(%q, 5) = %q, want %q", "hello", result, "hello")
	}
}

func TestCenterText_Wider(t *testing.T) {
	result := centerText("hello world", 5)
	if result != "hello world" {
		t.Errorf("centerText(%q, 5) = %q, want %q", "hello world", result, "hello world")
	}
}

func TestRenderBoxLine_Coverage(t *testing.T) {
	result := renderBoxLine("hello")
	if !strings.HasPrefix(result, "║ ") {
		t.Error("renderBoxLine should start with '║ '")
	}
	if !strings.HasSuffix(result, " ║\n") {
		t.Error("renderBoxLine should end with ' ║\\n'")
	}
	if !strings.Contains(result, "hello") {
		t.Error("renderBoxLine should contain the content")
	}
}

func TestRenderBoxLine_LongContent(t *testing.T) {
	longContent := strings.Repeat("x", 200)
	result := renderBoxLine(longContent)
	if !strings.Contains(result, longContent) {
		t.Error("renderBoxLine should contain long content")
	}
}

func TestRenderTaskLine(t *testing.T) {
	var sb strings.Builder
	task := TaskInfo{
		ID:       "task-1",
		Title:    "Fix the bug",
		Priority: 2,
	}
	renderTaskLine(&sb, task)
	result := sb.String()

	if !strings.Contains(result, "P2") {
		t.Error("renderTaskLine should contain priority 'P2'")
	}
	if !strings.Contains(result, "task-1") {
		t.Error("renderTaskLine should contain task ID")
	}
	if !strings.Contains(result, "Fix the bug") {
		t.Error("renderTaskLine should contain task title")
	}
}

func TestRenderAgentLine_Basic(t *testing.T) {
	var sb strings.Builder
	agent := AgentStatus{
		Name:   "agent-1",
		Branch: "feature-branch",
		Status: "ready",
	}
	RenderAgentLine(&sb, agent, "  ")
	result := sb.String()

	if !strings.Contains(result, "agent-1") {
		t.Error("renderAgentLine should contain agent name")
	}
	if !strings.Contains(result, "feature-branch") {
		t.Error("renderAgentLine should contain branch")
	}
}

func TestRenderAgentLine_WithSyncIndicator(t *testing.T) {
	var sb strings.Builder
	agent := AgentStatus{
		Name:   "agent",
		Branch: "dev",
		Status: "ready",
		Ahead:  3,
		Behind: 1,
	}
	RenderAgentLine(&sb, agent, "  ")
	result := sb.String()

	if !strings.Contains(result, "↑3") {
		t.Errorf("renderAgentLine should show ahead indicator '↑3', got %q", result)
	}
	if !strings.Contains(result, "↓1") {
		t.Errorf("renderAgentLine should show behind indicator '↓1', got %q", result)
	}
}

func TestRenderAgentLine_StatusIcons(t *testing.T) {
	tests := []struct {
		status string
		icon   string
	}{
		{"planning:task-1", "●"},
		{"done:task-2", "●"},
		{"review:task-3", "●"},
		{"error:crash", "●"},
		{"3 changes", "●"},
		{"dirty", "●"},
		{"ready", "✓"},
		{"", "✓"},
	}

	for _, tc := range tests {
		var sb strings.Builder
		agent := AgentStatus{Name: "a", Branch: "b", Status: tc.status}
		RenderAgentLine(&sb, agent, "")
		result := sb.String()
		if !strings.Contains(result, tc.icon) {
			t.Errorf("RenderAgentLine(status=%q) should contain icon %q, got %q", tc.status, tc.icon, result)
		}
	}
}

func TestRenderAgentsWorkspace_Coverage(t *testing.T) {
	var sb strings.Builder
	agents := []AgentStatus{
		{Name: "a1", Branch: "b1", Status: "ready", Workspace: "ws1"},
		{Name: "a2", Branch: "b2", Status: "ready", Workspace: "ws1"},
		{Name: "a3", Branch: "b3", Status: "ready", Workspace: "ws2"},
	}
	renderAgentsWorkspace(&sb, agents)
	result := sb.String()

	if !strings.Contains(result, "ws1") {
		t.Error("renderAgentsWorkspace should show workspace name 'ws1'")
	}
	if !strings.Contains(result, "ws2") {
		t.Error("renderAgentsWorkspace should show workspace name 'ws2'")
	}
}

func TestCompleteSyncStatus_NoAgents(t *testing.T) {
	info := SyncInfo{DBSynced: true}
	result := completeSyncStatus(info, []AgentStatus{})
	if result.GitNeedsPush != 0 || result.GitNeedsPull != 0 {
		t.Errorf("completeSyncStatus with no agents should have 0 push/pull needs, got push=%d, pull=%d",
			result.GitNeedsPush, result.GitNeedsPull)
	}
}

func TestCompleteSyncStatus_WithCounts(t *testing.T) {
	info := SyncInfo{DBSynced: true}
	agents := []AgentStatus{
		{Name: "a1", Ahead: 2, Behind: 0},
		{Name: "a2", Ahead: 0, Behind: 3},
		{Name: "a3", Ahead: 1, Behind: 1},
		{Name: "a4", Ahead: 0, Behind: 0},
	}
	result := completeSyncStatus(info, agents)
	if result.GitNeedsPush != 2 {
		t.Errorf("GitNeedsPush = %d, want 2", result.GitNeedsPush)
	}
	if result.GitNeedsPull != 2 {
		t.Errorf("GitNeedsPull = %d, want 2", result.GitNeedsPull)
	}
}

func TestCompleteSyncStatus_PreservesDBInfo(t *testing.T) {
	info := SyncInfo{
		DBSynced:   true,
		DBLastSync: "2m ago",
	}
	result := completeSyncStatus(info, []AgentStatus{})
	if !result.DBSynced {
		t.Error("completeSyncStatus should preserve DBSynced")
	}
	if result.DBLastSync != "2m ago" {
		t.Errorf("DBLastSync = %q, want %q", result.DBLastSync, "2m ago")
	}
}
