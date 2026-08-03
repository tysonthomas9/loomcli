package workflowdistribution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	_ "embed"

	appworkflowauthoring "github.com/tysonthomas9/loomcli/internal/app/workflowauthoring"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
)

const (
	BuiltinEpicRunnerWorkflowName        = workflowcatalog.BuiltinEpicRunnerWorkflowName
	BuiltinGitHubReviewAgentWorkflowName = workflowcatalog.BuiltinGitHubReviewAgentWorkflowName
	BuiltinGitHubReviewTaskRunnerName    = workflowcatalog.BuiltinGitHubReviewTaskRunnerName
	BuiltinBugFixAgentWorkflowName       = workflowcatalog.BuiltinBugFixAgentWorkflowName
	BuiltinReviewLoopAgentWorkflowName   = workflowcatalog.BuiltinReviewLoopAgentWorkflowName
	BuiltinLocalReviewAgentWorkflowName  = workflowcatalog.BuiltinLocalReviewAgentWorkflowName
	BuiltinPromptAgentWorkflowName       = workflowcatalog.BuiltinPromptAgentWorkflowName
)

// ErrBuildToolchainUnavailable distinguishes a deployment/profile that cannot
// materialize a workflow bundle from a malformed workflow or a store failure.
// Callers such as the unified agent API can map this operator-fixable condition
// to 503 instead of reporting an opaque internal error.
var ErrBuildToolchainUnavailable = appworkflowauthoring.ErrBuildToolchainUnavailable

//go:embed builtin/epic-runner.ts
var builtinEpicRunnerWorkflowSource string

//go:embed builtin/local-task-runner.ts
var builtinLocalTaskRunnerWorkflowSource string

//go:embed builtin/daytona-task-runner.ts
var builtinDaytonaTaskRunnerWorkflowSource string

//go:embed builtin/daytona-provider-host.ts
var builtinDaytonaProviderHostWorkflowSource string

//go:embed builtin/openshell-task-runner.ts
var builtinOpenShellTaskRunnerWorkflowSource string

//go:embed builtin/github-review-agent.ts
var builtinGitHubReviewAgentWorkflowSource string

//go:embed builtin/github-review-task-runner.ts
var builtinGitHubReviewTaskRunnerWorkflowSource string

//go:embed builtin/bug-fix-agent.ts
var builtinBugFixAgentWorkflowSource string

//go:embed builtin/review-loop-agent.ts
var builtinReviewLoopAgentWorkflowSource string

//go:embed builtin/local-review-agent.ts
var builtinLocalReviewAgentWorkflowSource string

//go:embed builtin/prompt-agent.ts
var builtinPromptAgentWorkflowSource string

type Spec struct {
	Entrypoint string
	Files      map[string]string
}

var builtinWorkflows = map[string]Spec{
	BuiltinEpicRunnerWorkflowName:        builtinEpicRunnerSpec(),
	BuiltinGitHubReviewAgentWorkflowName: builtinGitHubReviewAgentSpec(),
	BuiltinBugFixAgentWorkflowName:       builtinBugFixAgentSpec(),
	BuiltinReviewLoopAgentWorkflowName:   builtinReviewLoopAgentSpec(),
	BuiltinLocalReviewAgentWorkflowName:  builtinLocalReviewAgentSpec(),
	BuiltinPromptAgentWorkflowName:       builtinPromptAgentSpec(),
}

// builtinSpec builds the single-entrypoint Spec for an embedded source-tree
// workflow: the entrypoint is workflows/{name}.ts and the only file is that
// embedded source. Adding a builtin is one map entry + one //go:embed.
func builtinSpec(name, source string) Spec {
	entrypoint := "workflows/" + name + ".ts"
	return Spec{
		Entrypoint: entrypoint,
		Files:      map[string]string{entrypoint: source},
	}
}

func builtinEpicRunnerSpec() Spec {
	spec := builtinSpec(BuiltinEpicRunnerWorkflowName, builtinEpicRunnerWorkflowSource)
	spec.Files["workflows/local-task-runner.ts"] = builtinLocalTaskRunnerWorkflowSource
	spec.Files["workflows/daytona-task-runner.ts"] = builtinDaytonaTaskRunnerWorkflowSource
	spec.Files["workflows/daytona-provider-host.ts"] = builtinDaytonaProviderHostWorkflowSource
	spec.Files["workflows/openshell-task-runner.ts"] = builtinOpenShellTaskRunnerWorkflowSource
	return spec
}

