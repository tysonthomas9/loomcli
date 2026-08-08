package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestPhase5SourceControlProductionReachabilityRatchet keeps the named owner
// on the two production paths Phase 5 closes. Task execution and PR review
// receive only the typed authority-free application Materializer. Workspace
// admission alone receives the separate admission materializer; task/PR
// helpers may create worktrees from refs/loom but may not resolve credentials
// or perform credential-aware clone/fetch operations themselves.
func TestPhase5SourceControlProductionReachabilityRatchet(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	required := map[string][]string{
		"internal/app/serve/source_control_composition.go": {
			"MaterializeWorkspace",
			"FetchRepositoryRef",
		},
		"internal/cli/serve/serve.go": {
			"SourceControlMaterializer",
			"RepositoryAdmissionMaterializer",
			"NewRepositoryAdmissionJournal",
			"NewStoreBackedWorkspaceAdmissionOperationsWithWorkspace",
			"CreateWorkspace",
			"AddWorkspaceRepos",
			"RuntimeRegistrations",
		},
		"internal/cli/serve/workspacemgr/workspace_store_repository_materialization.go": {
			"PrepareRepositoryAdmissionCheckout",
		},
		"internal/driver/task_worktree_resolver.go": {
			"PrepareTaskCheckout",
		},
		"internal/driver/taskworktree/preparer.go": {
			"EnsureDetachedGitWorktreeAtRef",
		},
		"internal/infra/localgit/executor.go": {
			"CloneRepoToAnonymous",
			"FetchGitRefAnonymous",
		},
		"internal/webui/handlers/prreview/reviewer.go": {
			"PreparePullRequestCheckout",
		},
		"internal/webui/handlers/prreview/module.go": {
			"EnsureDetachedGitWorktreeAtFetchedPRHead",
			"RecordPRReviewContextFromFetchedBase",
		},
	}
	for path, selectors := range required {
		facts := loadSourceControlFileFacts(t, root, path)
		for _, selector := range selectors {
			if facts.selectors[selector] == 0 {
				t.Errorf("%s no longer reaches required Source Control/local-only operation %s", path, selector)
			}
		}
	}

	serveFacts := loadSourceControlFileFacts(
		t,
		root,
		"internal/cli/serve/serve.go",
	)
	if !serveFacts.wiresSourceControlConfig {
		t.Error("serve no longer assigns the authority-free SourceControl Materializer to ServerConfig")
	}
	if !serveFacts.wiresRepositoryAdmissionConfig {
		t.Error("serve no longer assigns the Workspace-only RepositoryAdmissionMaterializer to ServerConfig")
	}

	localGitFacts := loadSourceControlFileFacts(
		t,
		root,
		"internal/infra/localgit/executor.go",
	)
	for _, required := range []string{
		"cloneWithCredential",
		"cloneAmbient",
		"fetchRefWithCredential",
		"fetchRefAmbient",
	} {
		if localGitFacts.identifiers[required] == 0 {
			t.Errorf("Connectors/localgit no longer owns required private Git operation %s", required)
		}
	}
	for _, forbidden := range []string{
		"CloneRepoToWithCredential",
		"CloneRepoToWithCredentials",
		"CloneRepoToAmbient",
		"FetchGitRefWithCredential",
		"FetchGitRefAmbient",
	} {
		if localGitFacts.selectors[forbidden] != 0 {
			t.Errorf("Connectors/localgit delegates private Git operation to legacy localworkspace selector %s", forbidden)
		}
	}

	agentsFacts := loadSourceControlFileFacts(
		t,
		root,
		"internal/cli/serve/serveadapter/agents.go",
	)
	if agentsFacts.functions["SourceControlMaterializer"] == 0 {
		t.Error("serve composition no longer publishes the authority-free SourceControl Materializer")
	}
	if agentsFacts.functions["RepositoryAdmissionMaterializer"] == 0 {
		t.Error("serve composition no longer publishes the Workspace-only RepositoryAdmissionMaterializer")
	}
	if !agentsFacts.retainsSourceControl {
		t.Error("Agents composition no longer retains the SourceControl capability it constructs")
	}
	sourceControlAPIFacts := loadSourceControlFileFacts(
		t,
		root,
		"internal/modules/sourcecontrol/api.go",
	)
	for _, required := range []string{
		"Materializer",
		"RepositoryAdmissionMaterializer",
		"PrepareTaskCheckout",
		"PreparePullRequestCheckout",
		"PrepareRepositoryAdmissionCheckout",
	} {
		if sourceControlAPIFacts.identifiers[required] == 0 {
			t.Errorf("Source Control API no longer declares required split materialization symbol %s", required)
		}
	}
	assertFileExcludes(t, root, "internal/modules/sourcecontrol/api.go", []string{
		"PrepareRepositoryCheckout",
	})
	for _, path := range productionGoFilesBelow(t, root, "internal") {
		if path == "internal/app/serve/source_control_composition.go" {
			continue
		}
		facts := loadSourceControlFileFacts(t, root, path)
		if facts.selectors["PrepareRepositoryCheckout"] != 0 {
			t.Errorf(
				"%s reaches generic repository checkout outside private Source Control composition",
				path,
			)
		}
	}

	credentialFreePaths := []string{
		"internal/driver/task_worker.go",
		"internal/driver/task_worktree_resolver.go",
		"internal/webui/handlers/driverapi/task_runs.go",
	}
	credentialFreePaths = append(
		credentialFreePaths,
		productionGoFilesBelow(t, root, "internal/driver/taskworktree")...,
	)
	credentialFreePaths = append(
		credentialFreePaths,
		productionGoFilesBelow(t, root, "internal/webui/handlers/prreview")...,
	)
	for _, path := range credentialFreePaths {
		facts := loadSourceControlFileFacts(t, root, path)
		if facts.imports["github.com/tysonthomas9/loomcli/internal/gitauth"] {
			t.Errorf("%s imports credential resolution outside Connectors/localgit", path)
		}
		for _, forbidden := range []string{
			"NewLocalSettingsSource",
			"OpenWithLocalSettings",
			"OpenWithCredentials",
			"CloneRepoToWithCredentials",
			"EnsureDetachedGitWorktreeFromBranchWithCredentials",
			"EnsureDetachedGitWorktreeAtPRHeadWithCredentials",
			"RecordPRReviewContextWithCredentials",
			"PrepareRepositoryAdmissionCheckout",
		} {
			if facts.selectors[forbidden] != 0 {
				t.Errorf("%s reaches forbidden credential-aware operation %s", path, forbidden)
			}
		}
	}

	credentialOwnerPrefixes := []string{
		"internal/gitauth/",
		"internal/infra/localgit/",
		"internal/modules/connectors/",
	}
	credentialOwnerFiles := map[string]bool{
		"internal/app/serve/source_control_composition.go": true,
	}
	for _, path := range productionGoFilesBelow(t, root, "internal") {
		if credentialOwnerFiles[path] || pathHasAnyPrefix(path, credentialOwnerPrefixes) {
			continue
		}
		facts := loadSourceControlFileFacts(t, root, path)
		if facts.imports["github.com/tysonthomas9/loomcli/internal/gitauth"] {
			t.Errorf("%s imports raw git credential resolution outside Connectors/localgit", path)
		}
		for _, forbidden := range []string{
			"CloneRepoToWithCredential",
			"CloneRepoToWithCredentials",
		} {
			if facts.identifiers[forbidden] != 0 {
				t.Errorf("%s references forbidden credential-aware operation %s outside Connectors/localgit", path, forbidden)
			}
		}
	}
}

