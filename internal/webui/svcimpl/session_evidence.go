package svcimpl

import (
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func enrichSessionRecordFromLocal(rec *sessions.SessionRecord, local sessions.SessionRecord) []service.SessionEvidenceConflict {
	var conflicts []service.SessionEvidenceConflict
	addConflict := func(field, existing, incoming string) {
		conflicts = append(conflicts, service.SessionEvidenceConflict{
			Field: field, ExistingSource: "control_plane", ExistingValue: existing,
			IncomingSource: "session_store", IncomingValue: incoming,
		})
	}
	if rec.TaskID != "" && local.TaskID != "" && rec.TaskID != local.TaskID {
		addConflict("task_id", rec.TaskID, local.TaskID)
	}
	if rec.Status != "" && local.Status != "" && rec.Status != local.Status {
		addConflict("status", string(rec.Status), string(local.Status))
	}
	if rec.TaskID == "" {
		rec.TaskID = local.TaskID
	}
	if rec.EpicID == "" {
		rec.EpicID = local.EpicID
	}
	if rec.Backend == "" {
		rec.Backend = local.Backend
	}
	if rec.Model == "" {
		rec.Model = local.Model
	}
	if rec.Phase == "" {
		rec.Phase = local.Phase
	}
	// Precedence is control-plane/TaskRun first, local file-store second.
	// Local metadata fills only missing zero-value fields so list and detail
	// agree without clobbering a projected TaskRun value with a local zero.
	reconcileNonZero("input_tokens", &rec.InputTokens, local.InputTokens, addConflict)
	reconcileNonZero("output_tokens", &rec.OutputTokens, local.OutputTokens, addConflict)
	reconcileNonZero("cache_read_tokens", &rec.CacheReadTokens, local.CacheReadTokens, addConflict)
	reconcileNonZero("cache_write_tokens", &rec.CacheWriteTokens, local.CacheWriteTokens, addConflict)
	reconcileNonZero("estimated_cost_usd", &rec.EstimatedCostUSD, local.EstimatedCostUSD, addConflict)
	reconcileNonZero("files_changed", &rec.FilesChanged, local.FilesChanged, addConflict)
	reconcileNonZero("lines_added", &rec.LinesAdded, local.LinesAdded, addConflict)
	reconcileNonZero("lines_removed", &rec.LinesRemoved, local.LinesRemoved, addConflict)
	reconcileStringSlice("files_touched", &rec.FilesTouched, local.FilesTouched, addConflict)
	if rec.ErrorClass == "" {
		rec.ErrorClass = local.ErrorClass
	}
	return conflicts
}

func reconcileNonZero[T comparable](field string, current *T, incoming T, addConflict func(string, string, string)) {
	var zero T
	if *current != zero && incoming != zero && *current != incoming {
		addConflict(field, fmt.Sprint(*current), fmt.Sprint(incoming))
		return
	}
	if *current == zero {
		*current = incoming
	}
}

func fillIfZero[T comparable](current *T, incoming T) {
	var zero T
	if *current == zero {
		*current = incoming
	}
}

func reconcileStringSlice(field string, current *[]string, incoming []string, addConflict func(string, string, string)) {
	if len(*current) > 0 && len(incoming) > 0 && !equalStrings(*current, incoming) {
		addConflict(field, strings.Join(*current, ","), strings.Join(incoming, ","))
		return
	}
	if len(*current) == 0 && len(incoming) > 0 {
		*current = append([]string(nil), incoming...)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func sessionEvidence(rec sessions.SessionRecord, conflicts []service.SessionEvidenceConflict) service.SessionEvidence {
	status := "ok"
	usageStatus := "unavailable"
	if rec.InputTokens != 0 || rec.OutputTokens != 0 || rec.CacheReadTokens != 0 || rec.CacheWriteTokens != 0 || rec.EstimatedCostUSD != 0 {
		usageStatus = "reported"
	}
	for _, conflict := range conflicts {
		status = "conflict"
		switch conflict.Field {
		case "input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "estimated_cost_usd":
			usageStatus = "conflict"
		}
	}
	if conflicts == nil {
		conflicts = []service.SessionEvidenceConflict{}
	}
	return service.SessionEvidence{Status: status, UsageStatus: usageStatus, Conflicts: conflicts}
}
