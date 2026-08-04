package sessions

import "testing"

func TestNotifyPath_Value(t *testing.T) {
	const want = "/api/sessions/notify"
	if NotifyPath != want {
		t.Errorf("NotifyPath = %q, want %q", NotifyPath, want)
	}
}
