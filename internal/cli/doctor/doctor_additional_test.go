package doctor

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
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

func TestCollectDoctorChecksFleetAndFleetDBBranches(t *testing.T) {
	deps, _, _, _, _ := NewTestDeps(t)
	cmd := &cobra.Command{}
	cmd.SetContext(cli.WithDeps(context.Background(), deps))

	t.Setenv("LOOM_ISSUE_BACKEND", "fleet")
	fleetChecks := collectDoctorChecks(cmd)
	if len(fleetChecks) == 0 {
		t.Fatal("fleet checks empty")
	}

	t.Setenv("LOOM_ISSUE_BACKEND", "")
	fleetDBChecks := collectDoctorChecks(cmd)
	if len(fleetDBChecks) == 0 {
		t.Fatal("fleetdb checks empty")
	}
	if len(fleetChecks) != len(fleetDBChecks) {
		t.Fatalf("check counts differ fleet=%d fleetdb=%d", len(fleetChecks), len(fleetDBChecks))
	}

	first := fleetDBChecks[0]()
	if first.Name == "" {
		t.Fatalf("first doctor check did not run: %+v", first)
	}
}

func TestRunDoctorUsesCollectedChecksForJSONAndHumanOutput(t *testing.T) {
	oldCollect := collectDoctorChecksFn
	oldJSON := doctorJSON
	t.Cleanup(func() {
		collectDoctorChecksFn = oldCollect
		doctorJSON = oldJSON
	})

	collectDoctorChecksFn = func(*cobra.Command) []checkFunc {
		return []checkFunc{
			func() CheckResult { return CheckResult{Name: "skipless", Status: StatusPass, Summary: "pass check"} },
			func() CheckResult { return CheckResult{} },
			func() CheckResult { return CheckResult{Name: "bad", Status: StatusFail, Summary: "fail check"} },
		}
	}
	doctorJSON = true
	cmd := &cobra.Command{}
	out := captureDoctorStdout(t, func() {
		err := runDoctor(cmd, nil)
		if err == nil || !strings.Contains(err.Error(), "doctor found 1 failure") {
			t.Fatalf("runDoctor err = %v", err)
		}
	})
	if !strings.Contains(out, `"fail": 1`) || !strings.Contains(out, `"pass check"`) {
		t.Fatalf("json output = %s", out)
	}
	if !cmd.SilenceErrors {
		t.Fatal("runDoctor should silence cobra errors after failures")
	}

	collectDoctorChecksFn = func(*cobra.Command) []checkFunc {
		return []checkFunc{func() CheckResult {
			return CheckResult{Name: "warn", Status: StatusWarn, Summary: "warn check", Detail: "details"}
		}}
	}
	doctorJSON = false
	out = captureDoctorStdout(t, func() {
		if err := runDoctor(&cobra.Command{}, nil); err != nil {
			t.Fatalf("runDoctor human: %v", err)
		}
	})
	if !strings.Contains(out, "warn check") || !strings.Contains(out, "details") || !strings.Contains(out, "0 checks passed, 1 warnings, 0 failures") {
		t.Fatalf("human output = %s", out)
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