func builtinGitHubReviewAgentSpec() Spec {
	spec := builtinSpec(BuiltinGitHubReviewAgentWorkflowName, builtinGitHubReviewAgentWorkflowSource)
	spec.Files["workflows/"+BuiltinGitHubReviewTaskRunnerName+".ts"] = builtinGitHubReviewTaskRunnerWorkflowSource
	return spec
}

// builtinBugFixAgentSpec bundles the bug-fix workflow with the local + daytona
// task runners it dispatches codex through (P1, golden scenario S1).
func builtinBugFixAgentSpec() Spec {
	spec := builtinSpec(BuiltinBugFixAgentWorkflowName, builtinBugFixAgentWorkflowSource)
	spec.Files["workflows/local-task-runner.ts"] = builtinLocalTaskRunnerWorkflowSource
	spec.Files["workflows/daytona-task-runner.ts"] = builtinDaytonaTaskRunnerWorkflowSource
	spec.Files["workflows/daytona-provider-host.ts"] = builtinDaytonaProviderHostWorkflowSource
	return spec
}

// builtinReviewLoopAgentSpec is the code-review loop (P2, golden scenario S2). It
// performs the review INLINE (no child workflow) — a child run inherits no trigger
// binding, so its connector actions would be unauthorizable — dispatching a
// github-review-task-runner task-run for the codex review. It bundles that runner
// as a sibling (mirroring how bug-fix bundles local-task-runner) so the driver
// version manifest declares it and resolveDriverRunner can find it.
func builtinReviewLoopAgentSpec() Spec {
	spec := builtinSpec(BuiltinReviewLoopAgentWorkflowName, builtinReviewLoopAgentWorkflowSource)
	spec.Files["workflows/"+BuiltinGitHubReviewTaskRunnerName+".ts"] = builtinGitHubReviewTaskRunnerWorkflowSource
	return spec
}

// builtinLocalReviewAgentSpec is the local-branch review loop. It has no
// GitHub connector dependency, but it still uses the trusted review task-runner
// as a diff-as-data code-review brain, so it declares the runner as a sibling.
func builtinLocalReviewAgentSpec() Spec {
	spec := builtinSpec(BuiltinLocalReviewAgentWorkflowName, builtinLocalReviewAgentWorkflowSource)
	spec.Files["workflows/"+BuiltinGitHubReviewTaskRunnerName+".ts"] = builtinGitHubReviewTaskRunnerWorkflowSource
	return spec
}

// builtinPromptAgentSpec bundles the prompt-agent workflow with the local
// task runner it dispatches the backend CLI through (Phase 4 prompt-agent
// spike). Bundling local-task-runner as a sibling (mirroring bug-fix-agent) is
// what lets DeriveRunners declare it in the version manifest so
// resolveDriverRunner can find it when the workflow calls
// taskRuns.request({ runner: "local-task-runner" }).
func builtinPromptAgentSpec() Spec {
	spec := builtinSpec(BuiltinPromptAgentWorkflowName, builtinPromptAgentWorkflowSource)
	spec.Files["workflows/local-task-runner.ts"] = builtinLocalTaskRunnerWorkflowSource
	return spec
}

// BuiltinWorkflowNames returns the registered built-in workflow names sorted,
// so callers (EnsureBuiltinWorkflow loops, registration round-trip tests) get
// a stable list independent of map iteration order.
func BuiltinWorkflowNames() []string {
	return workflowcatalog.BuiltinWorkflowNames()
}

func BuiltinWorkflow(name string) (Spec, bool) {
	spec, ok := builtinWorkflows[strings.TrimSpace(name)]
	if !ok {
		return Spec{}, false
	}
	files := make(map[string]string, len(spec.Files))
	for key, value := range spec.Files {
		files[key] = value
	}
	spec.Files = files
	return spec, true
}

// IsBuiltinWorkflow reports whether name is a registered built-in workflow. It
// is a direct map lookup, unlike scanning the sorted BuiltinWorkflowNames slice.
func IsBuiltinWorkflow(name string) bool {
	return workflowcatalog.IsBuiltinWorkflowName(strings.TrimSpace(name))
}

