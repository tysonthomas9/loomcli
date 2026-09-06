package fleet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

type recoveryHistoryWire struct {
	IssueID  string             `json:"issue_id"`
	Present  *bool              `json:"present"`
	Events   *[]json.RawMessage `json:"events"`
	HasOlder *bool              `json:"has_older"`
	Timeline *[]json.RawMessage `json:"timeline"`
}

func validateRecoveryHistoryScope(data []byte, selected string) error {
	var root struct {
		History json.RawMessage `json:"history"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}
	if selected == "" {
		if !bytes.Equal(bytes.TrimSpace(root.History), []byte("null")) {
			return fmt.Errorf("unexpected selected history")
		}
		return nil
	}
	var history recoveryHistoryWire
	if err := json.Unmarshal(root.History, &history); err != nil {
		return err
	}
	if history.IssueID != selected {
		return fmt.Errorf("selected history echo mismatch")
	}
	return nil
}

func validateRecoveryHistory(raw json.RawMessage, ws string, issues map[string]map[string]json.RawMessage) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if len(fields) != 5 {
		return fmt.Errorf("invalid history fields")
	}
	for _, key := range []string{"issue_id", "present", "events", "has_older", "timeline"} {
		if fields[key] == nil {
			return fmt.Errorf("missing history field")
		}
	}
	var history recoveryHistoryWire
	if err := json.Unmarshal(raw, &history); err != nil {
		return err
	}
	if !backend.ValidRecoveryIssueSelection(history.IssueID) || history.Present == nil || history.Events == nil || history.HasOlder == nil || history.Timeline == nil {
		return fmt.Errorf("incomplete history")
	}
	present := issues[history.IssueID] != nil
	count := len(*history.Events)
	if !validRecoveryHistoryWindow(*history.Present, present, count, *history.HasOlder) {
		return fmt.Errorf("inconsistent history window")
	}
	var previous int64
	for _, event := range *history.Events {
		seq, err := validateRecoveryHistoryEvent(event, ws, history.IssueID)
		if err != nil {
			return err
		}
		if seq <= previous {
			return fmt.Errorf("history events not strictly ordered")
		}
		previous = seq
	}
	return validateRecoveryTimeline(*history.Timeline, *history.Events)
}

func validRecoveryHistoryWindow(reported, present bool, count int, older bool) bool {
	return reported == present && count <= 200 && (!older || count == 200) && (present || (count == 0 && !older))
}

func validateRecoveryHistoryEvent(raw json.RawMessage, ws, issueID string) (int64, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return 0, err
	}
	if len(fields) != 10 {
		return 0, fmt.Errorf("invalid history event fields")
	}
	values := make(map[string]string, 9)
	for _, key := range []string{"id", "workspace_id", "timestamp", "actor", "action", "entity_type", "entity_id", "before", "after"} {
		value, err := recoveryString(fields, key, key != "before" && key != "after")
		if err != nil {
			return 0, err
		}
		values[key] = value
	}
	major, ok := strings.CutSuffix(values["id"], "-0")
	seq, err := strconv.ParseInt(major, 10, 64)
	if !ok || err != nil || seq <= 0 || strconv.FormatInt(seq, 10) != major {
		return 0, fmt.Errorf("invalid history event ID")
	}
	if values["workspace_id"] != ws || values["entity_id"] != issueID || !validRecoveryTimestamp(values["timestamp"]) {
		return 0, fmt.Errorf("invalid history event scope or time")
	}
	entity, ok := recoveryHistoryActionEntities[values["action"]]
	if !ok || entity != values["entity_type"] {
		return 0, fmt.Errorf("invalid history event action")
	}
	var metadata map[string]*string
	if err := json.Unmarshal(fields["metadata"], &metadata); err != nil || metadata == nil {
		return 0, fmt.Errorf("invalid history metadata")
	}
	for _, value := range metadata {
		if value == nil {
			return 0, fmt.Errorf("null history metadata value")
		}
	}
	return seq, nil
}

// Checked against the generated Fleet action contract by the recovery tests.
var recoveryHistoryActionEntities = map[string]string{
	"agent.create":         "agent",
	"agent.delete":         "agent",
	"agent.update":         "agent",
	"comment.add":          "comment",
	"daemon.update":        "daemon_profile",
	"dep.add":              "dependency",
	"dep.remove":           "dependency",
	"driver_run.claim":     "driver_run",
	"driver_run.create":    "driver_run",
	"driver_run.finish":    "driver_run",
	"driver_run.heartbeat": "driver_run",
	"driver_run.recover":   "driver_run",
	"driver_run.resume":    "driver_run",
	"driver_run.suspend":   "driver_run",
	"issue.assign":         "issue",
	"issue.claim":          "issue",
	"issue.close":          "issue",
	"issue.create":         "issue",
	"issue.defer":          "issue",
	"issue.delete":         "issue",
	"issue.release":        "issue",
	"issue.reopen":         "issue",
	"issue.undefer":        "issue",
	"issue.update":         "issue",
	"label.add":            "label",
	"label.remove":         "label",
	"metadata.remove":      "metadata",
	"metadata.set":         "metadata",
	"repo.create":          "repo",
	"repo.delete":          "repo",
	"repo.update":          "repo",
	"role.create":          "role",
	"role.delete":          "role",
	"role.update":          "role",
	"skill.create":         "skill",
	"skill.delete":         "skill",
	"skill.update":         "skill",
	"skill_pack.create":    "skill_pack",
	"skill_pack.delete":    "skill_pack",
	"skill_pack.update":    "skill_pack",
	"workspace.create":     "workspace",
	"workspace.delete":     "workspace",
	"workspace.update":     "workspace",
}
