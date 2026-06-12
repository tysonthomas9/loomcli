package workflows

import (
	"context"
	"crypto/sha256"
	_ "embed"
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

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const BuiltinEpicRunnerWorkflowName = "epic-runner"

//go:embed builtin/epic-runner.ts
var builtinEpicRunnerWorkflowSource string

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
	// Trust is stamped server-side (§7 step 9 sandbox placement policy) and
	// is never plumbed from request input. BuildAndRegister is the external
	// submission path (workflows HTTP API), so empty defaults to UNTRUSTED —
	// fail closed; only EnsureBuiltinWorkflow passes trusted for the embedded
	// source-tree workflows.
	Trust domain.DriverTrustLevel
}

var builtinMu sync.Mutex

var builtinWorkflows = map[string]Spec{
	BuiltinEpicRunnerWorkflowName: {
		Entrypoint: "workflows/" + BuiltinEpicRunnerWorkflowName + ".ts",
		Files: map[string]string{
			"workflows/" + BuiltinEpicRunnerWorkflowName + ".ts": builtinEpicRunnerWorkflowSource,
		},
	},
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

	if _, err := ResolveDriverID(ctx, st, ws, name); err == nil {
		return nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}

	digest := SourceDigest(spec.Files)
	sourceRef := "builtin://workflows/" + name + "/versions/" + digest
	if _, _, err := BuildAndRegister(ctx, st, BuildAndRegisterOptions{
		WorkspaceKey: ws,
		Name:         name,
		Entrypoint:   spec.Entrypoint,
		Files:        spec.Files,
		Activate:     true,
		SourceRef:    sourceRef,
		SourceDigest: digest,
		CreatedBy:    "system",
		Trust:        domain.DriverTrustTrusted,
	}); err != nil {
		return fmt.Errorf("register built-in workflow %q: %w", name, err)
	}
	return nil
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
		return nil, output, err
	}
	result, err := driver.RegisterFlueDriver(ctx, st, driver.RegisterFlueOptions{
		WorkspaceKey: opts.WorkspaceKey,
		WorkDir:      workDir,
		DistPath:     outputDir,
		DriverName:   opts.Name,
		WorkflowName: strings.TrimSuffix(filepath.Base(opts.Entrypoint), filepath.Ext(opts.Entrypoint)),
		SourceRef:    opts.SourceRef,
		SourceDigest: opts.SourceDigest,
		CreatedBy:    opts.CreatedBy,
		Activate:     opts.Activate,
		Trust:        submissionTrust(opts.Trust),
	})
	if err != nil {
		return nil, output, err
	}
	return result, output, nil
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
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"type":"module","dependencies":{"@loom/sdk":"file:./node_modules/@loom/sdk"}}`+"\n"), 0o644); err != nil {
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
