package realtime

import (
	"fmt"
	"io"
	"net/http"
)

// Writer centralizes SSE wire-format concerns.
type Writer struct {
	W       http.ResponseWriter
	Flusher http.Flusher
}

// NewWriter creates a new SSE writer, checking that the ResponseWriter supports Flusher.
func NewWriter(w http.ResponseWriter) (*Writer, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("ResponseWriter does not implement http.Flusher")
	}
	return &Writer{W: w, Flusher: flusher}, nil
}

// WriteRetry writes the retry interval to the SSE stream.
func (sw *Writer) WriteRetry(ms int) error {
	_, err := fmt.Fprintf(sw.W, "retry: %d\n\n", ms)
	sw.Flusher.Flush()
	return err
}

// WriteEvent writes a named event with data to the SSE stream.
func (sw *Writer) WriteEvent(id int64, event, data string) error {
	_, err := io.WriteString(sw.W, fmt.Sprintf("id: %d\nevent: %s\ndata: %s\n\n", id, event, data))
	sw.Flusher.Flush()
	return err
}

// WriteComment writes a comment to the SSE stream.
func (sw *Writer) WriteComment(text string) error {
	_, err := fmt.Fprintf(sw.W, ": %s\n\n", text)
	sw.Flusher.Flush()
	return err
}
