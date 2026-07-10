package svcimpl

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// scopedRoot is a request-lifetime capability for one resolved file-browser
// scope. All user-controlled filesystem access goes through store.
type scopedRoot struct {
	identity    string
	workspaceID string
	scope       service.FileScope
	target      string
	repo        string
	path        string
	store       *rootedFileStore
}

func openScopedRoot(rootPath string) (*scopedRoot, error) {
	rootPath, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, service.ErrInternal("failed to resolve scope root", err)
	}
	info, err := os.Lstat(rootPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, service.ErrNotFound("scope root not found")
		}
		return nil, service.ErrInternal("failed to stat scope root", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, service.ErrForbidden("scope root must not be a symlink")
	}
	if !info.IsDir() {
		return nil, service.ErrValidation("scope root is not a directory")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, service.ErrInternal("failed to open scope root", err)
	}
	openedInfo, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		return nil, service.ErrInternal("failed to verify scope root", err)
	}
	if !os.SameFile(info, openedInfo) {
		_ = root.Close()
		return nil, service.ErrForbidden("scope root changed while it was opened")
	}
	identity, err := resolvedRootIdentity(rootPath, openedInfo)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	platform, err := openRootedPlatform(rootPath, info)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	return &scopedRoot{
		identity: identity,
		path:     rootPath,
		store:    &rootedFileStore{root: root, platform: platform},
	}, nil
}

func resolvedRootIdentity(rootPath string, openedInfo os.FileInfo) (string, error) {
	resolvedRootPath, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return "", service.ErrInternal("failed to resolve canonical scope root", err)
	}
	resolvedInfo, err := os.Stat(resolvedRootPath)
	if err != nil {
		return "", service.ErrInternal("failed to verify canonical scope root", err)
	}
	if !os.SameFile(openedInfo, resolvedInfo) {
		return "", service.ErrForbidden("scope root changed while resolving canonical path")
	}
	return foldFilesystemCase(filepath.Clean(resolvedRootPath)), nil
}

func (r *scopedRoot) Close() {
	if r != nil && r.store != nil && r.store.root != nil {
		if r.store.platform != nil {
			_ = r.store.platform.Close()
		}
		_ = r.store.root.Close()
	}
}

// rootedFileStore confines every operation to one scope. Unix operations use
// descriptor-relative no-follow primitives; other platforms use os.Root with
// explicit component validation as the containment fallback.
type rootedFileStore struct {
	root     *os.Root
	platform rootedPlatform
}

func normalizeFilePath(name string, allowRoot bool) (string, error) {
	if strings.IndexByte(name, 0) >= 0 {
		return "", service.ErrValidation("path contains a NUL byte")
	}
	if name == "" {
		if !allowRoot {
			return "", service.ErrValidation("path parameter is required")
		}
		return ".", nil
	}
	if filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", service.ErrForbidden("path outside root")
	}
	clean := filepath.Clean(name)
	if clean == "." {
		if !allowRoot {
			return "", service.ErrValidation("scope root cannot be mutated")
		}
		return clean, nil
	}
	for _, part := range strings.FieldsFunc(name, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if part == ".." {
			return "", service.ErrForbidden("path outside root")
		}
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return "", service.ErrForbidden("path outside root")
	}
	if hasGitSegment(clean) {
		return "", service.ErrForbidden(".git paths are not available")
	}
	return clean, nil
}

func hasGitSegment(name string) bool {
	for _, segment := range strings.Split(filepath.Clean(name), string(filepath.Separator)) {
		if strings.EqualFold(segment, ".git") {
			return true
		}
	}
	return false
}

func validateRootedName(name string) error {
	clean, err := normalizeFilePath(name, true)
	if err != nil {
		return err
	}
	if clean != name {
		return service.ErrValidation("path must be normalized")
	}
	return nil
}

