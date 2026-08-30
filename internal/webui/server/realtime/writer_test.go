package realtime

import (
	"bytes"
	"errors"
	"net/http"
	"testing"
)

func TestWriterFrames(t *testing.T) {
	tests := []struct {
		name  string
		write func(*Writer) error
		want  string
	}{
		{
			name: "integer ID event",
			write: func(sw *Writer) error {
				return sw.WriteEvent(42, "mutation", `{"ok":true}`)
			},
			want: "id: 42\nevent: mutation\ndata: {\"ok\":true}\n\n",
		},
		{
			name: "opaque ID event",
			write: func(sw *Writer) error {
				return sw.WriteEventID("1700000000800-0", "status", "ready")
			},
			want: "id: 1700000000800-0\nevent: status\ndata: ready\n\n",
		},
		{
			name: "ID-less event",
			write: func(sw *Writer) error {
				return sw.WriteEventNoID("connected", `{"clientId":1}`)
			},
			want: "event: connected\ndata: {\"clientId\":1}\n\n",
		},
		{
			name: "retry directive",
			write: func(sw *Writer) error {
				return sw.WriteRetry(5000)
			},
			want: "retry: 5000\n\n",
		},
		{
			name: "heartbeat comment",
			write: func(sw *Writer) error {
				return sw.WriteComment("heartbeat")
			},
			want: ": heartbeat\n\n",
		},
		{
			name: "multiline data accepts every line terminator",
			write: func(sw *Writer) error {
				return sw.WriteEventNoID("event", "one\r\ntwo\rthree\nfour")
			},
			want: "event: event\ndata: one\ndata: two\ndata: three\ndata: four\n\n",
		},
		{
			name: "multiline data preserves trailing and consecutive empty segments",
			write: func(sw *Writer) error {
				return sw.WriteEventNoID("event", "one\n\nthree\n")
			},
			want: "event: event\ndata: one\ndata: \ndata: three\ndata: \n\n",
		},
		{
			name: "multiline comment",
			write: func(sw *Writer) error {
				return sw.WriteComment("one\r\ntwo\rthree\nfour")
			},
			want: ": one\n: two\n: three\n: four\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rw := &recordingResponseWriter{}
			sw, err := NewWriter(rw)
			if err != nil {
				t.Fatalf("NewWriter() error = %v", err)
			}
			if err := tt.write(sw); err != nil {
				t.Fatalf("write frame error = %v", err)
			}
			if got := rw.String(); got != tt.want {
				t.Fatalf("frame bytes = %q, want %q", got, tt.want)
			}
			if rw.flushes != 1 {
				t.Fatalf("flush count = %d, want 1", rw.flushes)
			}
			if rw.writes != 1 {
				t.Fatalf("write count = %d, want 1", rw.writes)
			}
		})
	}
}

func TestWriterRejectsMalformedFields(t *testing.T) {
	tests := []struct {
		name  string
		write func(*Writer) error
	}{
		{
			name: "ID with line feed",
			write: func(sw *Writer) error {
				return sw.WriteEventID("one\ntwo", "event", "data")
			},
		},
		{
			name: "ID with carriage return",
			write: func(sw *Writer) error {
				return sw.WriteEventID("one\rtwo", "event", "data")
			},
		},
		{
			name: "ID with NUL",
			write: func(sw *Writer) error {
				return sw.WriteEventID("one\x00two", "event", "data")
			},
		},
		{
			name: "event with line feed",
			write: func(sw *Writer) error {
				return sw.WriteEventID("1", "one\ntwo", "data")
			},
		},
		{
			name: "event with carriage return",
			write: func(sw *Writer) error {
				return sw.WriteEventID("1", "one\rtwo", "data")
			},
		},
		{
			name: "negative retry",
			write: func(sw *Writer) error {
				return sw.WriteRetry(-1)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rw := &recordingResponseWriter{}
			sw, err := NewWriter(rw)
			if err != nil {
				t.Fatalf("NewWriter() error = %v", err)
			}
			if err := tt.write(sw); err == nil {
				t.Fatal("write frame error = nil, want validation error")
			}
			if got := rw.String(); got != "" {
				t.Fatalf("frame bytes = %q, want no write", got)
			}
			if rw.flushes != 0 {
				t.Fatalf("flush count = %d, want 0", rw.flushes)
			}
		})
	}
}

func TestWriterReturnsWriteFailureWithoutFlushing(t *testing.T) {
	writeErr := errors.New("write failed")
	rw := &recordingResponseWriter{writeErr: writeErr}
	sw, err := NewWriter(rw)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}

	err = sw.WriteEventNoID("event", "data")
	if !errors.Is(err, writeErr) {
		t.Fatalf("WriteEventNoID() error = %v, want %v", err, writeErr)
	}
	if rw.flushes != 0 {
		t.Fatalf("flush count = %d, want 0", rw.flushes)
	}
}

func TestNewWriterRejectsUnsupportedFlusher(t *testing.T) {
	if _, err := NewWriter(&nonFlushingResponseWriter{}); err == nil {
		t.Fatal("NewWriter() error = nil, want unsupported flusher error")
	}
}

type recordingResponseWriter struct {
	bytes.Buffer
	header   http.Header
	flushes  int
	writes   int
	writeErr error
}

func (w *recordingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *recordingResponseWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return w.Buffer.Write(p)
}

func (w *recordingResponseWriter) WriteHeader(int) {}

func (w *recordingResponseWriter) Flush() {
	w.flushes++
}

type nonFlushingResponseWriter struct {
	header http.Header
}

func (w *nonFlushingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (*nonFlushingResponseWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (*nonFlushingResponseWriter) WriteHeader(int) {}