// TestPhase5CredentialContainmentRatchet keeps the bundled Daytona runner on
// the lease-authenticated TaskRun facade while preventing it from reacquiring
// provider plaintext. Only the host-owned broker and its private, non-selectable
// provider workflow may resolve credentials or import the provider SDK.
func TestPhase5CredentialContainmentRatchet(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	retiredHandler := filepath.Join(
		root,
		"internal",
		"webui",
		"handlers",
		"taskrunapi",
		"credentials.go",
	)
	if _, err := os.Stat(retiredHandler); err == nil {
		t.Error("retired raw runtime-credential task-run handler was restored")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat retired runtime-credential handler: %v", err)
	}

	assertFileExcludes(t, root, "internal/webui/handlers/taskrunapi/module.go", []string{
		`"runtime-credential"`,
		"UnsealRuntimeCredential",
		"internal/localsettings",
	})

	for _, path := range []string{
		"sdk/runner.js",
		"sdk/runner.d.ts",
		"sdk/index.d.ts",
	} {
		assertFileExcludes(t, root, path, []string{
			"runtimeCredentials",
			"getRuntimeCredential",
			"RuntimeCredentialResponse",
			"runtime-credential",
		})
	}

	const daytonaRunner = "internal/infra/workflowdistribution/builtin/daytona-task-runner.ts"
	assertFileIncludes(t, root, daytonaRunner, []string{
		"DaytonaProviderSchemaV1",
		"TaskRunClient.fromEnv",
		"client.daytona.execute",
		"lease-authenticated TaskRun facade",
		"host-owned Daytona provider broker",
	})
	assertFileExcludes(t, root, daytonaRunner, []string{
		"@daytona/sdk",
		"runtime-credential",
		"runtimeCredentials",
		"getRuntimeCredential",
		"DAYTONA_API_KEY",
		"DAYTONA_CREDENTIAL_FILE",
		"GITHUB_TOKEN",
		"GITHUB_TOKEN_FILE",
		"apiKey",
		"githubFetch",
		"git clone",
		"Authorization",
		"x-access-token",
	})

	requiredDaytonaSelectors := map[string][]string{
		"internal/cli/serve/serve.go": {
			"NewDaytonaProviderBroker",
		},
		"internal/webui/handlers/taskrunapi/daytona.go": {
			"verifyLease",
			"ExecuteDaytona",
		},
		"internal/cli/serve/serveadapter/daytonabroker/daytona_provider.go": {
			"UnsealRuntimeCredentialBytes",
			"RunDaytonaProviderHost",
		},
		"internal/driver/daytonahost/host.go": {
			"DisallowUnknownFields",
		},
	}
	for path, selectors := range requiredDaytonaSelectors {
		facts := loadSourceControlFileFacts(t, root, path)
		for _, selector := range selectors {
			if facts.selectors[selector] == 0 {
				t.Errorf("%s no longer reaches required Daytona architecture operation %s", path, selector)
			}
		}
	}

	assertFileOrder(
		t,
		root,
		"internal/webui/handlers/taskrunapi/daytona.go",
		"m.verifyLease(",
		"m.daytonaProvider.ExecuteDaytona(",
	)
	assertFileIncludes(t, root, "internal/webui/handlers/taskrunapi/module.go", []string{
		`"daytona-execute":`,
		"DaytonaProvider execution.DaytonaProviderBroker",
	})
	assertFileIncludes(t, root, "internal/cli/serve/serveadapter/daytonabroker/daytona_provider.go", []string{
		"type DaytonaProviderBroker struct",
		"localsettings.UnsealRuntimeCredentialBytes",
		"driver.RunDaytonaProviderHost",
	})
	assertFileIncludes(t, root, "internal/driver/daytonahost/host.go", []string{
		"dedicated stdin/IPC launcher",
		"decoder.DisallowUnknownFields()",
		"containsProviderSecret",
		"redactProviderSecrets",
	})
	assertFileIncludes(t, root, "internal/infra/workflowdistribution/catalog_build.go", []string{
		"internalWorkflowEntries",
		`"daytona-provider-host": {}`,
	})
	assertFileIncludes(t, root, "internal/infra/workflowdistribution/builtin/daytona-provider-host.ts", []string{
		"not a selectable task runner",
		"private stdin/IPC channel",
		"Provider credentials never enter runner intent, argv, or subprocess env",
	})

	// The opt-in live-test harness is the sole operational input allowed to
	// read DAYTONA_API_KEY. It seals the value before invoking the same host
	// broker used by serve; no runtime compose/staging path accepts it.
	assertFileIncludes(t, root, "Makefile", []string{
		"harness seals DAYTONA_API_KEY into a temporary Loom vault",
		"LOOM_DAYTONA_BROKER_E2E=1",
		"TestE2EDaytonaProviderBroker",
	})
	assertFileExcludes(t, root, "Makefile", []string{
		"runtime-credential",
		"DAYTONA_CREDENTIAL_FILE",
		"GITHUB_TOKEN_FILE",
		"x-access-token",
	})

	runtimeOperationalPaths := []string{
		"scripts/stage-builtin-modules.sh",
		"scripts/test-builtin-workflows.sh",
		"scripts/test-epic-runner-runtime-matrix.sh",
		"scripts/test-runner-pr-e2e.sh",
		"smoke-test/smoke-test-slack-epic-runner-stack.sh",
		"test/local-mode/README.md",
		"test/local-mode/docker-compose.daytona.yml",
		"test/local-mode/docker-compose.workflow-build.yml",
		"test/local-mode/local-mode-entrypoint",
	}
	for _, path := range runtimeOperationalPaths {
		assertFileExcludes(t, root, path, []string{
			"runtime-credential",
			"DAYTONA_API_KEY",
			"DAYTONA_CREDENTIAL_FILE",
			"GITHUB_TOKEN_FILE",
			"x-access-token",
		})
	}
}