func (s *rootedFileStore) ensureNoSymlinks(name string, allowMissing bool) error {
	if err := validateRootedName(name); err != nil {
		return err
	}
	if name == "." {
		return nil
	}
	if s.platform != nil {
		_, err := s.platform.Stat(name)
		if allowMissing && isNotFound(err) {
			return nil
		}
		return err
	}
	current := ""
	parts := strings.Split(name, string(filepath.Separator))
	for i, part := range parts {
		if current == "" {
			current = part
		} else {
			current = filepath.Join(current, part)
		}
		info, err := s.root.Lstat(current)
		if err != nil {
			if allowMissing && os.IsNotExist(err) {
				return nil
			}
			if os.IsNotExist(err) {
				return service.ErrNotFound("path not found")
			}
			return service.ErrInternal("failed to stat path", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return service.ErrForbidden("refusing to follow symlink")
		}
		if i < len(parts)-1 && !info.IsDir() {
			return service.ErrValidation("path component is not a directory")
		}
	}
	return nil
}

func (s *rootedFileStore) stat(name string) (os.FileInfo, error) {
	if err := s.ensureNoSymlinks(name, false); err != nil {
		return nil, err
	}
	info, err := s.lstat(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, service.ErrNotFound("path not found")
		}
		return nil, service.ErrInternal("failed to stat path", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, service.ErrForbidden("refusing to follow symlink")
	}
	return info, nil
}

func (s *rootedFileStore) lstat(name string) (os.FileInfo, error) {
	if s.platform != nil {
		return s.platform.Stat(name)
	}
	return s.root.Lstat(name)
}

func isNotFound(err error) bool {
	var serviceErr *service.ServiceError
	return errors.As(err, &serviceErr) && serviceErr.Kind == service.KindNotFound
}

func isServiceError(err error) bool {
	var serviceErr *service.ServiceError
	return errors.As(err, &serviceErr)
}

func (s *rootedFileStore) List(name string) ([]os.DirEntry, error) {
	if err := s.ensureNoSymlinks(name, false); err != nil {
		return nil, err
	}
	if s.platform != nil {
		return s.platform.List(name)
	}
	f, err := s.root.Open(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, service.ErrNotFound("directory not found")
		}
		return nil, service.ErrInternal("failed to open directory", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, service.ErrInternal("failed to stat directory", err)
	}
	if !info.IsDir() {
		return nil, service.ErrValidation("path is not a directory")
	}
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, service.ErrInternal("failed to read directory", err)
	}
	return entries, nil
}

func (s *rootedFileStore) Read(name string, limit int64) ([]byte, os.FileInfo, bool, error) {
	if err := s.ensureNoSymlinks(name, false); err != nil {
		return nil, nil, false, err
	}
	if s.platform != nil {
		return s.platform.Read(name, limit)
	}
	f, err := s.root.Open(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, false, service.ErrNotFound("file not found")
		}
		return nil, nil, false, service.ErrInternal("failed to open file", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, false, service.ErrInternal("failed to stat file", err)
	}
	if info.IsDir() {
		return nil, nil, false, service.ErrValidation("path is a directory, not a file")
	}
	if !info.Mode().IsRegular() {
		return nil, nil, false, service.ErrValidation("path is not a regular file")
	}
	truncated := limit >= 0 && info.Size() > limit
	reader := io.Reader(f)
	if limit >= 0 {
		reader = io.LimitReader(f, limit)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, nil, false, service.ErrInternal("failed to read file", err)
	}
	return data, info, truncated, nil
}

