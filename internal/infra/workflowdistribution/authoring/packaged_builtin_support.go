package authoring

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type absentPackagedBuiltinFS struct{}

func (absentPackagedBuiltinFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
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
		// WalkDir guarantees path is below srcRoot; filepath.Rel preserves
		// that containment when it is joined beneath the private dstRoot.
		//nolint:gosec // Validated embedded-FS relative path rooted under dstRoot.
		out, err := os.OpenFile(
			target,
			os.O_WRONLY|os.O_CREATE|os.O_TRUNC,
			info.Mode().Perm(),
		)
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