func pathHasAnyPrefix(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func assertFileIncludes(t *testing.T, root, relativePath string, required []string) {
	t.Helper()
	body := readArchitectureSourceFile(t, root, relativePath)
	for _, needle := range required {
		if !strings.Contains(body, needle) {
			t.Errorf("%s no longer contains required architecture marker %q", relativePath, needle)
		}
	}
}

func assertFileExcludes(t *testing.T, root, relativePath string, forbidden []string) {
	t.Helper()
	body := readArchitectureSourceFile(t, root, relativePath)
	for _, needle := range forbidden {
		if strings.Contains(body, needle) {
			t.Errorf("%s contains forbidden credential-containment marker %q", relativePath, needle)
		}
	}
}

func assertFileOrder(t *testing.T, root, relativePath, first, second string) {
	t.Helper()
	body := readArchitectureSourceFile(t, root, relativePath)
	firstIndex := strings.Index(body, first)
	secondIndex := strings.Index(body, second)
	if firstIndex < 0 {
		t.Errorf("%s no longer contains required architecture marker %q", relativePath, first)
		return
	}
	if secondIndex < 0 {
		t.Errorf("%s no longer contains required architecture marker %q", relativePath, second)
		return
	}
	if firstIndex >= secondIndex {
		t.Errorf("%s reaches %q before required fence %q", relativePath, second, first)
	}
}

func readArchitectureSourceFile(t *testing.T, root, relativePath string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relativePath)))
	if err != nil {
		t.Fatalf("read %s: %v", relativePath, err)
	}
	return string(body)
}