func (s *rootedFileStore) Hash(name string) ([]byte, os.FileInfo, error) {
	if err := s.ensureNoSymlinks(name, false); err != nil {
		return nil, nil, err
	}
	if s.platform != nil {
		return s.platform.Hash(name)
	}
	f, err := s.root.Open(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, service.ErrNotFound("file not found")
		}
		return nil, nil, service.ErrInternal("failed to open file", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, service.ErrInternal("failed to stat file", err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, service.ErrValidation("path is not a regular file")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return nil, nil, service.ErrInternal("failed to hash file", err)
	}
	return hash.Sum(nil), info, nil
}

func (s *rootedFileStore) ReadVersioned(name string, limit int64) ([]byte, os.FileInfo, bool, []byte, error) {
	if err := s.ensureNoSymlinks(name, false); err != nil {
		return nil, nil, false, nil, err
	}
	if s.platform != nil {
		return s.platform.ReadVersioned(name, limit)
	}
	f, err := s.root.Open(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, false, nil, service.ErrNotFound("file not found")
		}
		return nil, nil, false, nil, service.ErrInternal("failed to open file", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, false, nil, service.ErrInternal("failed to stat file", err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, false, nil, service.ErrValidation("path is not a regular file")
	}
	return readAndHashRootedFile(f, info, limit)
}

func (s *rootedFileStore) Readlink(name string) (string, error) {
	if err := validateRootedName(name); err != nil {
		return "", err
	}
	if s.platform != nil {
		return s.platform.Readlink(name)
	}
	target, err := s.root.Readlink(name)
	if err != nil {
		if os.IsNotExist(err) {
			return "", service.ErrNotFound("symlink not found")
		}
		return "", service.ErrInternal("failed to read symlink", err)
	}
	return target, nil
}

func readAndHashRootedFile(f *os.File, info os.FileInfo, limit int64) ([]byte, os.FileInfo, bool, []byte, error) {
	hash := sha256.New()
	if limit < 0 {
		data, err := io.ReadAll(io.TeeReader(f, hash))
		if err != nil {
			return nil, nil, false, nil, service.ErrInternal("failed to read file", err)
		}
		return data, info, false, hash.Sum(nil), nil
	}
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, nil, false, nil, service.ErrInternal("failed to read file", err)
	}
	_, _ = hash.Write(data)
	remainder, err := io.Copy(hash, f)
	if err != nil {
		return nil, nil, false, nil, service.ErrInternal("failed to hash file", err)
	}
	return data, info, remainder > 0, hash.Sum(nil), nil
}

func (s *rootedFileStore) WriteAtomic(name string, content []byte) error {
	if err := validateRootedName(name); err != nil {
		return err
	}
	if s.platform != nil {
		return s.platform.WriteAtomic(name, content)
	}
	return s.writeAtomicFallback(name, content)
}

func (s *rootedFileStore) writeAtomicFallback(name string, content []byte) error {
	parent := filepath.Dir(name)
	if err := s.ensureNoSymlinks(parent, false); err != nil {
		return err
	}
	parentInfo, err := s.stat(parent)
	if err != nil {
		return err
	}
	if !parentInfo.IsDir() {
		return service.ErrValidation("parent path is not a directory")
	}

	perm, err := s.writePermissions(name)
	if err != nil {
		return err
	}

	tempName, temp, err := s.createTemp(parent, perm)
	if err != nil {
		return err
	}
	closed := false
	cleanup := func() {
		if !closed {
			_ = temp.Close()
		}
		_ = s.root.Remove(tempName)
	}
	defer cleanup()
	if _, err := temp.Write(content); err != nil {
		return service.ErrInternal("failed to write temporary file", err)
	}
	if err := temp.Sync(); err != nil {
		return service.ErrInternal("failed to sync temporary file", err)
	}
	if err := temp.Close(); err != nil {
		return service.ErrInternal("failed to close temporary file", err)
	}
	closed = true
	if err := s.ensureNoSymlinks(parent, false); err != nil {
		return err
	}
	if err := s.root.Rename(tempName, name); err != nil {
		return service.ErrInternal("failed to save file", err)
	}
	return nil
}

func (s *rootedFileStore) writePermissions(name string) (os.FileMode, error) {
	info, err := s.root.Lstat(name)
	if os.IsNotExist(err) {
		return 0o644, nil
	}
	if err != nil {
		return 0, service.ErrInternal("failed to stat file", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 0, service.ErrForbidden("refusing to overwrite symlink")
	}
	if info.IsDir() {
		return 0, service.ErrValidation("path is a directory, not a file")
	}
	if !info.Mode().IsRegular() {
		return 0, service.ErrValidation("path is not a regular file")
	}
	return info.Mode().Perm(), nil
}

func (s *rootedFileStore) createTemp(parent string, perm os.FileMode) (string, *os.File, error) {
	for i := 0; i < 10; i++ {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, service.ErrInternal("failed to generate temporary file name", err)
		}
		base := ".loom-file-" + hex.EncodeToString(random[:]) + ".tmp"
		name := base
		if parent != "." {
			name = filepath.Join(parent, base)
		}
		f, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if err == nil {
			return name, f, nil
		}
		if !os.IsExist(err) {
			return "", nil, service.ErrInternal("failed to create temporary file", err)
		}
	}
	return "", nil, service.ErrInternal("failed to allocate temporary file", nil)
}

func (s *rootedFileStore) MkdirAll(name string) error {
	if err := validateRootedName(name); err != nil {
		return err
	}
	if s.platform != nil {
		return s.platform.MkdirAll(name)
	}
	current := ""
	for _, part := range strings.Split(name, string(filepath.Separator)) {
		if current == "" {
			current = part
		} else {
			current = filepath.Join(current, part)
		}
		info, err := s.root.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return service.ErrForbidden("refusing to follow symlink")
			}
			if !info.IsDir() {
				return service.ErrConflict("file exists at path")
			}
			continue
		}
		if !os.IsNotExist(err) {
			return service.ErrInternal("failed to stat directory", err)
		}
		if err := s.root.Mkdir(current, 0o755); err != nil {
			if os.IsExist(err) {
				return service.ErrConflict("file exists at path")
			}
			return service.ErrInternal("failed to create directory", err)
		}
	}
	return nil
}

