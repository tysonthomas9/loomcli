package workflows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	_ "embed"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	BuiltinEpicRunnerWorkflowName        = "epic-runner"
	BuiltinGitHubReviewAgentWorkflowName = "github-review-agent"
	BuiltinGitHubReviewTaskRunnerName    = "github-review-task-runner"
)

//go:embed builtin/epic-runner.ts
var builtinEpicRunnerWorkflowSource string

//go:embed builtin/local-task-runner.ts
var builtinLocalTaskRunnerWorkflowSource string

//go:embed builtin/daytona-task-runner.ts
var builtinDaytonaTaskRunnerWorkflowSource string

//go:embed builtin/openshell-task-runner.ts
var builtinOpenShellTaskRunnerWorkflowSource string

//go:embed builtin/github-review-agent.ts
var builtinGitHubReviewAgentWorkflowSource string

//go:embed builtin/github-review-task-runner.ts
var builtinGitHubReviewTaskRunnerWorkflowSource string

type Spec struct {
	Entrypoint string
	Files      map[string]string
}

type BuildAndRegisterOptions struct {
	WorkspaceKey string
	Name         string
	Entrypoint   string
	Files        map[string]string
	Activate     bool
	SourceRef    string
	SourceDigest string
	CreatedBy    string
	WorkDir      string
	Runners      []driver.DriverRunnerSpec
	Manifest     map[string]string
	// DeriveRunners is reserved for trusted built-in template registration.
	// Custom/API/CLI builds must provide explicit runner specs; otherwise
	// sibling workflow files remain bundle-private and are not selectable
	// task runners.
	DeriveRunners bool
	// Trust is stamped server-side (§7 step 9 sandbox placement policy) and
	// is never plumbed from request input. BuildAndRegister is the external
	// submission path (workflows HTTP API), so empty defaults to UNTRUSTED —
	// fail closed; only EnsureBuiltinWorkflow passes trusted for the embedded
	// source-tree workflows.
	Trust domain.DriverTrustLevel
}

var builtinMu sync.Mutex

var builtinWorkflows = map[string]Spec{
	BuiltinEpicRunnerWorkflowName:        builtinEpicRunnerSpec(),
	BuiltinGitHubReviewAgentWorkflowName: builtinGitHubReviewAgentSpec(),
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
	spec.Files["workflows/openshell-task-runner.ts"] = builtinOpenShellTaskRunnerWorkflowSource
	return spec
}

func builtinGitHubReviewAgentSpec() Spec {
	spec := builtinSpec(BuiltinGitHubReviewAgentWorkflowName, builtinGitHubReviewAgentWorkflowSource)
	spec.Files["workflows/"+BuiltinGitHubReviewTaskRunnerName+".ts"] = builtinGitHubReviewTaskRunnerWorkflowSource
	return spec
}