// BuildBuiltinBundle builds a builtin workflow source tree into destDir and
// returns the generated server.mjs path plus redacted Flue diagnostics. It lets
// host-side callers obtain a runnable builtin task runner without committing
// generated bundle artifacts.
func BuildBuiltinBundle(ctx context.Context, name, destDir string) (string, string, error) {
	spec, ok := BuiltinWorkflow(name)
	if !ok {
		return "", "", domain.ErrNotFound
	}
	files, err := ValidateWorkflowFiles(spec.Files)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		return "", "", fmt.Errorf("create builtin bundle parent: %w", err)
	}
	if err := os.RemoveAll(destDir); err != nil {
		return "", "", fmt.Errorf("clean builtin bundle dest %q: %w", destDir, err)
	}
	buildRoot, err := os.MkdirTemp(filepath.Dir(destDir), name+"-source-build-*")
	if err != nil {
		return "", "", fmt.Errorf("create builtin source build root: %w", err)
	}
	defer os.RemoveAll(buildRoot) //nolint:errcheck
	if err := writeWorkflowBuildProject(buildRoot, files); err != nil {
		return "", "", err
	}
	output, err := runFlueBuild(ctx, buildRoot, destDir)
	output = RedactBuildDiagnostics(output)
	if err != nil {
		if output != "" {
			return "", output, redactedFlueBuildError(err, output)
		}
		return "", "", err
	}
	if err := stageFlueRuntimeDependencies(destDir); err != nil {
		_ = os.RemoveAll(destDir)
		return "", output, err
	}
	serverPath := filepath.Join(destDir, "server.mjs")
	if _, err := os.Stat(serverPath); err != nil {
		return "", output, fmt.Errorf("flue build missing dist/server.mjs: %w", err)
	}
	return serverPath, output, nil
}

// BuildOptions identifies one local Flue build. Durable Driver and
// DriverVersion mutation is intentionally absent from this infrastructure
// adapter; its caller must submit the staged immutable metadata through
// Workflow Catalog's AuthoringStore.
type BuildOptions struct {
	Name    string
	Files   map[string]string
	WorkDir string
}

// BuiltBundle owns a temporary built distribution until Cleanup. OutputDir is
// suitable for local bundle staging; it is never a durable catalog reference.
type BuiltBundle struct {
	WorkDir     string
	OutputDir   string
	Diagnostics string
	cleanupRoot string
}

func (bundle *BuiltBundle) Cleanup() {
	if bundle == nil || bundle.cleanupRoot == "" {
		return
	}
	_ = os.RemoveAll(bundle.cleanupRoot)
	bundle.cleanupRoot = ""
}

// Build materializes source into a temporary Flue distribution without
// mutating catalog state.
func Build(ctx context.Context, opts BuildOptions) (*BuiltBundle, string, error) {
	files, err := ValidateWorkflowFiles(opts.Files)
	if err != nil {
		return nil, "", err
	}
	workDir := opts.WorkDir
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return nil, "", fmt.Errorf("resolve work dir: %w", err)
		}
	}
	buildParent := filepath.Join(workDir, ".loom", "workflow-builds")
	if err := os.MkdirAll(buildParent, 0o755); err != nil {
		return nil, "", fmt.Errorf("create workflow build root: %w", err)
	}
	buildRoot, err := os.MkdirTemp(buildParent, opts.Name+"-*")
	if err != nil {
		return nil, "", fmt.Errorf("create workflow build project: %w", err)
	}
	if err := writeWorkflowBuildProject(buildRoot, files); err != nil {
		_ = os.RemoveAll(buildRoot)
		return nil, "", err
	}
	outputDir := filepath.Join(buildRoot, "dist")
	output, err := runFlueBuild(ctx, buildRoot, outputDir)
	if err != nil {
		_ = os.RemoveAll(buildRoot)
		redacted := RedactBuildDiagnostics(output)
		if redacted != "" {
			return nil, redacted, redactedFlueBuildError(err, redacted)
		}
		return nil, "", err
	}
	if err := stageFlueRuntimeDependencies(outputDir); err != nil {
		_ = os.RemoveAll(buildRoot)
		return nil, RedactBuildDiagnostics(output), err
	}
	output = RedactBuildDiagnostics(output)
	return &BuiltBundle{
		WorkDir: workDir, OutputDir: outputDir, Diagnostics: output,
		cleanupRoot: buildRoot,
	}, output, nil
}

