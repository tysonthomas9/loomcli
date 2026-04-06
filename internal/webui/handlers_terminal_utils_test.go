package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		max       int
		wantLen   func(string) bool // length constraint check
		wantUTF8  bool              // must be valid UTF-8
		want      string            // exact expected output
		wantExact bool              // when true, compare got == want exactly (including empty)
	}{
		{
			name:      "empty string",
			input:     "",
			max:       10,
			want:      "",
			wantExact: true,
			wantUTF8:  true,
		},
		{
			name:     "ASCII within limit",
			input:    "hello",
			max:      10,
			want:     "hello",
			wantUTF8: true,
		},
		{
			name:     "ASCII at limit",
			input:    "hello",
			max:      5,
			want:     "hello",
			wantUTF8: true,
		},
		{
			name:     "ASCII over limit returns tail",
			input:    "hello world",
			max:      5,
			want:     "world",
			wantUTF8: true,
		},
		{
			name:      "zero limit",
			input:     "hello",
			max:       0,
			want:      "",
			wantExact: true,
			wantUTF8:  true,
		},
		{
			name:     "limit of 1 on ASCII",
			input:    "abc",
			max:      1,
			want:     "c",
			wantUTF8: true,
		},
		{
			name:     "multi-byte UTF-8 no split 2-byte char",
			input:    "abc\u00e9\u00e9", // e-acute is 2 bytes each, total = 3 + 2 + 2 = 7
			max:      4,                 // tail 4 bytes = last 2-byte char + possibly split char before it
			wantUTF8: true,
			wantLen: func(s string) bool {
				return len(s) <= 4 && utf8.ValidString(s)
			},
		},
		{
			name:     "multi-byte UTF-8 no split 3-byte char",
			input:    "ab\u4e16\u754c", // Chinese chars, 3 bytes each, total = 2 + 3 + 3 = 8
			max:      5,                // tail 5 bytes could split the first Chinese char
			wantUTF8: true,
			wantLen: func(s string) bool {
				return len(s) <= 5 && utf8.ValidString(s)
			},
		},
		{
			name:     "emoji 4-byte chars",
			input:    "hi\U0001F600\U0001F601", // two 4-byte emojis, total = 2 + 4 + 4 = 10
			max:      6,                        // tail 6 bytes could split first emoji
			wantUTF8: true,
			wantLen: func(s string) bool {
				return len(s) <= 6 && utf8.ValidString(s)
			},
		},
		{
			name:     "emoji only within limit",
			input:    "\U0001F600",
			max:      4,
			want:     "\U0001F600",
			wantUTF8: true,
		},
		{
			name:      "emoji truncated when max less than char size",
			input:     "\U0001F600", // 4 bytes
			max:       3,            // cannot fit the emoji
			want:      "",           // all continuation bytes skipped
			wantExact: true,
			wantUTF8:  true,
		},
		{
			name:     "mixed ASCII and multi-byte tail preserved",
			input:    "hello\u00e9", // 5 + 2 = 7 bytes
			max:      7,
			want:     "hello\u00e9",
			wantUTF8: true,
		},
		{
			name:     "single 2-byte char at exact limit",
			input:    "\u00e9", // 2 bytes
			max:      2,
			want:     "\u00e9",
			wantUTF8: true,
		},
		{
			name:     "single 3-byte char at exact limit",
			input:    "\u4e16", // 3 bytes
			max:      3,
			want:     "\u4e16",
			wantUTF8: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := realtime.TruncateUTF8(tt.input, tt.max)

			if tt.wantUTF8 && !utf8.ValidString(got) {
				t.Errorf("result is not valid UTF-8: %q", got)
			}

			if tt.wantExact && got != tt.want {
				t.Errorf("realtime.TruncateUTF8(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
			} else if !tt.wantExact && tt.want != "" && got != tt.want {
				t.Errorf("realtime.TruncateUTF8(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
			}

			if tt.wantLen != nil && !tt.wantLen(got) {
				t.Errorf("realtime.TruncateUTF8(%q, %d) = %q (len=%d), failed length constraint",
					tt.input, tt.max, got, len(got))
			}

			if len(got) > len(tt.input) {
				t.Errorf("result longer than input: %q vs %q", got, tt.input)
			}
		})
	}
}