// BuiltinWorkflowNames returns the registered built-in workflow names sorted,
// so callers (EnsureBuiltinWorkflow loops, registration round-trip tests) get
// a stable list independent of map iteration order.
func BuiltinWorkflowNames() []string {
	names := make([]string, 0, len(builtinWorkflows))
	for name := range builtinWorkflows {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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

func EnsureBuiltinWorkflow(ctx context.Context, st store.Store, ws, name string) error {
	spec, ok := BuiltinWorkflow(name)
	if !ok {
		return domain.ErrNotFound
	}

	builtinMu.Lock()
	defer builtinMu.Unlock()

	digest := SourceDigest(spec.Files)
	sourceRef := "builtin://workflows/" + name + "/versions/" + digest
	freshRunners := workflowRunnerNameSet(spec)
	if driverID, err := ResolveDriverID(ctx, st, ws, name); err == nil {
		current, bundleAvailable, manifest, err := activeBuiltInWorkflowState(ctx, st, ws, driverID)
		if err != nil {
			return err
		}
		// Refresh-on-deprecated (§4.6): a digest+bundle match still re-registers
		// when the active manifest declares a deprecated/fabricated runner or a
		// runner the freshly-derived set no longer contains.
		if current == digest && bundleAvailable && !activeManifestRunnersAreStale(manifest, freshRunners) {
			return nil
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	if _, _, err := BuildAndRegister(ctx, st, BuildAndRegisterOptions{
		WorkspaceKey:  ws,
		Name:          name,
		Entrypoint:    spec.Entrypoint,
		Files:         spec.Files,
		Activate:      true,
		SourceRef:     sourceRef,
		SourceDigest:  digest,
		CreatedBy:     "system",
		WorkDir:       builtinWorkflowWorkDir(),
		DeriveRunners: true,
		Trust:         domain.DriverTrustTrusted,
	}); err != nil {
		return fmt.Errorf("register built-in workflow %q: %w", name, err)
	}
	return nil
}

func activeBuiltInWorkflowState(ctx context.Context, st store.Store, ws, driverID string) (string, bool, map[string]string, error) {
	driverRecord, err := st.Drivers().Get(ctx, ws, driverID)
	if err != nil {
		return "", false, nil, fmt.Errorf("get built-in workflow driver %q: %w", driverID, err)
	}
	if strings.TrimSpace(driverRecord.ActiveVersionID) == "" {
		return "", false, nil, nil
	}
	version, err := st.DriverVersions().Get(ctx, ws, driverRecord.ActiveVersionID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", false, nil, nil
		}
		return "", false, nil, fmt.Errorf("get active built-in workflow version %q: %w", driverRecord.ActiveVersionID, err)
	}
	return strings.TrimSpace(version.SourceDigest), builtInWorkflowBundleAvailable(version), version.Manifest, nil
}

func builtInWorkflowBundleAvailable(version *domain.DriverVersion) bool {
	if version == nil || strings.TrimSpace(version.BundleRef) == "" || filepath.IsAbs(version.BundleRef) {
		return false
	}
	workDir := builtinWorkflowWorkDir()
	root := filepath.Clean(filepath.Join(workDir, filepath.FromSlash(version.BundleRef)))
	rel, err := filepath.Rel(workDir, root)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	for _, relFile := range []string{"manifest.json", filepath.Join("dist", "server.mjs")} {
		info, err := os.Stat(filepath.Join(root, relFile))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

func builtinWorkflowWorkDir() string {
	if dir := strings.TrimSpace(os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR")); dir != "" {
		return dir
	}
	workDir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return workDir
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
	if err := writeWorkflowBuildProject(buildRoot, spec.Files); err != nil {
		return "", "", err
	}
	output, err := runFlueBuild(ctx, buildRoot, destDir)
	output = RedactBuildDiagnostics(output)
	if err != nil {
		if output != "" {
			return "", output, fmt.Errorf("flue build failed: %s", output)
		}
		return "", "", err
	}
	serverPath := filepath.Join(destDir, "server.mjs")
	if _, err := os.Stat(serverPath); err != nil {
		return "", output, fmt.Errorf("flue build missing dist/server.mjs: %w", err)
	}
	return serverPath, output, nil
}

// submissionTrust resolves the trust level a BuildAndRegister submission
// stamps: empty defaults to UNTRUSTED (fail closed) because this is the
// external user path — only server-side callers like EnsureBuiltinWorkflow
// pass trusted explicitly, and nothing maps request input onto Trust.
func submissionTrust(trust domain.DriverTrustLevel) domain.DriverTrustLevel {
	if trust == "" {
		return domain.DriverTrustUntrusted
	}
	return trust
}

//nolint:funlen // Ordered build staging, diagnostics redaction, and registration share error context.
func BuildAndRegister(ctx context.Context, st store.Store, opts BuildAndRegisterOptions) (*driver.RegisterFlueResult, string, error) {
	if opts.SourceDigest == "" {
		opts.SourceDigest = SourceDigest(opts.Files)
	}
	if opts.SourceRef == "" {
		opts.SourceRef = "api://workflows/" + opts.Name + "/versions/" + opts.SourceDigest
	}
	if opts.CreatedBy == "" {
		opts.CreatedBy = "api"
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
	defer os.RemoveAll(buildRoot) //nolint:errcheck
	if err := writeWorkflowBuildProject(buildRoot, opts.Files); err != nil {
		return nil, "", err
	}
	outputDir := filepath.Join(buildRoot, "dist")
	output, err := runFlueBuild(ctx, buildRoot, outputDir)
	if err != nil {
		redacted := RedactBuildDiagnostics(output)
		if redacted != "" {
			return nil, redacted, fmt.Errorf("flue build failed: %s", redacted)
		}
		return nil, "", err
	}
	output = RedactBuildDiagnostics(output)
	result, err := driver.RegisterFlueDriver(ctx, st, driver.RegisterFlueOptions{
		WorkspaceKey:     opts.WorkspaceKey,
		WorkDir:          workDir,
		DistPath:         outputDir,
		DriverName:       opts.Name,
		WorkflowName:     strings.TrimSuffix(filepath.Base(opts.Entrypoint), filepath.Ext(opts.Entrypoint)),
		SourceRef:        opts.SourceRef,
		SourceDigest:     opts.SourceDigest,
		CreatedBy:        opts.CreatedBy,
		Activate:         opts.Activate,
		RunnerSpecs:      workflowRunnerSpecs(opts),
		Manifest:         opts.Manifest,
		BuildDiagnostics: output,
		Trust:            submissionTrust(opts.Trust),
	})
	if err != nil {
		return nil, output, err
	}
	return result, output, nil
}

// deprecatedWorkflowRunners are sibling runner files that must never be
// registered as selectable runners (§4.6): openshell-task-runner is a
// fail-closed stub with no real integration, so it is denied even though its
// source still ships in the bundle.
var deprecatedWorkflowRunners = map[string]struct{}{
	driver.OpenShellRunnerName: {},
}

func workflowRunnerSpecs(opts BuildAndRegisterOptions) []driver.DriverRunnerSpec {
	if len(opts.Runners) > 0 {
		return opts.Runners
	}
	if !opts.DeriveRunners {
		return nil
	}
	return deriveWorkflowRunnerSpecs(opts.Entrypoint, opts.Files)
}

func deriveWorkflowRunnerSpecs(entrypoint string, files map[string]string) []driver.DriverRunnerSpec {
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
func workflowRunnerNameSet(spec Spec) map[string]struct{} {
	runners := deriveWorkflowRunnerSpecs(spec.Entrypoint, spec.Files)
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
func activeManifestRunnersAreStale(manifest map[string]string, fresh map[string]struct{}) bool {
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

func ResolveDriverID(ctx context.Context, st store.Store, ws, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("workflow name is required: %w", domain.ErrInvalid)
	}
	driverRecord, err := st.Drivers().Get(ctx, ws, name)
	if err == nil {
		return driverRecord.DriverID, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return "", err
	}
	drivers, err := st.Drivers().List(ctx, ws, store.DriverFilter{Name: name, Limit: 1})
	if err != nil {
		return "", err
	}
	if len(drivers) == 0 {
		return "", domain.ErrNotFound
	}
	return drivers[0].DriverID, nil
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
	for raw, content := range in {
		rel, err := validateWorkflowFilePath(raw)
		if err != nil {
			return nil, err
		}
		if strings.ContainsRune(content, '\x00') {
			return nil, fmt.Errorf("%s contains binary content", rel)
		}
		out[rel] = content
	}
	return out, nil
}

func SourceDigest(files map[string]string) string {
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		hash.Write([]byte(key))
		hash.Write([]byte{0})
		hash.Write([]byte(files[key]))
		hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
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
	rel := filepath.ToSlash(strings.TrimSpace(raw))
	if rel == "" {
		return "", fmt.Errorf("file path is required")
	}
	if strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%s must be relative", rel)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
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
	switch filepath.Base(clean) {
	case "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb":
		return "", fmt.Errorf("%s is not allowed in workflow source uploads", clean)
	}
	return clean, nil
}

func writeWorkflowBuildProject(root string, files map[string]string) error {
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"type":"module","dependencies":{"@loom/sdk":"file:./node_modules/@loom/sdk","@flue/runtime":"file:./node_modules/@flue/runtime"}}`+"\n"), 0o644); err != nil {
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
	}
	if daytonaRoot, err := daytonaSDKRoot(); err == nil {
		links[filepath.Join("node_modules", "@daytona", "sdk")] = daytonaRoot
	} else if strings.TrimSpace(os.Getenv("DAYTONA_SDK_ROOT")) != "" {
		return err
	}
	for rel, target := range links {
		if _, err := os.Stat(target); err != nil {
			return fmt.Errorf("resolve Flue build dependency %s: %w", target, err)
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

func loomSDKRoot() (string, error) {
	if root := strings.TrimSpace(os.Getenv("LOOM_SDK_ROOT")); root != "" {
		return root, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(cwd, "sdk")
	if _, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil {
		return candidate, nil
	}
	return "", fmt.Errorf("local @loom/sdk package not found; set LOOM_SDK_ROOT")
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
	return "", fmt.Errorf("local @flue/runtime package not found; set LOOM_FLUE_RUNTIME_ROOT, FLUE_RUNTIME_ROOT, or FLUE_REPO")
}

func daytonaSDKRoot() (string, error) {
	candidates := []string{}
	if root := strings.TrimSpace(os.Getenv("DAYTONA_SDK_ROOT")); root != "" {
		candidates = append(candidates, root)
	}
	if repo := strings.TrimSpace(os.Getenv("FLUE_REPO")); repo != "" {
		candidates = append(candidates, filepath.Join(repo, "node_modules", ".pnpm", "node_modules", "@daytona", "sdk"))
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
	return "", fmt.Errorf("local @daytona/sdk package not found; set DAYTONA_SDK_ROOT")
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
		return output, fmt.Errorf("flue build failed: %s", output)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "server.mjs")); err != nil {
		return output, fmt.Errorf("flue build missing dist/server.mjs: %w", err)
	}
	return output, nil
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
		return nil, fmt.Errorf("flue not found on PATH; set LOOM_REAL_FLUE_CMD_JSON or LOOM_REAL_FLUE_CMD")
	}
	return []string{path}, nil
}