func (s *rootedFileStore) Delete(name string, recursive bool) error {
	info, err := s.stat(name)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if !recursive {
			entries, err := s.List(name)
			if err != nil {
				return err
			}
			if len(entries) > 0 {
				return service.ErrConflict("directory not empty")
			}
		} else {
			containsGit, err := s.containsGit(name)
			if err != nil {
				return err
			}
			if containsGit {
				return service.ErrForbidden("directory contains .git metadata")
			}
			if err := s.removeTreeContents(name); err != nil {
				return err
			}
		}
	}
	if err := s.remove(name, info.IsDir()); err != nil {
		if os.IsNotExist(err) || isNotFound(err) {
			return service.ErrNotFound("path not found")
		}
		if isServiceError(err) {
			return err
		}
		return service.ErrInternal("failed to delete path", err)
	}
	return nil
}

func (s *rootedFileStore) remove(name string, directory bool) error {
	if s.platform != nil {
		return s.platform.Remove(name, directory)
	}
	return s.root.Remove(name)
}

func (s *rootedFileStore) removeTreeContents(name string) error {
	entries, err := s.List(name)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := s.removeTreeEntry(name, entry); err != nil {
			return err
		}
	}
	return nil
}

func (s *rootedFileStore) removeTreeEntry(parent string, entry os.DirEntry) error {
	if strings.EqualFold(entry.Name(), ".git") {
		return service.ErrForbidden("directory contains .git metadata")
	}
	child := filepath.Join(parent, entry.Name())
	if entry.Type()&os.ModeSymlink != 0 {
		return ignoreMissing(s.remove(child, false))
	}
	info, err := s.lstat(child)
	if err != nil {
		return normalizeDeleteError("failed to stat path during delete", err)
	}
	isDirectory := info.IsDir() && info.Mode()&os.ModeSymlink == 0
	if isDirectory {
		if err := s.removeTreeContents(child); err != nil {
			return err
		}
	}
	return normalizeDeleteError("failed to delete path", s.remove(child, isDirectory))
}

func ignoreMissing(err error) error {
	if err == nil || os.IsNotExist(err) || isNotFound(err) {
		return nil
	}
	return err
}

