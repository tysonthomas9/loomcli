package leadcontrol

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Harness lead runtime metadata keys, persisted on the lead's orchestration
// session alongside the shared lead_runtime_* keys from codex_metadata.go.
// The runtime provider value is the loom backend name (e.g. "claude").
const (
	MetadataHarnessName          = "lead_harness_name"
	MetadataHarnessChatSessionID = "lead_harness_chat_session_id"
	MetadataHarnessSessionID     = "lead_harness_session_id"
	MetadataHarnessPID           = "lead_harness_pid"
)

// HarnessRuntimeMetadata mirrors CodexRuntimeMetadata for leads supervised by
// the harness-wrapper PTY runtime. Unlike codex there is no dialable endpoint:
// delivery requires the in-process conversation registry.
type HarnessRuntimeMetadata struct {
	Provider         string
	HarnessName      string
	ChatSessionID    string
	HarnessSessionID string
	PID              int
	Status           string
	Controlled       bool
}

func HarnessRuntimeMetadataFromSession(session *domain.AgentSession) HarnessRuntimeMetadata {
	if session == nil {
		return HarnessRuntimeMetadata{}
	}
	m := session.Metadata
	pid, _ := strconv.Atoi(strings.TrimSpace(m[MetadataHarnessPID]))
	return HarnessRuntimeMetadata{
		Provider:         strings.TrimSpace(m[MetadataRuntimeProvider]),
		HarnessName:      strings.TrimSpace(m[MetadataHarnessName]),
		ChatSessionID:    strings.TrimSpace(m[MetadataHarnessChatSessionID]),
		HarnessSessionID: strings.TrimSpace(m[MetadataHarnessSessionID]),
		PID:              pid,
		Status:           strings.TrimSpace(m[MetadataRuntimeStatus]),
		Controlled:       strings.EqualFold(strings.TrimSpace(m[MetadataRuntimeControlled]), "true"),
	}
}

func UpdateHarnessRuntimeMetadata(ctx context.Context, st store.Store, workspace, sessionID string, runtime HarnessRuntimeMetadata) error {
	if st == nil || st.AgentSessions() == nil {
		return nil
	}
	workspace = strings.TrimSpace(workspace)
	sessionID = strings.TrimSpace(sessionID)
	if workspace == "" || sessionID == "" {
		return nil
	}
	session, err := st.AgentSessions().Get(ctx, workspace, sessionID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	metadata := cloneMetadata(session.Metadata)
	if runtime.Provider != "" {
		metadata[MetadataRuntimeProvider] = runtime.Provider
	}
	metadata[MetadataRuntimeControlled] = strconv.FormatBool(runtime.Controlled)
	if runtime.HarnessName != "" {
		metadata[MetadataHarnessName] = runtime.HarnessName
	}
	if runtime.ChatSessionID != "" {
		metadata[MetadataHarnessChatSessionID] = runtime.ChatSessionID
	}
	if runtime.HarnessSessionID != "" {
		metadata[MetadataHarnessSessionID] = runtime.HarnessSessionID
	}
	if runtime.PID > 0 {
		metadata[MetadataHarnessPID] = strconv.Itoa(runtime.PID)
	}
	if runtime.Status != "" {
		metadata[MetadataRuntimeStatus] = runtime.Status
		metadata[MetadataRuntimeStatusUpdated] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err = st.AgentSessions().Update(ctx, workspace, sessionID, store.AgentSessionUpdate{Metadata: &metadata})
	return err
}

func hasHarnessRuntimeMetadata(metadata map[string]string) bool {
	if len(metadata) == 0 {
		return false
	}
	for _, key := range []string{
		MetadataRuntimeProvider,
		MetadataRuntimeControlled,
		MetadataHarnessName,
		MetadataHarnessChatSessionID,
		MetadataHarnessPID,
	} {
		if strings.TrimSpace(metadata[key]) != "" {
			return true
		}
	}
	return false
}

// harnessRuntimeStatus maps a wrapper snapshot onto the shared RuntimeStatus
// vocabulary. An empty wrapper status means the session is producing output
// and has not been classified — treat as active.
func harnessRuntimeStatus(snap wrapper.Snapshot) string {
	switch snap.Status {
	case wrapper.StatusWaitingForInput:
		return RuntimeStatusWaitingUserInput
	case wrapper.StatusIdle, wrapper.StatusStale:
		return RuntimeStatusIdle
	case wrapper.StatusInterrupted:
		return RuntimeStatusWaitingUserInput
	case wrapper.StatusFailed, wrapper.StatusBlockedByCost, wrapper.StatusRetryLater,
		wrapper.StatusAPIError, wrapper.StatusBinaryNotFound:
		return RuntimeStatusFailed
	case "":
		return RuntimeStatusActive
	default:
		return string(snap.Status)
	}
}
