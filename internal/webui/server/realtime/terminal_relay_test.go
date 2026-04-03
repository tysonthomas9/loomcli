package realtime

import (
	"testing"

	"nhooyr.io/websocket"
)

func TestTruncateUTF8_ShortString(t *testing.T) {
	s := "hello"
	got := TruncateUTF8(s, 123)
	if got != s {
		t.Errorf("expected %q, got %q", s, got)
	}
}

func TestTruncateUTF8_ExactLimit(t *testing.T) {
	s := "abc"
	got := TruncateUTF8(s, 3)
	if got != "abc" {
		t.Errorf("expected %q, got %q", "abc", got)
	}
}

func TestTruncateUTF8_TakesTail(t *testing.T) {
	s := "abcdefghij" // 10 bytes
	got := TruncateUTF8(s, 5)
	if got != "fghij" {
		t.Errorf("expected %q, got %q", "fghij", got)
	}
}

func TestTruncateUTF8_MultibyteNotSplit(t *testing.T) {
	// UTF-8 representation of some multi-byte characters
	// Each character below is 3 bytes in UTF-8
	s := "\xe4\xb8\x96\xe7\x95\x8c" // "世界" (6 bytes total)

	// maxBytes=4 => take last 4 bytes => "\x95\x8c" would be invalid start...
	// The function takes tail then skips continuation bytes.
	// Last 4 bytes: \x95 \x8c \xe7... wait, let me recalculate.
	// s = [e4 b8 96 e7 95 8c], len=6
	// s[6-4:] = s[2:] = [96 e7 95 8c]
	// 96 = 10010110 => continuation byte (10xxxxxx), skip
	// e7 = 11100111 => start byte, stop
	// result = [e7 95 8c] = "界"
	got := TruncateUTF8(s, 4)
	if got != "\xe7\x95\x8c" { // "界"
		t.Errorf("expected single char, got %q (len %d)", got, len(got))
	}
}

func TestTruncateUTF8_AllContinuationBytes(t *testing.T) {
	// Edge case: if after truncation we only have continuation bytes
	// (shouldn't happen with valid UTF-8, but test the safety)
	s := "\xc0\x80\x80" // technically invalid UTF-8, but tests the loop
	got := TruncateUTF8(s, 2)
	// last 2 bytes: [80 80], both continuation bytes => skip all => empty
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestTruncateUTF8_EmptyString(t *testing.T) {
	got := TruncateUTF8("", 10)
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestCrashInfo_WSClose_Crashed(t *testing.T) {
	ci := CrashInfo{Crashed: true, Reason: "process died"}
	code, reason := ci.WSClose()
	if code != websocket.StatusCode(WSCloseBackendExited) {
		t.Errorf("expected status %d, got %d", WSCloseBackendExited, code)
	}
	if reason != "process died" {
		t.Errorf("expected 'process died', got %q", reason)
	}
}

func TestCrashInfo_WSClose_Normal(t *testing.T) {
	ci := CrashInfo{Crashed: false}
	code, reason := ci.WSClose()
	if code != websocket.StatusNormalClosure {
		t.Errorf("expected StatusNormalClosure, got %d", code)
	}
	if reason != "session detached" {
		t.Errorf("expected 'session detached', got %q", reason)
	}
}
