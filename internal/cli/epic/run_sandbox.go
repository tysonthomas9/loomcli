package epic

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leaddispatch"
)

const sandboxDefaultRunner = "daytona-task-runner"

var sandboxSupportedEpicRunFlags = map[string]struct{}{
	"parent": {}, "max-concurrency": {}, "runner": {}, "detach": {},
}

var sandboxRejectedEpicRunFlags = map[string]string{
	"dry-run":               "the epic-runner payload is built server-side",
	"exclude-label":         "the epic-runner payload is built server-side",
	"stacked-pull-requests": "stacked pull requests need the host stack store and a local git checkout",
	"workflow":              "the occupant dispatch surface only runs the built-in epic-runner workflow",
	"worker-prefix":         "the epic-runner payload is built server-side",
	"interval-seconds":      "the epic-runner payload is built server-side",
	"node-id":               "worker placement is chosen server-side",
	"lead":                  "the lead identity is derived from the sandbox placement",
	"repo-url":              "the repository is derived server-side from the workspace repo record",
	"base-branch":           "the base branch is derived server-side from the workspace repo record",
	"open-pull-request":     "pull-request delivery is decided server-side",
}

func rejectHostOnlyEpicRunFlags(flags *pflag.FlagSet) error {
	names := make([]string, 0, len(sandboxRejectedEpicRunFlags))
	for name := range sandboxRejectedEpicRunFlags {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if flags.Changed(name) {
			return fmt.Errorf(
				"loom epic run --%s is not supported for a sandboxed lead (%s); run it from the host loom CLI",
				name, sandboxRejectedEpicRunFlags[name])
		}
	}
	return nil
}

func sandboxEpicRunner(flags *pflag.FlagSet) (string, error) {
	if !flags.Changed("runner") {
		return sandboxDefaultRunner, nil
	}
	runner := strings.TrimSpace(runRunner)
	if runner != sandboxDefaultRunner {
		return "", fmt.Errorf(
			"loom epic run --runner %q is not supported for a sandboxed lead; only %q may be dispatched from a sandbox",
			runner, sandboxDefaultRunner)
	}
	return runner, nil
}

type epicDispatchClient interface {
	DispatchEpicRun(ctx context.Context, req leaddispatch.EpicRunRequest) (leaddispatch.EpicRunDispatch, error)
	RunStatus(ctx context.Context, runID string) (leaddispatch.RunStatus, error)
}

func validateSandboxEpicRunFlags(flags *pflag.FlagSet) (maxConcurrency *int, err error) {
	if strings.TrimSpace(runParent) == "" {
		return nil, errors.New("--parent is required")
	}
	if !flags.Changed("max-concurrency") {
		return nil, nil
	}
	if runMaxConcurrency < 1 || runMaxConcurrency > 4 {
		return nil, fmt.Errorf("--max-concurrency must be between 1 and 4 for a sandboxed lead (got %d)", runMaxConcurrency)
	}
	value := runMaxConcurrency
	return &value, nil
}

type sandboxEpicRunInput struct {
	epicID         string
	runner         string
	maxConcurrency *int
	detach         bool
}

func validateSandboxEpicRun(flags *pflag.FlagSet) (sandboxEpicRunInput, error) {
	var in sandboxEpicRunInput
	if err := rejectHostOnlyEpicRunFlags(flags); err != nil {
		return in, err
	}
	runner, err := sandboxEpicRunner(flags)
	if err != nil {
		return in, err
	}
	maxConcurrency, err := validateSandboxEpicRunFlags(flags)
	if err != nil {
		return in, err
	}
	in = sandboxEpicRunInput{
		epicID: strings.TrimSpace(runParent), runner: runner,
		maxConcurrency: maxConcurrency, detach: runDetach,
	}
	return in, nil
}

func runEpicRunSandbox(ctx context.Context, cmd *cobra.Command) error {
	in, err := validateSandboxEpicRun(cmd.Flags())
	if err != nil {
		return err
	}
	client, err := leaddispatch.New()
	if err != nil {
		return err
	}
	return runEpicRunSandboxWithClient(ctx, in, client)
}

func runEpicRunSandboxWithClient(ctx context.Context, in sandboxEpicRunInput,
	client epicDispatchClient,
) error {
	dispatched, err := client.DispatchEpicRun(ctx, leaddispatch.EpicRunRequest{
		EpicID: in.epicID, MaxConcurrency: in.maxConcurrency, Runner: in.runner,
	})
	if err != nil {
		return err
	}
	fmt.Printf("[epic-run] queued workflow %s run %s for epic %s\n",
		dispatched.Workflow, dispatched.RunID, dispatched.EpicID)
	if in.detach {
		return nil
	}
	return waitForSandboxEpicRun(ctx, client, dispatched.RunID)
}

func waitForSandboxEpicRun(ctx context.Context, client epicDispatchClient, runID string) error {
	final, err := leaddispatch.Wait(ctx, runID, func(ctx context.Context) (leaddispatch.RunStatus, error) {
		return client.RunStatus(ctx, runID)
	}, leaddispatch.WaitOptions{})
	if err != nil {
		return err
	}
	fmt.Printf("[epic-run] workflow run %s finished: %s\n", runID, final.Status)
	if final.Summary != "" {
		fmt.Printf("[epic-run] %s\n", final.Summary)
	}
	if final.Status == string(domain.DriverRunFailed) {
		return fmt.Errorf("epic workflow run %s failed: %s", runID, final.Summary)
	}
	return nil
}