// deprecatedWorkflowRunners are sibling runner files that must never be
// registered as selectable runners (§4.6): openshell-task-runner is a
// fail-closed stub with no real integration, so it is denied even though its
// source still ships in the bundle.
var deprecatedWorkflowRunners = map[string]struct{}{
	driver.OpenShellRunnerName: {},
}

// internalWorkflowEntries ship in a bundle as host-only implementation
// details. They are not public task-runner entrypoints and must never appear
// in a DriverVersion runner manifest.
var internalWorkflowEntries = map[string]struct{}{
	"daytona-provider-host": {},
}

func DeriveWorkflowRunnerSpecs(entrypoint string, files map[string]string) []driver.DriverRunnerSpec {
	entrypoint = filepath.ToSlash(entrypoint)
	runners := []driver.DriverRunnerSpec{}
	for rel := range files {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" || rel == entrypoint || !strings.HasPrefix(rel, "workflows/") || filepath.Ext(rel) != ".ts" {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
		if name == "" {
			continue
		}
		if _, denied := deprecatedWorkflowRunners[name]; denied {
			continue
		}
		if _, internal := internalWorkflowEntries[name]; internal {
			continue
		}
		runners = append(runners, driver.DriverRunnerSpec{
			Name:       name,
			Kind:       driver.RunnerKindFlueWorkflow,
			Entrypoint: name,
		})
	}
	sort.Slice(runners, func(i, j int) bool {
		return runners[i].Name < runners[j].Name
	})
	return runners
}

// workflowRunnerNameSet returns the set of runner names the freshly-derived
// spec declares for a builtin workflow.
func RunnerNameSet(spec Spec) map[string]struct{} {
	runners := DeriveWorkflowRunnerSpecs(spec.Entrypoint, spec.Files)
	out := make(map[string]struct{}, len(runners))
	for _, runner := range runners {
		out[runner.Name] = struct{}{}
	}
	return out
}

// activeManifestRunnersAreStale reports whether the active version's declared
// runner list contains a deprecated/fabricated runner (e.g.
// openshell-task-runner) or any runner not in the freshly-derived set. Such a
// manifest must be re-registered (§4.6 refresh-on-deprecated) even when its
// source digest matches.
func ActiveManifestRunnersAreStale(manifest map[string]string, fresh map[string]struct{}) bool {
	raw := strings.TrimSpace(manifest["runners"])
	if raw == "" {
		return len(fresh) > 0
	}
	var runners []driver.DriverRunnerSpec
	if err := json.Unmarshal([]byte(raw), &runners); err != nil {
		// An undecodable runner list is stale by definition: force a refresh.
		return true
	}
	for _, runner := range runners {
		name := strings.TrimSpace(runner.Name)
		if name == "" {
			continue
		}
		if _, denied := deprecatedWorkflowRunners[name]; denied {
			return true
		}
		if _, ok := fresh[name]; !ok {
			return true
		}
	}
	return false
}

