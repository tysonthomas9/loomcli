package doctor

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestCheckStatusStringMarshalAndTally(t *testing.T) {
	if StatusPass.String() != "pass" || StatusWarn.String() != "warn" || StatusFail.String() != "fail" || CheckStatus(99).String() != "unknown" {
		t.Fatalf("unexpected status strings")
	}
	data, err := json.Marshal(StatusWarn)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	if string(data) != `"warn"` {
		t.Fatalf("status json = %s", data)
	}

	summary := tallyResults([]CheckResult{
		{Status: StatusPass},
		{Status: StatusWarn},
		{Status: StatusFail},
		{Status: CheckStatus(99)},
	})
	if summary.Pass != 1 || summary.Warn != 1 || summary.Fail != 1 {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestRenderDoctorHumanCoversAllStatuses(t *testing.T) {
	out := captureDoctorStdout(t, func() {
		renderDoctorHuman(DoctorOutput{
			Checks: []CheckResult{
				{Name: "git", Status: StatusPass, Summary: "git ok"},
				{Name: "tmux", Status: StatusWarn, Summary: "tmux missing", Detail: "install tmux\nretry"},
				{Name: "backend", Status: StatusFail, Summary: "backend missing"},
			},
			Summary: DoctorSummary{Pass: 1, Warn: 1, Fail: 1},
		})
	})
	for _, want := range []string{"Loom Doctor", "git ok", "tmux missing", "install tmux", "backend missing", "1 checks passed, 1 warnings, 1 failures"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func captureDoctorStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = orig
		_ = r.Close()
	})

	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return string(data)
}
