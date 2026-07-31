package driver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/driver/nativearchive"
)

func archiveNativeDriverDist(distPath string) ([]byte, error) { //nolint:gocognit,cyclop,funlen // Archive traversal keeps every containment, entry-type, and byte-budget check in one security boundary.
	root, err := filepath.Abs(distPath)
	if err != nil {
		return nil, fmt.Errorf("resolve native Flue dist: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat native Flue dist: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("native Flue dist must be a directory")
	}

	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	entryCount := 0
	var extractedBytes int64
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.Mode().IsRegular() && !info.IsDir()) {
			return fmt.Errorf("native Flue dist contains unsupported file %q", relative)
		}
		var accountErr error
		entryCount, extractedBytes, accountErr = nativearchive.AccountEntry(
			entryCount,
			extractedBytes,
			info.Size(),
			info.Mode().IsRegular(),
		)
		if accountErr != nil {
			return fmt.Errorf("native Flue dist exceeds upload limits: %w", accountErr)
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		if _, err := nativearchive.CleanEntryName(header.Name); err != nil {
			return err
		}
		header.Uid, header.Gid, header.Uname, header.Gname = 0, 0, "", ""
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		file, err := os.Open(path) //nolint:gosec // path is produced by walking the explicit dist root
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(archive, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if walkErr != nil {
		_ = archive.Close()
		_ = compressed.Close()
		return nil, fmt.Errorf("archive native Flue dist: %w", walkErr)
	}
	if err := archive.Close(); err != nil {
		_ = compressed.Close()
		return nil, fmt.Errorf("finish native Flue archive: %w", err)
	}
	if err := compressed.Close(); err != nil {
		return nil, fmt.Errorf("compress native Flue archive: %w", err)
	}
	if err := nativearchive.ValidateArchiveSize(buffer.Len()); err != nil {
		return nil, fmt.Errorf("native Flue archive: %w", err)
	}
	return buffer.Bytes(), nil
}
