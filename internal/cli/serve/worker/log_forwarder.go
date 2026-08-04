package worker

import (
	"bytes"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// LogForwarder wraps an io.Writer and forwards chunks to the control plane via HTTP.
// It buffers output and flushes periodically or when the buffer reaches a threshold.
type LogForwarder struct {
	controlPlaneURL string
	workerID        string
	token           string
	httpClient      *http.Client

	mu        sync.Mutex
	buffer    []byte
	flushStop chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

const (
	logForwarderFlushInterval = 2 * time.Second
	logForwarderBufferSize    = 8192 // flush when buffer reaches this size
)

// NewLogForwarder creates a LogForwarder that POSTs log chunks to the control plane.
func NewLogForwarder(controlPlaneURL, workerID, token string) *LogForwarder {
	lf := &LogForwarder{
		controlPlaneURL: controlPlaneURL,
		workerID:        workerID,
		token:           token,
		httpClient:      &http.Client{Timeout: 10 * time.Second},
		buffer:          make([]byte, 0, logForwarderBufferSize),
		flushStop:       make(chan struct{}),
		done:            make(chan struct{}),
	}

	go lf.periodicFlush()
	return lf
}

// Write implements io.Writer. It buffers output and flushes when the buffer is full.
func (lf *LogForwarder) Write(p []byte) (int, error) {
	lf.mu.Lock()
	lf.buffer = append(lf.buffer, p...)
	shouldFlush := len(lf.buffer) >= logForwarderBufferSize
	lf.mu.Unlock()

	if shouldFlush {
		lf.Flush()
	}

	return len(p), nil
}

// Flush sends any buffered data to the control plane.
func (lf *LogForwarder) Flush() {
	lf.mu.Lock()
	if len(lf.buffer) == 0 {
		lf.mu.Unlock()
		return
	}
	data := make([]byte, len(lf.buffer))
	copy(data, lf.buffer)
	lf.buffer = lf.buffer[:0]
	lf.mu.Unlock()

	lf.send(data)
}

// Close flushes remaining data and stops the periodic flush goroutine.
// Safe to call multiple times.
func (lf *LogForwarder) Close() error {
	lf.closeOnce.Do(func() {
		close(lf.flushStop)
		<-lf.done
		lf.Flush() // final flush
	})
	return nil
}

func (lf *LogForwarder) periodicFlush() {
	defer close(lf.done)
	ticker := time.NewTicker(logForwarderFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			lf.Flush()
		case <-lf.flushStop:
			return
		}
	}
}

func (lf *LogForwarder) send(data []byte) {
	url := fmt.Sprintf("%s/api/internal/workers/%s/logs", lf.controlPlaneURL, lf.workerID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return // best-effort
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if lf.token != "" {
		req.Header.Set("Authorization", "Bearer "+lf.token)
	}

	resp, err := lf.httpClient.Do(req)
	if err != nil {
		return // best-effort
	}
	resp.Body.Close()
}
