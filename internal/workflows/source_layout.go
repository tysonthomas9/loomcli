package workflows

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
)

const WorkflowSourceSchemaVersion = "1"

type SourceManifest struct {
	SchemaVersion string                    `json:"schema_version"`
	DriverID      string                    `json:"driver_id"`
	Entrypoint    string                    `json:"entrypoint"`
	Template      string                    `json:"template,omitempty"`
	Dependencies  map[string]string         `json:"dependencies,omitempty"`
	Runners       []driver.DriverRunnerSpec `json:"runners,omitempty"`
}

type LocalSource struct {
	Root     string                    `json:"root"`
	Manifest SourceManifest            `json:"manifest"`
	Files    map[string]string         `json:"-"`
	Runners  []driver.DriverRunnerSpec `json:"runners,omitempty"`
}

func CloneBuiltinSource(name, outDir string) (*SourceManifest, error) {
	spec, ok := BuiltinWorkflow(name)
	if !ok {
		return nil, fmt.Errorf("built-in workflow %q: %w", name, domain.ErrNotFound)
	}
	outDir = strings.TrimSpace(outDir)
	if outDir == "" {
		return nil, fmt.Errorf("output directory required: %w", domain.ErrInvalid)
	}
	manifest := SourceManifest{
		SchemaVersion: WorkflowSourceSchemaVersion,
		DriverID:      name,
		Entrypoint:    spec.Entrypoint,
		Template:      "builtin://" + name,
		Dependencies:  dependencyManifest(spec.Files),
		Runners:       deriveWorkflowRunnerSpecs(spec.Entrypoint, spec.Files),
	}
	if err := writeSourceManifest(outDir, manifest); err != nil {
		return nil, err
	}
	for rel, content := range spec.Files {
		if err := writeNewSourceFile(outDir, rel, content); err != nil {
			return nil, err
		}
	}
	return &manifest, nil
}

//nolint:funlen // Source validation is a linear pipeline; splitting it makes the contract harder to audit.
func ReadLocalSource(workflow, sourceDir string) (*LocalSource, error) {
	sourceDir = strings.TrimSpace(sourceDir)
	if sourceDir == "" {
		return nil, fmt.Errorf("source directory required: %w", domain.ErrInvalid)
	}
	manifest, err := readSourceManifest(sourceDir)
	if err != nil {
		return nil, err
	}
	manifest.DriverID = strings.TrimSpace(manifest.DriverID)
	if manifest.DriverID == "" {
		manifest.DriverID = strings.TrimSpace(workflow)
	}
	if manifest.DriverID == "" {
		return nil, fmt.Errorf("workflow driver_id required: %w", domain.ErrInvalid)
	}
	if workflow = strings.TrimSpace(workflow); workflow != "" && workflow != manifest.DriverID {
		return nil, fmt.Errorf("workflow %q does not match source driver_id %q: %w", workflow, manifest.DriverID, domain.ErrInvalid)
	}
	if manifest.Entrypoint == "" {
		manifest.Entrypoint = filepath.ToSlash(filepath.Join("workflows", manifest.DriverID+".ts"))
	}
	if err := ValidateWorkflowEntrypoint(manifest.DriverID, manifest.Entrypoint); err != nil {
		return nil, err
	}
	if err := validateSourceDependencies(manifest.Dependencies); err != nil {
		return nil, err
	}
	files, err := readWorkflowSourceFiles(sourceDir)
	if err != nil {
		return nil, err
	}
	files, err = ValidateWorkflowFiles(files)
	if err != nil {
		return nil, err
	}
	if _, ok := files[manifest.Entrypoint]; !ok {
		return nil, fmt.Errorf("entrypoint %s not found in source files: %w", manifest.Entrypoint, domain.ErrInvalid)
	}
	runners := []driver.DriverRunnerSpec(nil)
	if len(manifest.Runners) > 0 {
		runners = driver.NormalizeDriverRunnerSpecs(manifest.Runners)
		if err := driver.ValidateDriverRunnerSpecs(runners); err != nil {
			return nil, err
		}
	}
	return &LocalSource{
		Root:     sourceDir,
		Manifest: manifest,
		Files:    files,
		Runners:  runners,
	}, nil
}

func SourceManifestProvenance(manifest SourceManifest) map[string]string {
	out := map[string]string{}
	if schema := strings.TrimSpace(manifest.SchemaVersion); schema != "" {
		out["workflow_source_schema_version"] = schema
	}
	if template := strings.TrimSpace(manifest.Template); template != "" {
		out["workflow_template"] = template
	}
	if len(manifest.Dependencies) > 0 {
		data, err := json.Marshal(manifest.Dependencies)
		if err == nil {
			out["workflow_dependencies"] = string(data)
		}
	}
	return out
}

func writeSourceManifest(root string, manifest SourceManifest) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create workflow source root: %w", err)
	}
	path := filepath.Join(root, "workflow.json")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists: %w", path, domain.ErrAlreadyExists)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat workflow manifest: %w", err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode workflow manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write workflow manifest: %w", err)
	}
	return nil
}

func writeNewSourceFile(root, rel, content string) error {
	rel, err := validateWorkflowFilePath(rel)
	if err != nil {
		return err
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists: %w", path, domain.ErrAlreadyExists)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat workflow source file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create workflow source parent: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write workflow source file: %w", err)
	}
	return nil
}

func readSourceManifest(root string) (SourceManifest, error) {
	data, err := os.ReadFile(filepath.Join(root, "workflow.json")) //nolint:gosec // root is the explicit workflow source directory; manifest name is fixed.
	if err != nil {
		return SourceManifest{}, fmt.Errorf("read workflow manifest: %w", err)
	}
	var manifest SourceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return SourceManifest{}, fmt.Errorf("decode workflow manifest: %w", err)
	}
	return manifest, nil
}

func readWorkflowSourceFiles(root string) (map[string]string, error) {
	files := map[string]string{}
	workflowsRoot := filepath.Join(root, "workflows")
	if err := filepath.WalkDir(workflowsRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if filepath.Ext(rel) != ".ts" {
			return fmt.Errorf("%s is not a TypeScript workflow source file: %w", rel, domain.ErrInvalid)
		}
		data, err := os.ReadFile(path) //nolint:gosec // path is produced by WalkDir under the source root and validated before use.
		if err != nil {
			return err
		}
		files[rel] = string(data)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("read workflow source files: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("workflow source directory has no .ts files: %w", domain.ErrInvalid)
	}
	return files, nil
}

func dependencyManifest(files map[string]string) map[string]string {
	deps := map[string]string{
		"@loom/sdk":     "local",
		"@flue/runtime": "local",
	}
	if sourceUses(files, "@daytona/sdk") {
		deps["@daytona/sdk"] = "optional-local"
	}
	return deps
}

func sourceUses(files map[string]string, needle string) bool {
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if strings.Contains(files[key], needle) {
			return true
		}
	}
	return false
}

func validateSourceDependencies(deps map[string]string) error {
	for name, mode := range deps {
		name = strings.TrimSpace(name)
		mode = strings.TrimSpace(mode)
		switch name {
		case "@loom/sdk", "@flue/runtime", "@daytona/sdk":
		default:
			return fmt.Errorf("workflow dependency %q is not supported in phase 1: %w", name, domain.ErrInvalid)
		}
		switch mode {
		case "", "local", "optional-local":
		default:
			return fmt.Errorf("workflow dependency %q has unsupported mode %q: %w", name, mode, domain.ErrInvalid)
		}
	}
	return nil
}
