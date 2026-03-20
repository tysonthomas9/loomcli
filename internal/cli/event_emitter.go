package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/events"
)

// EventEmitter abstracts domain event emission.
// Local mode writes JSONL files; remote mode POSTs to the control plane.
type EventEmitter interface {
	Emit(event events.Event) error
	Close() error
}

// LocalEventEmitter wraps an events.Bus for local JSONL file writing.
type LocalEventEmitter struct {
	bus *events.Bus
}

// NewLocalEventEmitter creates an emitter that writes events to the given directory.
func NewLocalEventEmitter(eventsDir string) *LocalEventEmitter {
	return &LocalEventEmitter{
		bus: events.NewBus(eventsDir),
	}
}

func (e *LocalEventEmitter) Emit(event events.Event) error {
	return e.bus.Emit(event)
}

func (e *LocalEventEmitter) Close() error {
	return e.bus.Close()
}

// HTTPEventEmitter implements EventEmitter by POSTing events to the control plane.
// Used by remote workers running in containers.
type HTTPEventEmitter struct {
	ControlPlaneURL string
	WorkerID        string
	Token           string
	HTTPClient      *http.Client
}

func (e *HTTPEventEmitter) client() *http.Client {
	if e.HTTPClient != nil {
		return e.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (e *HTTPEventEmitter) Emit(event events.Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	url := fmt.Sprintf("%s/api/internal/workers/%s/events", e.ControlPlaneURL, e.WorkerID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.Token != "" {
		req.Header.Set("Authorization", "Bearer "+e.Token)
	}

	resp, err := e.client().Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("control plane returned %d for event", resp.StatusCode)
	}
	return nil
}

func (e *HTTPEventEmitter) Close() error {
	// No persistent state to clean up for HTTP emitter.
	return nil
}