func normalizeDeleteError(message string, err error) error {
	if err == nil || os.IsNotExist(err) || isNotFound(err) {
		return nil
	}
	if isServiceError(err) {
		return err
	}
	return service.ErrInternal(message, err)
}

func (s *rootedFileStore) Move(from, to string, overwrite bool) error {
	if err := validateRootedName(from); err != nil {
		return err
	}
	if err := validateRootedName(to); err != nil {
		return err
	}
	info, err := s.stat(from)
	if err != nil {
		return err
	}
	if err := s.rejectProtectedTree(from, info); err != nil {
		return err
	}
	if err := s.ensureNoSymlinks(filepath.Dir(to), false); err != nil {
		return err
	}
	if err := s.validateMoveDestination(to, overwrite); err != nil {
		return err
	}
	if err := s.rejectProtectedTree(from, info); err != nil {
		return err
	}
	if err := s.rename(from, to); err != nil {
		if os.IsNotExist(err) || isNotFound(err) {
			return service.ErrNotFound("path not found")
		}
		if os.IsExist(err) {
			return service.ErrConflict("destination exists")
		}
		if isServiceError(err) {
			return err
		}
		return service.ErrInternal("failed to move path", err)
	}
	return nil
}

func (s *rootedFileStore) rename(from, to string) error {
	if s.platform != nil {
		return s.platform.Rename(from, to)
	}
	return s.root.Rename(from, to)
}

func (s *rootedFileStore) rejectProtectedTree(name string, info os.FileInfo) error {
	if !info.IsDir() {
		return nil
	}
	containsGit, err := s.containsGit(name)
	if err != nil {
		return err
	}
	if containsGit {
		return service.ErrForbidden("directory contains .git metadata")
	}
	return nil
}

func (s *rootedFileStore) validateMoveDestination(name string, overwrite bool) error {
	dest, err := s.lstat(name)
	if os.IsNotExist(err) || isNotFound(err) {
		return nil
	}
	if err != nil {
		if isServiceError(err) {
			return err
		}
		return service.ErrInternal("failed to stat destination path", err)
	}
	if dest.Mode()&os.ModeSymlink != 0 {
		return service.ErrForbidden("refusing to follow symlink")
	}
	if !overwrite {
		return service.ErrConflict("destination exists")
	}
	return nil
}

func (s *rootedFileStore) containsGit(name string) (bool, error) {
	entries, err := s.List(name)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), ".git") {
			return true, nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			continue
		}
		found, err := s.containsGit(filepath.Join(name, entry.Name()))
		if err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}

type rootedWalkVisitor func(relPath string, info os.FileInfo) error

func (s *rootedFileStore) Walk(ctxErr func() error, visitDir func(relPath string, entry os.DirEntry) (bool, error), visitFile rootedWalkVisitor) error {
	return s.walkDir(".", ctxErr, visitDir, visitFile)
}

func (s *rootedFileStore) walkDir(dir string, ctxErr func() error, visitDir func(string, os.DirEntry) (bool, error), visitFile rootedWalkVisitor) error {
	if err := ctxErr(); err != nil {
		return err
	}
	entries, err := s.List(dir)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if err := ctxErr(); err != nil {
			return err
		}
		relPath := entry.Name()
		if dir != "." {
			relPath = filepath.Join(dir, entry.Name())
		}
		if strings.EqualFold(entry.Name(), ".git") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if entry.IsDir() {
			descend, err := visitDir(relPath, entry)
			if err != nil {
				return err
			}
			if descend {
				if err := s.walkDir(relPath, ctxErr, visitDir, visitFile); err != nil {
					return err
				}
			}
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return service.ErrInternal("failed to stat file", err)
		}
		if err := visitFile(relPath, info); err != nil {
			return err
		}
	}
	return nil
}

func (s *rootedFileStore) absolute(clean string) string {
	if clean == "." {
		return s.root.Name()
	}
	return filepath.Join(s.root.Name(), clean)
}
