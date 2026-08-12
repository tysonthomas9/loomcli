package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	BuiltinBugFixAgentWorkflowName       = "bug-fix-agent"
	BuiltinReviewLoopAgentWorkflowName   = "review-loop-agent"
	BuiltinLocalReviewAgentWorkflowName  = "local-review-agent"
	BuiltinPromptAgentWorkflowName       = "prompt-agent"
)

// ErrBuildToolchainUnavailable distinguishes a deployment/profile that cannot
// materialize a workflow bundle from a malformed workflow or a store failure.
// Callers such as the unified agent API can map this operator-fixable condition
// to 503 instead of reporting an opaque internal error.
var ErrBuildToolchainUnavailable = errors.New("workflow build toolchain unavailable")

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

// IsBuiltinWorkflow reports whether name is a registered built-in workflow. It
// is a direct map lookup, unlike scanning the sorted BuiltinWorkflowNames slice.
func IsBuiltinWorkflow(name string) bool {
	_, ok := BuiltinWorkflow(name)
	return ok
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
	reuse, current, reuseMissingRunners, err := builtinReuseDecision(ctx, st, ws, name, freshRunners)
	if err != nil {
		return err
	}
	if reuse {
		// Registrations stamped via `loom workflow digest` carry the canonical
		// SourceDigest and hit this fast path with current == digest. A usable
		// driver at a different digest (pre-unification stacks, or a different
		// source revision) is still reused — never rebuilt — but the mismatch is
		// logged so version drift between the registered builtin and the serve
		// binary stays visible instead of silent.
		if current != digest {
			slog.Warn("builtin digest drift: reusing registered version despite source-digest mismatch",
				"workflow", name,
				"workspace", ws,
				"registered_digest", current,
				"embedded_digest", digest)
		}
		return nil
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
		if len(reuseMissingRunners) > 0 {
			slog.Warn("builtin runner manifest is missing runners and re-register failed; reusing the registered version",
				"workflow", name,
				"workspace", ws,
				"missing_runners", strings.Join(reuseMissingRunners, ","),
				"err", err.Error())
			return nil
		}
		return fmt.Errorf("register built-in workflow %q: %w", name, err)
	}
	return nil
}

// builtinReuseDecision decides whether the currently-registered builtin can be
// reused as-is. It returns reuse=true when the active version has a usable
// bundle on disk AND its manifest declares exactly the current runner set.
//
// We deliberately do NOT require the active version's source_digest to equal
// loom's embedded SourceDigest. A builtin staged out-of-band via `loom driver
// register` (the epic-runner smoke/e2e stack) records a different digest
// RECIPE for the SAME source, so demanding an exact match forced a source
// REBUILD of a perfectly good driver on every webui workflow-run. That rebuild
// fails closed in a `loom serve` process that has no bundling toolchain
// (@loom/sdk) on disk, surfacing as a misleading 500 "workflow not found" —
// while the CLI path (which resolves the same driver first) reused it and
// worked. Refresh-on-deprecated (§4.6) still fires: a stale/deprecated runner
// manifest, or a missing bundle, fails this check and re-registers.
//
// A manifest declaring only a SUBSET of the fresh runners (e.g. a stack that
// registered epic-runner with just local-task-runner) is NOT reused as-is: a
// run requesting a missing runner would pin that version and
// applyResolvedRunner would reject the child task. missing (non-empty only
// for such usable-but-subset registrations) tells the caller to re-register —
// and to fail OPEN onto the still-usable subset version, with a warning, if
// that rebuild cannot run here (the same toolchain-less serve), rather than
// failing runs the registered driver can serve.
//
// registeredDigest is the active version's recorded source_digest (empty when
// there is no active version), so the caller can log digest drift on reuse.
func builtinReuseDecision(ctx context.Context, st store.Store, ws, name string, fresh map[string]struct{}) (reuse bool, registeredDigest string, missing []string, err error) {
	driverID, err := ResolveDriverID(ctx, st, ws, name)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return false, "", nil, nil
		}
		return false, "", nil, err
	}
	current, bundleAvailable, manifest, err := activeBuiltInWorkflowState(ctx, st, ws, driverID)
	if err != nil {
		return false, "", nil, err
	}
	if !bundleAvailable || activeManifestRunnersAreStale(manifest, fresh) {
		return false, current, nil, nil
	}
	if missing = manifestMissingFreshRunners(manifest, fresh); len(missing) > 0 {
		return false, current, missing, nil
	}
	return true, current, nil, nil
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
			return "", output, redactedFlueBuildError(err, output)
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
			return nil, redacted, redactedFlueBuildError(err, redacted)
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

// manifestMissingFreshRunners returns the freshly-derived runner names that
// the active version's manifest does NOT declare, sorted. A non-empty result
// means the registered builtin can serve only a SUBSET of the current runners
// (e.g. scripts/test-runner-pr-e2e.sh registers epic-runner with only
// local-task-runner): a later run requesting a missing runner would pin this
// version and applyResolvedRunner would reject the child task run. Such a
// manifest must be refreshed when a rebuild is possible. Undecodable or empty
// manifests are the stale check's business, not this one's.
func manifestMissingFreshRunners(manifest map[string]string, fresh map[string]struct{}) []string {
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

// EnsureAndResolveDriver self-heals a builtin workflow (registering or
// refreshing its driver on demand) and then resolves the workflow name to its
// driver record. It is the shared resolve path for every HTTP surface that
// accepts a workflow name (workflow runs, trigger bindings); non-builtin names
// skip the heal and resolve directly.
func EnsureAndResolveDriver(ctx context.Context, st store.Store, ws, name string) (*domain.Driver, error) {
	if IsBuiltinWorkflow(name) {
		if err := EnsureBuiltinWorkflow(ctx, st, ws, name); err != nil {
			return nil, err
		}
	}
	return ResolveDriver(ctx, st, ws, name)
}

// ResolveDriver resolves a workflow name (or driver id) to its full driver
// record. It does not self-heal builtins — use EnsureAndResolveDriver for
// that. Callers that need more than the id (e.g. ActiveVersionID) should use
// this instead of ResolveDriverID followed by a second Drivers().Get.
func ResolveDriver(ctx context.Context, st store.Store, ws, name string) (*domain.Driver, error) {
	if name == "" {
		return nil, fmt.Errorf("workflow name is required: %w", domain.ErrInvalid)
	}
	driverRecord, err := st.Drivers().Get(ctx, ws, name)
	if err == nil {
		return driverRecord, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	drivers, err := st.Drivers().List(ctx, ws, store.DriverFilter{Name: name, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(drivers) == 0 {
		return nil, domain.ErrNotFound
	}
	return drivers[0], nil
}

func ResolveDriverID(ctx context.Context, st store.Store, ws, name string) (string, error) {
	driver, err := ResolveDriver(ctx, st, ws, name)
	if err != nil {
		return "", err
	}
	return driver.DriverID, nil
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
