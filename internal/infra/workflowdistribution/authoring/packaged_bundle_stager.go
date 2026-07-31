package authoring

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	appworkflowauthoring "github.com/tysonthomas9/loomcli/internal/app/workflowauthoring"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
)

//nolint:funlen // Keep temporary-root copy, bundle registration, failure cleanup, and cleanup-root handoff in one compensating staging transaction.
func stagePackagedBuiltin(
	options appworkflowauthoring.BuildOptions,
	source fs.FS,
) (appworkflowauthoring.StagedBundle, bool, error) {
	if source == nil ||
		!options.Trust.Trusted() ||
		!workflowcatalog.IsBuiltinWorkflowName(strings.TrimSpace(options.Name)) {
		return nil, false, nil
	}
	distPath := filepath.ToSlash(filepath.Join("builtin-dist", options.Name, "dist"))
	if _, err := fs.Stat(
		source,
		filepath.ToSlash(filepath.Join(distPath, "server.mjs")),
	); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, true, fmt.Errorf("stat packaged built-in workflow bundle: %w", err)
	}
	matches, err := packagedBuiltinDigestMatches(source, distPath, options.SourceDigest)
	if err != nil {
		return nil, true, err
	}
	if !matches {
		return nil, false, nil
	}

	workDir, buildRoot, err := createPackagedBuiltinBuildRoot(options)
	if err != nil {
		return nil, true, err
	}
	outputDir := filepath.Join(buildRoot, "dist")
	if err := copyPackagedBuiltinTree(source, distPath, outputDir); err != nil {
		_ = os.RemoveAll(buildRoot)
		return nil, true, err
	}
	runners := legacyRunnerSpecs(options.Runners)
	if len(runners) == 0 && options.DeriveRunners {
		runners = deriveWorkflowRunnerSpecs(options.Entrypoint, options.Files)
	}
	staged, err := driver.StageFlueDriverBundle(driver.RegisterFlueOptions{
		WorkspaceKey: options.WorkspaceKey,
		WorkDir:      workDir,
		DistPath:     outputDir,
		DriverName:   options.Name,
		DriverID:     options.DriverID,
		WorkflowName: strings.TrimSuffix(
			filepath.Base(options.Entrypoint),
			filepath.Ext(options.Entrypoint),
		),
		SourceRef:    options.SourceRef,
		SourceDigest: options.SourceDigest,
		RunnerSpecs:  runners,
		Manifest:     options.Manifest,
		Trust:        options.Trust,
	})
	if err != nil {
		_ = os.RemoveAll(buildRoot)
		return nil, true, err
	}
	return &legacyStagedBundle{
		staged:      staged,
		cleanupRoot: buildRoot,
	}, true, nil
}

func createPackagedBuiltinBuildRoot(
	options appworkflowauthoring.BuildOptions,
) (string, string, error) {
	workDir := strings.TrimSpace(options.WorkDir)
	if workDir == "" {
		workDir = builtinWorkflowWorkDir()
	}
	buildParent := filepath.Join(workDir, ".loom", "workflow-builds")
	if err := os.MkdirAll(buildParent, 0o755); err != nil {
		return "", "", fmt.Errorf("create packaged workflow staging root: %w", err)
	}
	buildRoot, err := os.MkdirTemp(buildParent, options.Name+"-packaged-*")
	if err != nil {
		return "", "", fmt.Errorf("create packaged workflow staging directory: %w", err)
	}
	return workDir, buildRoot, nil
}
