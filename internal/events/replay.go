package events

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ReplayFromFile reads a JSONL event file and feeds each event through
// the MetricsStore's handleEvent method for cold-start recovery.
// Malformed lines are skipped. Returns the count of successfully replayed events.
func (ms *MetricsStore) ReplayFromFile(path string) (int, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return 0, fmt.Errorf("open events file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	count := 0
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		event.Type = normalizeEventType(event.Type)
		ms.handleEvent(event)
		count++
	}
	if err := scanner.Err(); err != nil {
		return count, fmt.Errorf("scan events file: %w", err)
	}
	return count, nil
}

// normalizeEventType maps old underscore-format event types to the current
// dot-notation format. This ensures JSONL files written before the rename
// can still be replayed correctly.
func normalizeEventType(et EventType) EventType {
	switch et {
	case "task_claimed":
		return TaskClaimed
	case "task_started":
		return TaskStarted
	case "task_completed":
		return TaskCompleted
	case "task_failed":
		return TaskFailed
	case "agent_started":
		return AgentStarted
	case "agent_restarted":
		return AgentRestarted
	case "agent_stopped":
		return AgentStopped
	case "epic_assigned":
		return EpicAssigned
	case "epic_exhausted":
		return EpicExhausted
	case "pr_created":
		return PRCreated
	case "conflict_resolved":
		return ConflictResolved
	case "health_check":
		return HealthCheck
	case "daemon_degraded":
		return DaemonDegraded
	case "config_reloaded":
		return ConfigReloaded
	default:
		return et
	}
}
