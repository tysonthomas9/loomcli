package workflows

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
)

type absentPackagedBuiltinFS struct{}

func (absentPackagedBuiltinFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// registerPackagedBuiltinWorkflow activates a build-time generated bundle when
// the sidecar carries one for the exact embedded source digest. Packaged apps
// therefore never need the source-tree @loom/sdk or Flue toolchain merely to
// refresh a managed built-in after an upgrade. Ordinary source builds compile
// with an empty FS and fall through to BuildAndRegister.
func registerPackagedBuiltinWorkflow(
	ctx context.Context,
	st DriverCatalog,
	ws, name string,
	spec Spec,
	digest string,
) (bool, error) {
	return registerPackagedBuiltinWorkflowFromFS(ctx, st, ws, name, spec, digest, packagedBuiltinFS)
}

func registerPackagedBuiltinWorkflowFromFS(
	ctx context.Context,
	st DriverCatalog,
	ws, name string,
	spec Spec,
	digest string,
	source fs.FS,
) (bool, error) {
	distPath := filepath.ToSlash(filepath.Join("builtin-dist", name, "dist"))
	if _, err := fs.Stat(source, filepath.ToSlash(filepath.Join(distPath, "server.mjs"))); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return true, fmt.Errorf("stat packaged built-in workflow bundle: %w", err)
	}
	matches, err := packagedBuiltinDigestMatches(source, distPath, digest)
	if err != nil {
		return true, err
	}
	if !matches {
		// Never activate an output generated from different source. A source
		// checkout may rebuild dynamically; strict packaged startup will fail
		// closed if that toolchain is unavailable.
		return false, nil
	}

	workDir := builtinWorkflowWorkDir()
	buildParent := filepath.Join(workDir, ".loom", "workflow-builds")
	if err := os.MkdirAll(buildParent, 0o755); err != nil {
		return true, fmt.Errorf("create packaged workflow staging root: %w", err)
	}
	buildRoot, err := os.MkdirTemp(buildParent, name+"-packaged-*")
	if err != nil {
		return true, fmt.Errorf("create packaged workflow staging directory: %w", err)
	}
	defer os.RemoveAll(buildRoot) //nolint:errcheck
	outputDir := filepath.Join(buildRoot, "dist")
	if err := copyPackagedBuiltinTree(source, distPath, outputDir); err != nil {
		return true, err
	}
	if err := registerPackagedBuiltinBundle(ctx, st, ws, name, spec, digest, workDir, outputDir); err != nil {
		return true, err
	}
	return true, nil
}

func registerPackagedBuiltinBundle(
	ctx context.Context,
	st DriverCatalog,
	ws, name string,
	spec Spec,
	digest, workDir, outputDir string,
) error {
	_, err := driver.RegisterFlueDriver(ctx, st, driver.RegisterFlueOptions{
		WorkspaceKey: ws,
		WorkDir:      workDir,
		DistPath:     outputDir,
		DriverName:   name,
		DriverID:     name,
		WorkflowName: strings.TrimSuffix(filepath.Base(spec.Entrypoint), filepath.Ext(spec.Entrypoint)),
		SourceRef:    "builtin://workflows/" + name + "/versions/" + digest,
		SourceDigest: digest,
		CreatedBy:    "system",
		Activate:     true,
		RunnerSpecs:  deriveWorkflowRunnerSpecs(spec.Entrypoint, spec.Files),
		Trust:        domain.DriverTrustTrusted,
	})
	if err != nil {
		return fmt.Errorf("register packaged built-in workflow %q: %w", name, err)
	}
	return nil
}

func packagedBuiltinDigestMatches(source fs.FS, distPath, digest string) (bool, error) {
	markerPath := filepath.ToSlash(filepath.Join(distPath, "source-digest.txt"))
	content, err := fs.ReadFile(source, markerPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read packaged built-in workflow source digest: %w", err)
	}
	return strings.TrimSpace(string(content)) == strings.TrimSpace(digest), nil
}

func copyPackagedBuiltinTree(source fs.FS, srcRoot, dstRoot string) error {
	return fs.WalkDir(source, srcRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dstRoot, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := source.Open(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			_ = in.Close()
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm()) //nolint:gosec // target is an embedded-FS relative path rooted under dstRoot
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeOutErr := out.Close()
		closeInErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeOutErr != nil {
			return closeOutErr
		}
		return closeInErr
	})
}
