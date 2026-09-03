package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
)

type modernEvent struct {
	Type string      `json:"type"`
	Item *modernItem `json:"item"`
}

type modernItem struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	Text             string `json:"text"`
	Command          string `json:"command"`
	AggregatedOutput string `json:"aggregated_output"`
	Output           string `json:"output"`
	ExitCode         *int   `json:"exit_code"`
}

func modernEvents(data []byte) ([]transcript.Event, bool) {
	events := []transcript.Event{}
	modern := false
	for _, line := range bytes.Split(data, []byte("\n")) {
		var event modernEvent
		if json.Unmarshal(line, &event) != nil || !isModernEventType(event.Type) {
			continue
		}
		modern = true
		events = appendModernEvent(events, event)
	}
	for index := range events {
		events[index].Seq = index
	}
	return events, modern
}

func isModernEventType(kind string) bool {
	return strings.HasPrefix(kind, "thread.") || strings.HasPrefix(kind, "turn.") || strings.HasPrefix(kind, "item.")
}

func appendModernEvent(events []transcript.Event, event modernEvent) []transcript.Event {
	if event.Type != "item.completed" || event.Item == nil {
		return events
	}
	return appendModernItem(events, *event.Item)
}

func appendModernItem(events []transcript.Event, item modernItem) []transcript.Event {
	text := item.Text
	switch item.Type {
	case "agent_message":
		return appendModernText(events, transcript.EventText, text)
	case "reasoning":
		return appendModernText(events, transcript.EventReasoning, text)
	case "command_execution":
		return appendModernCommand(events, item)
	default:
		return events
	}
}

func appendModernText(events []transcript.Event, kind, text string) []transcript.Event {
	if strings.TrimSpace(text) == "" {
		return events
	}
	return append(events, transcript.Event{Role: transcript.RoleAssistant, Type: kind, Text: text})
}

func appendModernCommand(events []transcript.Event, item modernItem) []transcript.Event {
	input, _ := json.Marshal(map[string]string{"command": item.Command})
	events = append(events, transcript.Event{
		Role: transcript.RoleAssistant, Type: transcript.EventToolUse, ToolName: "command_execution",
		ToolUseID: item.ID, ToolInput: input,
	})
	return append(events, transcript.Event{
		Role: transcript.RoleTool, Type: transcript.EventToolResult, ToolUseID: item.ID,
		Output: modernCommandOutput(item),
	})
}

func modernCommandOutput(item modernItem) string {
	if strings.TrimSpace(item.AggregatedOutput) != "" {
		return item.AggregatedOutput
	}
	if strings.TrimSpace(item.Output) != "" {
		return item.Output
	}
	if item.ExitCode != nil {
		return fmt.Sprintf("exit code: %d", *item.ExitCode)
	}
	return ""
}
