package epic

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/tysonthomas9/loomcli/internal/leaddispatch"
	"github.com/tysonthomas9/loomcli/internal/leadoccupant"
)

func TestSandboxEpicRunFlagCoverage(t *testing.T) {
	seen := 0
	epicRunCmd.Flags().VisitAll(func(flag *pflag.Flag) {
		_, supported := sandboxSupportedEpicRunFlags[flag.Name]
		_, rejected := sandboxRejectedEpicRunFlags[flag.Name]
		if supported == rejected {
			t.Errorf("flag --%s classified supported=%t rejected=%t", flag.Name, supported, rejected)
		}
		seen++
	})
	if want := len(sandboxSupportedEpicRunFlags) + len(sandboxRejectedEpicRunFlags); seen != want {
		t.Fatalf("classified flags = %d, want %d", seen, want)
	}
}

func TestRejectHostOnlyEpicRunFlags(t *testing.T) {
	for name := range sandboxRejectedEpicRunFlags {
		t.Run(name, func(t *testing.T) {
			resetEpicRunFlags(t)
			setEpicRunFlag(t, name, changedEpicRunFlagValue(name))
			err := rejectHostOnlyEpicRunFlags(epicRunCmd.Flags())
			if err == nil || !strings.Contains(err.Error(), "--"+name) ||
				!strings.Contains(err.Error(), "not supported for a sandboxed lead") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestSandboxEpicRunnerDefaultsToDaytona(t *testing.T) {
	resetEpicRunFlags(t)
	if got, err := sandboxEpicRunner(epicRunCmd.Flags()); err != nil || got != sandboxDefaultRunner {
		t.Fatalf("default runner = %q, err = %v", got, err)
	}
	setEpicRunFlag(t, "runner", sandboxDefaultRunner)
	if got, err := sandboxEpicRunner(epicRunCmd.Flags()); err != nil || got != sandboxDefaultRunner {
		t.Fatalf("explicit runner = %q, err = %v", got, err)
	}
	resetEpicRunFlags(t)
	setEpicRunFlag(t, "runner", "local-task-runner")
	if _, err := sandboxEpicRunner(epicRunCmd.Flags()); err == nil {
		t.Fatal("local-task-runner was accepted")
	}
}

func TestSandboxSupportedFlagsAreAccepted(t *testing.T) {
	resetEpicRunFlags(t)
	setEpicRunFlag(t, "parent", "epic-1")
	setEpicRunFlag(t, "max-concurrency", "3")
	setEpicRunFlag(t, "runner", sandboxDefaultRunner)
	setEpicRunFlag(t, "detach", "true")
	if err := rejectHostOnlyEpicRunFlags(epicRunCmd.Flags()); err != nil {
		t.Fatalf("supported flags rejected: %v", err)
	}
}

func TestSandboxEpicRunFinishContract(t *testing.T) {
	tests := []struct {
		status  string
		summary string
		wantErr string
	}{
		{"completed", "all done", ""},
		{"failed", "worker failed", "epic workflow run run-1 failed: worker failed"},
		{"cancelled", "", ""},
		{"needs_review", "review it", ""},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			client := &fakeEpicDispatchClient{
				dispatch: leaddispatch.EpicRunDispatch{RunID: "run-1", Workflow: "epic-runner", EpicID: "epic-1", Status: "queued"},
				statuses: []leaddispatch.RunStatus{{RunID: "run-1", Status: tt.status, Terminal: true, Summary: tt.summary}},
			}
			var runErr error
			out := captureEpicStdout(t, func() {
				runErr = runEpicRunSandboxWithClient(context.Background(), sandboxEpicRunInput{
					epicID: "epic-1", runner: sandboxDefaultRunner,
				}, client)
			})
			want := "[epic-run] queued workflow epic-runner run run-1 for epic epic-1\n" +
				"[epic-run] run run-1 status: " + tt.status + "\n" +
				"[epic-run] workflow run run-1 finished: " + tt.status + "\n"
			if tt.summary != "" {
				want += "[epic-run] " + tt.summary + "\n"
			}
			if out != want {
				t.Fatalf("stdout = %q, want %q", out, want)
			}
			if tt.wantErr == "" && runErr != nil {
				t.Fatalf("error = %v, want nil", runErr)
			}
			if tt.wantErr != "" && (runErr == nil || runErr.Error() != tt.wantErr) {
				t.Fatalf("error = %v, want %q", runErr, tt.wantErr)
			}
		})
	}
}

func TestSandboxEpicRunDetach(t *testing.T) {
	client := &fakeEpicDispatchClient{dispatch: leaddispatch.EpicRunDispatch{
		RunID: "run-1", Workflow: "epic-runner", EpicID: "epic-1", Status: "queued",
	}}
	var runErr error
	out := captureEpicStdout(t, func() {
		runErr = runEpicRunSandboxWithClient(context.Background(), sandboxEpicRunInput{
			epicID: "epic-1", runner: sandboxDefaultRunner, detach: true,
		}, client)
	})
	if runErr != nil {
		t.Fatalf("run: %v", runErr)
	}
	if want := "[epic-run] queued workflow epic-runner run run-1 for epic epic-1\n"; out != want {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
	if client.statusCalls != 0 {
		t.Fatalf("RunStatus calls = %d, want 0", client.statusCalls)
	}
}

func TestValidateSandboxEpicRunFlags(t *testing.T) {
	resetEpicRunFlags(t)
	if _, err := validateSandboxEpicRunFlags(epicRunCmd.Flags()); err == nil {
		t.Fatal("blank parent accepted")
	}
	setEpicRunFlag(t, "parent", "epic-1")
	if got, err := validateSandboxEpicRunFlags(epicRunCmd.Flags()); err != nil || got != nil {
		t.Fatalf("unset concurrency = %v, err = %v", got, err)
	}
	for _, value := range []string{"0", "-1", "5"} {
		resetEpicRunFlags(t)
		setEpicRunFlag(t, "parent", "epic-1")
		setEpicRunFlag(t, "max-concurrency", value)
		if _, err := validateSandboxEpicRunFlags(epicRunCmd.Flags()); err == nil || !strings.Contains(err.Error(), "between 1 and 4") {
			t.Errorf("value %s error = %v", value, err)
		}
	}
	for _, value := range []string{"1", "4"} {
		resetEpicRunFlags(t)
		setEpicRunFlag(t, "parent", "epic-1")
		setEpicRunFlag(t, "max-concurrency", value)
		got, err := validateSandboxEpicRunFlags(epicRunCmd.Flags())
		if err != nil || got == nil || *got != int(value[0]-'0') {
			t.Errorf("value %s = %v, err = %v", value, got, err)
		}
	}
	resetEpicRunFlags(t)
	setEpicRunFlag(t, "parent", "epic-1")
	setEpicRunFlag(t, "max-concurrency", "4")
	in, err := validateSandboxEpicRun(epicRunCmd.Flags())
	if err != nil {
		t.Fatal(err)
	}
	in.detach = true
	client := &fakeEpicDispatchClient{dispatch: leaddispatch.EpicRunDispatch{
		RunID: "run-1", Workflow: "epic-runner", EpicID: "epic-1",
	}}
	_ = captureEpicStdout(t, func() {
		if err := runEpicRunSandboxWithClient(context.Background(), in, client); err != nil {
			t.Fatal(err)
		}
	})
	if len(client.requests) != 1 || client.requests[0].MaxConcurrency == nil ||
		*client.requests[0].MaxConcurrency != 4 {
		t.Fatalf("dispatch requests = %+v, explicit concurrency pointer was lost", client.requests)
	}
}

func TestSandboxEpicRunCommandOrdersValidationBeforeEnvironment(t *testing.T) {
	t.Setenv(leadoccupant.EnvOccupantToken, "token")
	t.Setenv(leadoccupant.EnvLeadAPIURL, "")
	t.Setenv(leadoccupant.EnvWorkspace, "WS")

	t.Run("workflow rejected before missing parent", func(t *testing.T) {
		resetEpicRunFlags(t)
		setEpicRunFlag(t, "workflow", "other")
		if err := epicRunCmd.ValidateRequiredFlags(); err != nil {
			t.Fatalf("cobra required-flag validation ran before sandbox classification: %v", err)
		}
		err := runEpicRun(epicRunCmd, nil)
		if err == nil || !strings.Contains(err.Error(), "--workflow") || strings.Contains(err.Error(), "--parent is required") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("host flag rejected before partial env", func(t *testing.T) {
		resetEpicRunFlags(t)
		setEpicRunFlag(t, "parent", "epic-1")
		setEpicRunFlag(t, "dry-run", "true")
		err := runEpicRun(epicRunCmd, nil)
		if err == nil || !strings.Contains(err.Error(), "--dry-run") || strings.Contains(err.Error(), "environment incomplete") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestHostEpicRunStillRequiresParent(t *testing.T) {
	t.Setenv(leadoccupant.EnvOccupantToken, "")
	resetEpicRunFlags(t)
	err := runEpicRun(epicRunCmd, nil)
	if err == nil || err.Error() != "--parent is required" {
		t.Fatalf("error = %v", err)
	}
}

type fakeEpicDispatchClient struct {
	dispatch    leaddispatch.EpicRunDispatch
	statuses    []leaddispatch.RunStatus
	statusCalls int
	requests    []leaddispatch.EpicRunRequest
}

func (f *fakeEpicDispatchClient) DispatchEpicRun(_ context.Context, req leaddispatch.EpicRunRequest) (leaddispatch.EpicRunDispatch, error) {
	f.requests = append(f.requests, req)
	return f.dispatch, nil
}

func (f *fakeEpicDispatchClient) RunStatus(context.Context, string) (leaddispatch.RunStatus, error) {
	f.statusCalls++
	status := f.statuses[0]
	f.statuses = f.statuses[1:]
	return status, nil
}

func resetEpicRunFlags(t *testing.T) {
	t.Helper()
	epicRunCmd.SetContext(context.Background())
	epicRunCmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if err := flag.Value.Set(flag.DefValue); err != nil {
			t.Fatalf("reset --%s: %v", flag.Name, err)
		}
		flag.Changed = false
	})
	t.Cleanup(func() {
		epicRunCmd.Flags().VisitAll(func(flag *pflag.Flag) {
			_ = flag.Value.Set(flag.DefValue)
			flag.Changed = false
		})
	})
}

func setEpicRunFlag(t *testing.T, name, value string) {
	t.Helper()
	if err := epicRunCmd.Flags().Set(name, value); err != nil {
		t.Fatalf("set --%s: %v", name, err)
	}
}

func changedEpicRunFlagValue(name string) string {
	switch name {
	case "dry-run", "stacked-pull-requests", "open-pull-request":
		return "true"
	case "interval-seconds":
		return "6"
	default:
		return "x"
	}
}

func captureEpicStdout(t *testing.T, run func()) string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = write
	run()
	_ = write.Close()
	os.Stdout = original
	raw, err := io.ReadAll(read)
	_ = read.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
