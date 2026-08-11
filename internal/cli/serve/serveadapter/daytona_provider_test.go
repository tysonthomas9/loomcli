package serveadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/localsettings"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

func validDaytonaBrokerCommand() execution.DaytonaProviderCommand {
	return execution.DaytonaProviderCommand{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-1",
		WorkItemID:   "TASK-1",
		DriverRunID:  "driver-run-1",
		Intent: execution.DaytonaProviderIntent{
			SchemaVersion: execution.DaytonaProviderSchemaV1,
			RepositoryURL: "https://github.com/octocat/Hello-World.git",
			TaskPrompt:    "Make a focused change.",
			Backend:       "codex",
			Delivery:      execution.DaytonaProviderDelivery{},
		},
	}
}

func saveDaytonaBrokerCredentials(t *testing.T, dir, daytona, github string) {
	t.Helper()
	settings := localsettings.Default()
	var err error
	settings.RuntimeCredentials.Daytona, err = localsettings.SealRuntimeCredential(
		dir,
		localsettings.RuntimeCredentialProviderDaytona,
		daytona,
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if github != "" {
		settings.RuntimeCredentials.GitHub, err = localsettings.SealRuntimeCredential(
			dir,
			localsettings.RuntimeCredentialProviderGitHub,
			github,
			time.Now(),
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := localsettings.Save(dir, settings); err != nil {
		t.Fatal(err)
	}
}

func TestDaytonaProviderBrokerResolvesCredentialsOnlyAtHostBoundary(t *testing.T) {
	dir := t.TempDir()
	saveDaytonaBrokerCredentials(t, dir, "daytona-secret", "")
	var gotCommand execution.DaytonaProviderCommand
	var gotDaytona, gotGitHub []byte
	broker := &DaytonaProviderBroker{
		dataDir:       dir,
		serverPath:    "/fake/server.mjs",
		sdkImportPath: "/fake/daytona/esm/index.js",
		invoke: func(_ context.Context, opts driver.DaytonaProviderHostOptions) (execution.DaytonaProviderResult, error) {
			gotCommand = opts.Command
			gotDaytona = append([]byte(nil), opts.DaytonaCredential...)
			gotGitHub = append([]byte(nil), opts.GitHubCredential...)
			return execution.DaytonaProviderResult{
				SchemaVersion: execution.DaytonaProviderSchemaV1,
				Status:        "completed",
				Sandbox: execution.DaytonaSandboxReceipt{
					Provider: "daytona",
					ID:       "opaque",
					WorkDir:  "/home/daytona",
					CWD:      "/tmp/loom-daytona-task-repo",
					RepoRef:  "abc123",
				},
			}, nil
		},
	}
	result, err := broker.ExecuteDaytona(context.Background(), validDaytonaBrokerCommand())
	if err != nil {
		t.Fatalf("ExecuteDaytona: %v", err)
	}
	if result.Status != "completed" || gotCommand.TaskRunID != "task-run-1" {
		t.Fatalf("result=%+v command=%+v", result, gotCommand)
	}
	if string(gotDaytona) != "daytona-secret" || len(gotGitHub) != 0 {
		t.Fatalf("host credentials daytona=%q github=%q", gotDaytona, gotGitHub)
	}
}

func TestDaytonaProviderBrokerFailsClosedForCredentialsAndDemoModes(t *testing.T) {
	command := validDaytonaBrokerCommand()
	t.Run("missing Daytona credential", func(t *testing.T) {
		broker := &DaytonaProviderBroker{dataDir: t.TempDir(), invoke: driver.RunDaytonaProviderHost}
		if _, err := broker.ExecuteDaytona(context.Background(), command); !errors.Is(err, execution.ErrUnavailable) {
			t.Fatalf("error = %v, want execution unavailable", err)
		}
	})
	t.Run("pull request requires GitHub credential", func(t *testing.T) {
		dir := t.TempDir()
		saveDaytonaBrokerCredentials(t, dir, "daytona-secret", "")
		broker := &DaytonaProviderBroker{
			dataDir: dir, serverPath: "/fake/server.mjs", sdkImportPath: "/fake/sdk.js",
			invoke: driver.RunDaytonaProviderHost,
		}
		withPR := command
		withPR.Intent.Delivery.OpenPullRequest = true
		if _, err := broker.ExecuteDaytona(context.Background(), withPR); !errors.Is(err, execution.ErrUnavailable) {
			t.Fatalf("error = %v, want execution unavailable", err)
		}
	})
	t.Run("demo mode is server gated", func(t *testing.T) {
		dir := t.TempDir()
		saveDaytonaBrokerCredentials(t, dir, "daytona-secret", "")
		broker := &DaytonaProviderBroker{
			dataDir: dir, serverPath: "/fake/server.mjs", sdkImportPath: "/fake/sdk.js",
			invoke: driver.RunDaytonaProviderHost,
		}
		demo := command
		demo.Intent.Mode = "e2e-smoke"
		t.Setenv("LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES", "0")
		if _, err := broker.ExecuteDaytona(context.Background(), demo); !errors.Is(err, execution.ErrUnavailable) {
			t.Fatalf("error = %v, want execution unavailable", err)
		}
	})
}