func productionGoFilesBelow(t *testing.T, root, relativeDirectory string) []string {
	t.Helper()
	var paths []string
	directory := filepath.Join(root, filepath.FromSlash(relativeDirectory))
	if err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() ||
			!strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", relativeDirectory, err)
	}
	return paths
}

type sourceControlFileFacts struct {
	selectors                      map[string]int
	functions                      map[string]int
	identifiers                    map[string]int
	imports                        map[string]bool
	wiresSourceControlConfig       bool
	wiresRepositoryAdmissionConfig bool
	retainsSourceControl           bool
}

func loadSourceControlFileFacts(t *testing.T, root, relativePath string) sourceControlFileFacts {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", relativePath, err)
	}
	facts := sourceControlFileFacts{
		selectors:   make(map[string]int),
		functions:   make(map[string]int),
		identifiers: make(map[string]int),
		imports:     make(map[string]bool),
	}
	for _, imported := range parsed.Imports {
		value, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			t.Fatalf("parse import in %s: %v", relativePath, err)
		}
		facts.imports[value] = true
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.Ident:
			facts.identifiers[value.Name]++
		case *ast.SelectorExpr:
			facts.selectors[value.Sel.Name]++
		case *ast.FuncDecl:
			if value.Name != nil {
				facts.functions[strings.TrimSpace(value.Name.Name)]++
			}
		case *ast.AssignStmt:
			for index := 0; index < len(value.Lhs) && index < len(value.Rhs); index++ {
				left, ok := value.Lhs[index].(*ast.SelectorExpr)
				if !ok {
					continue
				}
				switch left.Sel.Name {
				case "SourceControl":
					call, ok := value.Rhs[index].(*ast.CallExpr)
					if !ok {
						continue
					}
					right, ok := call.Fun.(*ast.SelectorExpr)
					facts.wiresSourceControlConfig = facts.wiresSourceControlConfig ||
						ok && right.Sel.Name == "SourceControlMaterializer"
				case "WorkspaceSourceControl":
					call, ok := value.Rhs[index].(*ast.CallExpr)
					if !ok {
						continue
					}
					right, ok := call.Fun.(*ast.SelectorExpr)
					facts.wiresRepositoryAdmissionConfig =
						facts.wiresRepositoryAdmissionConfig ||
							ok && right.Sel.Name == "RepositoryAdmissionMaterializer"
				case "sourceControl":
					right, ok := value.Rhs[index].(*ast.Ident)
					facts.retainsSourceControl = facts.retainsSourceControl ||
						ok && right.Name == "sourceControl"
				}
			}
		}
		return true
	})
	return facts
}