// manifestMissingFreshRunners returns the freshly-derived runner names that
// the active version's manifest does NOT declare, sorted. A non-empty result
// means the registered builtin can serve only a SUBSET of the current runners
// (e.g. scripts/test-runner-pr-e2e.sh registers epic-runner with only
// local-task-runner): a later run requesting a missing runner would pin this
// version and applyResolvedRunner would reject the child task run. Such a
// manifest must be refreshed when a rebuild is possible. Undecodable or empty
// manifests are the stale check's business, not this one's.
func ManifestMissingFreshRunners(manifest map[string]string, fresh map[string]struct{}) []string {
	raw := strings.TrimSpace(manifest["runners"])
	if raw == "" {
		return nil
	}
	var runners []driver.DriverRunnerSpec
	if err := json.Unmarshal([]byte(raw), &runners); err != nil {
		return nil
	}
	declared := make(map[string]struct{}, len(runners))
	for _, runner := range runners {
		if name := strings.TrimSpace(runner.Name); name != "" {
			declared[name] = struct{}{}
		}
	}
	missing := make([]string, 0, len(fresh))
	for name := range fresh {
		if _, ok := declared[name]; !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

func ValidateWorkflowEntrypoint(name, entrypoint string) error {
	want := filepath.ToSlash(filepath.Join("workflows", name+".ts"))
	if filepath.ToSlash(entrypoint) != want {
		return fmt.Errorf("entrypoint must be %s", want)
	}
	return nil
}

func ValidateWorkflowFiles(in map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(in))
	originalByPath := make(map[string]string, len(in))
	for raw, content := range in {
		rel, err := validateWorkflowFilePath(raw)
		if err != nil {
			return nil, err
		}
		if previous, exists := originalByPath[rel]; exists {
			return nil, fmt.Errorf("workflow source paths %q and %q normalize to the same path %q", previous, raw, rel)
		}
		if strings.ContainsRune(content, '\x00') {
			return nil, fmt.Errorf("%s contains binary content", rel)
		}
		originalByPath[rel] = raw
		out[rel] = content
	}
	return out, nil
}

func RedactBuildDiagnostics(input string) string {
	output := input
	for _, pair := range os.Environ() {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || len(value) < 4 || !sensitiveEnvKey(key) {
			continue
		}
		output = strings.ReplaceAll(output, value, "[redacted]")
	}
	const maxDiagnosticsBytes = 32768
	if len(output) > maxDiagnosticsBytes {
		output = output[len(output)-maxDiagnosticsBytes:]
	}
	return output
}

func sensitiveEnvKey(key string) bool {
	key = strings.ToUpper(strings.TrimSpace(key))
	for _, marker := range []string{"TOKEN", "SECRET", "PASSWORD", "CREDENTIAL", "API_KEY", "ACCESS_KEY", "PRIVATE_KEY"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func validateWorkflowFilePath(raw string) (string, error) {
	rel := strings.TrimSpace(raw)
	if rel == "" {
		return "", fmt.Errorf("file path is required")
	}
	if strings.Contains(rel, `\`) {
		return "", fmt.Errorf("%s must use canonical slash separators", rel)
	}
	if strings.HasPrefix(rel, "/") || path.IsAbs(rel) {
		return "", fmt.Errorf("%s must be relative", rel)
	}
	clean := path.Clean(rel)
	if clean == "." || clean == "" {
		return "", fmt.Errorf("%s is invalid", rel)
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".." {
			return "", fmt.Errorf("%s must not contain path traversal", rel)
		}
		if part == "node_modules" {
			return "", fmt.Errorf("%s must not include node_modules", rel)
		}
	}
	switch path.Base(clean) {
	case "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb":
		return "", fmt.Errorf("%s is not allowed in workflow source uploads", clean)
	}
	return clean, nil
}

func writeWorkflowBuildProject(root string, files map[string]string) error {
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"type":"module","dependencies":{"@loom/sdk":"file:./node_modules/@loom/sdk","@flue/runtime":"file:./node_modules/@flue/runtime","valibot":"file:./node_modules/valibot"}}`+"\n"), 0o644); err != nil {
		return fmt.Errorf("write generated package.json: %w", err)
	}
	sdkRoot, err := loomSDKRoot()
	if err != nil {
		return err
	}
	loomScope := filepath.Join(root, "node_modules", "@loom")
	if err := os.MkdirAll(loomScope, 0o755); err != nil {
		return fmt.Errorf("create generated node_modules: %w", err)
	}
	if err := os.Symlink(sdkRoot, filepath.Join(loomScope, "sdk")); err != nil {
		return fmt.Errorf("link @loom/sdk: %w", err)
	}
	if err := linkFlueBuildDependencies(root); err != nil {
		return err
	}
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create parent for %s: %w", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

func linkFlueBuildDependencies(root string) error {
	runtimeRoot, err := flueRuntimeRoot()
	if err != nil {
		return err
	}
	links := map[string]string{
		filepath.Join("node_modules", "@flue", "runtime"):     runtimeRoot,
		filepath.Join("node_modules", "@hono", "node-server"): filepath.Join(runtimeRoot, "node_modules", "@hono", "node-server"),
		filepath.Join("node_modules", "hono"):                 filepath.Join(runtimeRoot, "node_modules", "hono"),
		filepath.Join("node_modules", "valibot"):              filepath.Join(runtimeRoot, "node_modules", "valibot"),
	}
	for rel, target := range links {
		if _, err := os.Stat(target); err != nil {
			return fmt.Errorf("%w: resolve Flue build dependency %s: %v", ErrBuildToolchainUnavailable, target, err)
		}
		link := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return fmt.Errorf("create Flue build dependency parent: %w", err)
		}
		if err := os.Symlink(target, link); err != nil {
			return fmt.Errorf("link Flue build dependency %s: %w", rel, err)
		}
	}
	return nil
}

func LinkFlueBuildDependencies(root string) error {
	return linkFlueBuildDependencies(root)
}

// flueRuntimePackages are dependencies that Flue deliberately leaves as bare
// imports in its Node target. They must travel with dist: the source build's
// node_modules tree is temporary, and Driver staging copies only dist.
var flueRuntimePackages = []string{"valibot"}

func stageFlueRuntimeDependencies(outputDir string) error {
	runtimeRoot, err := flueRuntimeRoot()
	if err != nil {
		return err
	}
	for _, packageName := range flueRuntimePackages {
		sourceLink := filepath.Join(runtimeRoot, "node_modules", filepath.FromSlash(packageName))
		source, err := filepath.EvalSymlinks(sourceLink)
		if err != nil {
			return fmt.Errorf("%w: resolve Flue runtime dependency %s: %v", ErrBuildToolchainUnavailable, packageName, err)
		}
		dest := filepath.Join(outputDir, "node_modules", filepath.FromSlash(packageName))
		if err := copyPortableDirectory(source, dest); err != nil {
			return fmt.Errorf("stage Flue runtime dependency %s: %w", packageName, err)
		}
	}
	return nil
}

// copyPortableDirectory materializes a dependency without retaining symlinks
// into a host pnpm store. Nested links are rejected rather than allowing a
// bundle build to copy files outside the resolved package root.
func copyPortableDirectory(source, dest string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("source %s is not a directory", source)
	}
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		entryInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("nested symlink %s is not portable", current)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, entryInfo.Mode().Perm())
		}
		if !entryInfo.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type %s", current)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		input, err := os.Open(current) //nolint:gosec // source is beneath the resolved local package root.
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, entryInfo.Mode().Perm()) //nolint:gosec
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeOutputErr := output.Close()
		closeInputErr := input.Close()
		return errors.Join(copyErr, closeOutputErr, closeInputErr)
	})
}

