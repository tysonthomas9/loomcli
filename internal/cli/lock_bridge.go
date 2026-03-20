package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// LockBridge abstracts agent lock file operations.
// Local mode uses filesystem; remote mode uses HTTP to the control plane.
type LockBridge interface {
	UpdateState(agentName, state string) error
	UpdateTask(agentName, taskID, title string) error
	ClearTaskID(agentName string) error
	ReadLock(agentName string) (*LockInfo, error)
}

// AgentLockInfo is the subset of LockInfo used by the bridge interface.
// (LockInfo is the full lock file struct defined in lock.go.)
type AgentLockInfo = LockInfo

// LocalLockBridge implements LockBridge using the local filesystem.
// It delegates to the existing lock file functions in lock.go.
type LocalLockBridge struct {
	WorktreePath string
}

func (b *LocalLockBridge) UpdateState(agentName, state string) error {
	return UpdateLockState(b.WorktreePath, state)
}

func (b *LocalLockBridge) UpdateTask(agentName, taskID, title string) error {
	return UpdateLockTask(b.WorktreePath, taskID, title)
}

func (b *LocalLockBridge) ClearTaskID(agentName string) error {
	return ClearLockTaskID(b.WorktreePath)
}

func (b *LocalLockBridge) ReadLock(agentName string) (*LockInfo, error) {
	return ReadLockFile(b.WorktreePath)
}

// HTTPLockBridge implements LockBridge by POSTing to the control plane.
// Used by remote workers running in containers.
type HTTPLockBridge struct {
	ControlPlaneURL string
	WorkerID        string
	Token           string
	HTTPClient      *http.Client
}

// lockStateRequest is the JSON body for state updates.
type lockStateRequest struct {
	State     string `json:"state"`
	AgentName string `json:"agent_name"`
	TaskID    string `json:"task_id,omitempty"`
	TaskTitle string `json:"task_title,omitempty"`
	Action    string `json:"action"` // "update_state", "update_task", "clear_task", "read"
}

func (b *HTTPLockBridge) client() *http.Client {
	if b.HTTPClient != nil {
		return b.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (b *HTTPLockBridge) doRequest(body lockStateRequest) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal lock request: %w", err)
	}

	url := fmt.Sprintf("%s/api/internal/workers/%s/state", b.ControlPlaneURL, b.WorkerID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if b.Token != "" {
		req.Header.Set("Authorization", "Bearer "+b.Token)
	}

	resp, err := b.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", url, err)
	}
	return resp, nil
}

func (b *HTTPLockBridge) UpdateState(agentName, state string) error {
	resp, err := b.doRequest(lockStateRequest{
		Action:    "update_state",
		AgentName: agentName,
		State:     state,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("control plane returned %d for state update", resp.StatusCode)
	}
	return nil
}

func (b *HTTPLockBridge) UpdateTask(agentName, taskID, title string) error {
	resp, err := b.doRequest(lockStateRequest{
		Action:    "update_task",
		AgentName: agentName,
		TaskID:    taskID,
		TaskTitle: title,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("control plane returned %d for task update", resp.StatusCode)
	}
	return nil
}

func (b *HTTPLockBridge) ClearTaskID(agentName string) error {
	resp, err := b.doRequest(lockStateRequest{
		Action:    "clear_task",
		AgentName: agentName,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("control plane returned %d for clear task", resp.StatusCode)
	}
	return nil
}

func (b *HTTPLockBridge) ReadLock(agentName string) (*LockInfo, error) {
	resp, err := b.doRequest(lockStateRequest{
		Action:    "read",
		AgentName: agentName,
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("control plane returned %d for read lock", resp.StatusCode)
	}

	var info LockInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode lock info: %w", err)
	}
	return &info, nil
}