func TestTruncateUTF8_NeverSplitsRune(t *testing.T) {
	// Exhaustive check: build a string with 1/2/3/4-byte runes and try every maxBytes value.
	s := "A\u00e9\u4e16\U0001F600" // 1 + 2 + 3 + 4 = 10 bytes
	for max := 0; max <= len(s)+5; max++ {
		got := realtime.TruncateUTF8(s, max)
		if !utf8.ValidString(got) {
			t.Errorf("realtime.TruncateUTF8(%q, %d) = %q is invalid UTF-8", s, max, got)
		}
		if max >= 0 && len(got) > max && max < len(s) {
			t.Errorf("realtime.TruncateUTF8(%q, %d) = %q (len=%d) exceeds max", s, max, got, len(got))
		}
	}
}

func TestInjectTerminalContextBanner_Success(t *testing.T) {
	tc := TerminalContext{
		Stats: TerminalContextStats{
			Open:       5,
			InProgress: 2,
			Blocked:    1,
			Review:     1,
		},
		Agents: []TerminalAgentInfo{
			{Name: "agent-1", Status: "running"},
		},
		Tasks: TerminalContextTasks{
			NeedsPlanning:    2,
			ReadyToImplement: 3,
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tc)
	}))
	defer srv.Close()

	// Create a pipe to simulate the PTY.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error: %v", err)
	}
	defer r.Close()
	defer w.Close()

	session := &TerminalSession{
		PTY: w,
	}

	wsConfigFn := func() (*ops.WorkspaceData, error) {
		return &ops.WorkspaceData{Name: "test-project"}, nil
	}

	injectTerminalContextBanner(session, srv.URL, wsConfigFn)

	// Close writer so reader gets EOF.
	w.Close()

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	banner := string(buf[:n])

	checks := []string{
		"test-project",
		"5 open",
		"1 blocked",
		"agent-1 (running)",
		"2 need plans",
		"3 ready to implement",
	}
	for _, want := range checks {
		if !strings.Contains(banner, want) {
			t.Errorf("banner missing %q; got:\n%s", want, banner)
		}
	}
}

func TestInjectTerminalContextBanner_NilWorkspaceConfigFn(t *testing.T) {
	tc := TerminalContext{
		Stats: TerminalContextStats{Open: 1},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tc)
	}))
	defer srv.Close()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error: %v", err)
	}
	defer r.Close()
	defer w.Close()

	session := &TerminalSession{PTY: w}

	// nil workspaceConfigFn -- should use default workspace name.
	injectTerminalContextBanner(session, srv.URL, nil)

	w.Close()
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	banner := string(buf[:n])

	if !strings.Contains(banner, "(default)") {
		t.Errorf("expected banner to show (default) for nil workspace config; got:\n%s", banner)
	}
}

func TestInjectTerminalContextBanner_WorkspaceConfigError(t *testing.T) {
	tc := TerminalContext{
		Stats: TerminalContextStats{Open: 2},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tc)
	}))
	defer srv.Close()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error: %v", err)
	}
	defer r.Close()
	defer w.Close()

	session := &TerminalSession{PTY: w}

	// workspaceConfigFn returns an error -- banner should still be injected with empty workspace.
	wsConfigFn := func() (*ops.WorkspaceData, error) {
		return nil, os.ErrNotExist
	}

	injectTerminalContextBanner(session, srv.URL, wsConfigFn)

	w.Close()
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	banner := string(buf[:n])

	// Should still produce a banner, just without project name.
	if !strings.Contains(banner, "(default)") {
		t.Errorf("expected banner with (default) when workspace config errors; got:\n%s", banner)
	}
	if !strings.Contains(banner, "2 open") {
		t.Errorf("expected banner to include stats; got:\n%s", banner)
	}
}

func TestInjectTerminalContextBanner_FetchError(t *testing.T) {
	// Server that returns an error -- banner injection should be silently skipped.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error: %v", err)
	}
	defer r.Close()
	defer w.Close()

	session := &TerminalSession{PTY: w}

	injectTerminalContextBanner(session, srv.URL, nil)

	w.Close()
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)

	// Nothing should have been written to PTY.
	if n > 0 {
		t.Errorf("expected no banner on fetch error, but got %d bytes: %q", n, string(buf[:n]))
	}
}

func TestInjectTerminalContextBanner_ServerDown(t *testing.T) {
	// Unreachable server -- should not panic, banner silently skipped.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error: %v", err)
	}
	defer r.Close()
	defer w.Close()

	session := &TerminalSession{PTY: w}

	injectTerminalContextBanner(session, "http://127.0.0.1:1", nil)

	w.Close()
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)

	if n > 0 {
		t.Errorf("expected no banner when server is down, but got %d bytes: %q", n, string(buf[:n]))
	}
}