func loomSDKRoot() (string, error) {
	if root := strings.TrimSpace(os.Getenv("LOOM_SDK_ROOT")); root != "" {
		if _, err := os.Stat(filepath.Join(root, "package.json")); err == nil {
			return root, nil
		}
		return "", fmt.Errorf("%w: local @loom/sdk package not found at LOOM_SDK_ROOT=%s", ErrBuildToolchainUnavailable, root)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(cwd, "sdk")
	if _, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil {
		return candidate, nil
	}
	return "", fmt.Errorf("%w: local @loom/sdk package not found; set LOOM_SDK_ROOT", ErrBuildToolchainUnavailable)
}

func flueRuntimeRoot() (string, error) {
	candidates := []string{}
	if root := strings.TrimSpace(os.Getenv("LOOM_FLUE_RUNTIME_ROOT")); root != "" {
		candidates = append(candidates, root)
	}
	if root := strings.TrimSpace(os.Getenv("FLUE_RUNTIME_ROOT")); root != "" {
		candidates = append(candidates, root)
	}
	if repo := strings.TrimSpace(os.Getenv("FLUE_REPO")); repo != "" {
		candidates = append(candidates, filepath.Join(repo, "packages", "runtime"))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "..", "flue", "packages", "runtime"))
	}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if _, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w: local @flue/runtime package not found; set LOOM_FLUE_RUNTIME_ROOT, FLUE_RUNTIME_ROOT, or FLUE_REPO", ErrBuildToolchainUnavailable)
}

func FlueRuntimeRoot() (string, error) {
	return flueRuntimeRoot()
}

func daytonaSDKRoot(runtimeRoot string) (string, error) {
	candidates := []string{}
	configuredRoot := strings.TrimSpace(os.Getenv("DAYTONA_SDK_ROOT"))
	if configuredRoot != "" {
		candidates = append(candidates, configuredRoot)
	}
	if repo := strings.TrimSpace(os.Getenv("FLUE_REPO")); repo != "" {
		candidates = append(candidates, filepath.Join(repo, "node_modules", ".pnpm", "node_modules", "@daytona", "sdk"))
	}
	runtimeRoot = filepath.Clean(strings.TrimSpace(runtimeRoot))
	packagesRoot := filepath.Dir(runtimeRoot)
	if filepath.Base(runtimeRoot) == "runtime" && filepath.Base(packagesRoot) == "packages" {
		flueRoot := filepath.Dir(packagesRoot)
		candidates = append(candidates, filepath.Join(flueRoot, "node_modules", ".pnpm", "node_modules", "@daytona", "sdk"))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "..", "flue", "node_modules", ".pnpm", "node_modules", "@daytona", "sdk"))
	}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if _, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil {
			return candidate, nil
		}
	}
	if configuredRoot != "" {
		return "", fmt.Errorf("local @daytona/sdk package not found at DAYTONA_SDK_ROOT=%s", configuredRoot)
	}
	return "", fmt.Errorf("local @daytona/sdk package not found; set DAYTONA_SDK_ROOT")
}

