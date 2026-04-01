package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// sessionNotifyPayload is the JSON body sent to the web UI notification endpoint.
type sessionNotifyPayload struct {
	TaskID    string `json:"task_id"`
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

// NotifyWebUI sends a fire-and-forget POST to the web UI so it can broadcast
// a session_change SSE event to connected clients. If serverURL is empty the
// call is a no-op. Errors are logged to stderr but never returned.
// notifyToken is included as a Bearer token in the Authorization header when non-empty.
func NotifyWebUI(ctx context.Context, serverURL, taskID, sessionID string, status SessionStatus, notifyToken string) {
	if serverURL == "" {
		return
	}

	payload := sessionNotifyPayload{
		TaskID:    taskID,
		SessionID: sessionID,
		Status:    string(status),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("sessions.NotifyWebUI: marshal error: %v", err)
		return
	}

	notifyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/api/sessions/notify", serverURL)
	req, err := http.NewRequestWithContext(notifyCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("sessions.NotifyWebUI: request error: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if notifyToken != "" {
		req.Header.Set("Authorization", "Bearer "+notifyToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("sessions.NotifyWebUI: POST %s failed: %v", url, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		log.Printf("sessions.NotifyWebUI: POST %s returned %d", url, resp.StatusCode)
	}
}
