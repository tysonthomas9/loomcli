package serveadapter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/driver"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/infra/workflowdistribution"
	"github.com/tysonthomas9/loomcli/internal/localsettings"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

type daytonaProviderInvoker func(
	context.Context,
	driver.DaytonaProviderHostOptions,
) (execution.DaytonaProviderResult, error)

// DaytonaProviderBroker is the host-owned implementation of Execution's narrow
// Daytona provider port. It alone resolves sealed provider credentials and the
// provider SDK; TaskRun workflows receive only opaque receipts.
type DaytonaProviderBroker struct {
	dataDir string
	invoke  daytonaProviderInvoker

	mu            sync.Mutex
	serverPath    string
	sdkImportPath string
}

func NewDaytonaProviderBroker(dataDir string) *DaytonaProviderBroker {
	return &DaytonaProviderBroker{
		dataDir: strings.TrimSpace(dataDir),
		invoke:  driver.RunDaytonaProviderHost,
	}
}

//nolint:funlen // Provider execution validates the complete credential-free request and maps one typed result at the broker boundary.
func (broker *DaytonaProviderBroker) ExecuteDaytona(
	ctx context.Context,
	command execution.DaytonaProviderCommand,
) (execution.DaytonaProviderResult, error) {
	if broker == nil || broker.invoke == nil || broker.dataDir == "" {
		return execution.DaytonaProviderResult{}, fmt.Errorf("%w: Daytona provider broker is not configured", execution.ErrUnavailable)
	}
	if err := validateDaytonaBrokerCommand(command); err != nil {
		return execution.DaytonaProviderResult{}, err
	}
	if isDaytonaDemoMode(command.Intent.Mode) &&
		strings.TrimSpace(os.Getenv("LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES")) != "1" {
		return execution.DaytonaProviderResult{}, fmt.Errorf(
			"%w: Daytona demo mode %q is disabled",
			execution.ErrUnavailable,
			strings.TrimSpace(command.Intent.Mode),
		)
	}
	settings, err := localsettings.Load(broker.dataDir)
	if err != nil {
		return execution.DaytonaProviderResult{}, fmt.Errorf("%w: load local runtime settings: %v", execution.ErrUnavailable, err)
	}
	daytonaCredential, err := localsettings.UnsealRuntimeCredentialBytes(
		broker.dataDir,
		settings,
		localsettings.RuntimeCredentialProviderDaytona,
	)
	if err != nil {
		return execution.DaytonaProviderResult{}, fmt.Errorf("%w: saved Daytona credential is required", execution.ErrUnavailable)
	}
	defer zeroDaytonaCredential(daytonaCredential)

	var githubCredential []byte
	if strings.TrimSpace(settings.RuntimeCredentials.GitHub.Sealed) != "" {
		githubCredential, err = localsettings.UnsealRuntimeCredentialBytes(
			broker.dataDir,
			settings,
			localsettings.RuntimeCredentialProviderGitHub,
		)
		if err != nil {
			return execution.DaytonaProviderResult{}, fmt.Errorf("%w: saved GitHub credential is unusable", execution.ErrUnavailable)
		}
		defer zeroDaytonaCredential(githubCredential)
	}
	if command.Intent.Delivery.OpenPullRequest && len(githubCredential) == 0 {
		return execution.DaytonaProviderResult{}, fmt.Errorf("%w: saved GitHub credential is required for pull-request delivery", execution.ErrUnavailable)
	}

	serverPath, sdkImportPath, err := broker.runtime(ctx)
	if err != nil {
		return execution.DaytonaProviderResult{}, err
	}
	result, err := broker.invoke(ctx, driver.DaytonaProviderHostOptions{
		ServerPath:        serverPath,
		DaytonaSDKImport:  sdkImportPath,
		Command:           command,
		DaytonaCredential: daytonaCredential,
		GitHubCredential:  githubCredential,
	})
	if err != nil {
		return execution.DaytonaProviderResult{}, fmt.Errorf("%w: Daytona provider execution failed: %v", execution.ErrUnavailable, err)
	}
	return result, nil
}

func (broker *DaytonaProviderBroker) runtime(ctx context.Context) (string, string, error) {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if broker.serverPath != "" && broker.sdkImportPath != "" {
		return broker.serverPath, broker.sdkImportPath, nil
	}
	sdkRoot, err := workflowdefs.ResolveDaytonaSDKRoot()
	if err != nil {
		return "", "", fmt.Errorf("%w: resolve Daytona SDK: %v", execution.ErrUnavailable, err)
	}
	sdkImportPath := filepath.Join(sdkRoot, "esm", "index.js")
	if _, err := os.Stat(sdkImportPath); err != nil {
		return "", "", fmt.Errorf("%w: resolve Daytona SDK entrypoint: %v", execution.ErrUnavailable, err)
	}
	dest := filepath.Join(broker.dataDir, "runtime", "daytona-provider-host")
	serverPath, diagnostics, err := workflowdefs.BuildBuiltinBundle(
		ctx,
		workflowdefs.BuiltinEpicRunnerWorkflowName,
		dest,
	)
	if err != nil {
		if diagnostics != "" {
			return "", "", fmt.Errorf("%w: build Daytona provider host: %s", execution.ErrUnavailable, diagnostics)
		}
		return "", "", fmt.Errorf("%w: build Daytona provider host: %v", execution.ErrUnavailable, err)
	}
	broker.serverPath = serverPath
	broker.sdkImportPath = sdkImportPath
	return serverPath, sdkImportPath, nil
}

func validateDaytonaBrokerCommand(command execution.DaytonaProviderCommand) error {
	if strings.TrimSpace(command.WorkspaceKey) == "" ||
		strings.TrimSpace(command.TaskRunID) == "" ||
		strings.TrimSpace(command.WorkItemID) == "" ||
		strings.TrimSpace(command.DriverRunID) == "" {
		return fmt.Errorf("daytona provider command identity is incomplete")
	}
	if command.Intent.SchemaVersion != execution.DaytonaProviderSchemaV1 ||
		strings.TrimSpace(command.Intent.RepositoryURL) == "" ||
		strings.TrimSpace(command.Intent.TaskPrompt) == "" ||
		!strings.EqualFold(strings.TrimSpace(command.Intent.Backend), "codex") {
		return fmt.Errorf("daytona provider command is invalid")
	}
	return nil
}

func isDaytonaDemoMode(mode string) bool {
	switch strings.TrimSpace(mode) {
	case "e2e-smoke", "slack-pr-chain":
		return true
	default:
		return false
	}
}

func zeroDaytonaCredential(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

var _ execution.DaytonaProviderBroker = (*DaytonaProviderBroker)(nil)