func DaytonaSDKRoot(runtimeRoot string) (string, error) {
	return daytonaSDKRoot(runtimeRoot)
}

func runFlueBuild(ctx context.Context, root, outputDir string) (string, error) {
	command, err := flueCommand()
	if err != nil {
		return "", err
	}
	args := append(append([]string{}, command[1:]...), "build", "--target", "node", "--root", root, "--output", outputDir)
	cmd := exec.CommandContext(ctx, command[0], args...) //nolint:gosec // command is deployment/operator configuration.
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))
	if err != nil {
		if output == "" {
			output = err.Error()
		}
		return output, classifyFlueBuildError(err, output)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "server.mjs")); err != nil {
		return output, fmt.Errorf("flue build missing dist/server.mjs: %w", err)
	}
	return output, nil
}

// classifyFlueBuildError keeps malformed workflow source failures distinct
// from deployment profiles whose Flue installation cannot start its bundler.
// Rolldown ships its native executable as a platform-specific optional
// dependency, so a host-installed node_modules mounted into another platform
// can have a working Flue CLI while still being unable to build anything.
func classifyFlueBuildError(cause error, output string) error {
	if errors.Is(cause, exec.ErrNotFound) || errors.Is(cause, os.ErrNotExist) || isMissingRolldownNativeBinding(output) {
		return fmt.Errorf("%w: flue build failed: %s", ErrBuildToolchainUnavailable, output)
	}
	return fmt.Errorf("flue build failed: %s", output)
}

func ClassifyFlueBuildError(cause error, output string) error {
	return classifyFlueBuildError(cause, output)
}

// redactedFlueBuildError preserves the typed operator-facing classification
// after replacing raw build output with diagnostics safe for API callers and
// persisted build records.
func redactedFlueBuildError(err error, redacted string) error {
	if errors.Is(err, ErrBuildToolchainUnavailable) {
		return fmt.Errorf("%w: flue build failed: %s", ErrBuildToolchainUnavailable, redacted)
	}
	return fmt.Errorf("flue build failed: %s", redacted)
}

func isMissingRolldownNativeBinding(output string) bool {
	normalized := strings.ToLower(output)
	if !strings.Contains(normalized, "rolldown") {
		return false
	}
	bindingReference := strings.Contains(normalized, "@rolldown/binding-") ||
		strings.Contains(normalized, "rolldown-binding.") ||
		strings.Contains(normalized, "native binding")
	missingOrUnloadable := strings.Contains(normalized, "cannot find module") ||
		strings.Contains(normalized, "module not found") ||
		strings.Contains(normalized, "failed to load native binding") ||
		strings.Contains(normalized, "could not load native binding") ||
		strings.Contains(normalized, "native binding not found")
	return bindingReference && missingOrUnloadable
}

func flueCommand() ([]string, error) {
	if encoded := strings.TrimSpace(os.Getenv("LOOM_REAL_FLUE_CMD_JSON")); encoded != "" {
		var parsed []string
		if err := json.Unmarshal([]byte(encoded), &parsed); err != nil {
			return nil, fmt.Errorf("decode LOOM_REAL_FLUE_CMD_JSON: %w", err)
		}
		if len(parsed) == 0 {
			return nil, fmt.Errorf("LOOM_REAL_FLUE_CMD_JSON must contain at least one command element")
		}
		return parsed, nil
	}
	if raw := strings.TrimSpace(os.Getenv("LOOM_REAL_FLUE_CMD")); raw != "" {
		return []string{raw}, nil
	}
	path, err := exec.LookPath("flue")
	if err != nil {
		return nil, fmt.Errorf("%w: flue not found on PATH; set LOOM_REAL_FLUE_CMD_JSON or LOOM_REAL_FLUE_CMD", ErrBuildToolchainUnavailable)
	}
	return []string{path}, nil
}
