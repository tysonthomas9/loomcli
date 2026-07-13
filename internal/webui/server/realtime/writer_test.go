package realtime

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriterGoldenFrames(t *testing.T) {
	tests := []struct {
		name  string
		write func(*Writer) error
		want  string
	}{
		{
			name: "int64 event",
			write: func(sw *Writer) error {
				return sw.WriteEvent(42, "x", "y")
			},
			want: "id: 42\nevent: x\ndata: y\n\n",
		},
		{
			name: "opaque id event",
			write: func(sw *Writer) error {
				return sw.WriteEventID("1700000000100-0", "mutation", "{}")
			},
			want: "id: 1700000000100-0\nevent: mutation\ndata: {}\n\n",
		},
		{
			name: "id-less event",
			write: func(sw *Writer) error {
				return sw.WriteEventNoID("connected", `{"clientId":7}`)
			},
			want: "event: connected\ndata: {\"clientId\":7}\n\n",
		},
		{
			name: "comment",
			write: func(sw *Writer) error {
				return sw.WriteComment("heartbeat")
			},
			want: ": heartbeat\n\n",
		},
		{
			name: "retry",
			write: func(sw *Writer) error {
				return sw.WriteRetry(5000)
			},
			want: "retry: 5000\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			sw, err := NewWriter(rr)
			if err != nil {
				t.Fatalf("NewWriter() error = %v", err)
			}
			if err := tt.write(sw); err != nil {
				t.Fatalf("write error = %v", err)
			}
			if got := rr.Body.String(); got != tt.want {
				t.Fatalf("body = %q, want %q", got, tt.want)
			}
			if !rr.Flushed {
				t.Fatal("writer did not flush")
			}
		})
	}
}

func TestWriteEventNoIDOmitsIDLine(t *testing.T) {
	rr := httptest.NewRecorder()
	sw, err := NewWriter(rr)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	if err := sw.WriteEventNoID("connected", `{"clientId":7}`); err != nil {
		t.Fatalf("WriteEventNoID() error = %v", err)
	}
	if strings.Contains(rr.Body.String(), "id:") {
		t.Fatalf("WriteEventNoID emitted id line:\n%s", rr.Body.String())
	}
}
