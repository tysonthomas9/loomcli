//go:build unix

package svcimpl

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type unixRootedPlatform struct {
	root *os.File
}

func openRootedPlatform(rootPath string, expected os.FileInfo) (rootedPlatform, error) {
	fd, err := unix.Open(rootPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, unixPathError("open scope root", err)
	}
	root := os.NewFile(uintptr(fd), rootPath)
	info, err := root.Stat()
	if err != nil {
		_ = root.Close()
		return nil, service.ErrInternal("failed to verify Unix scope root", err)
	}
	if !os.SameFile(expected, info) {
		_ = root.Close()
		return nil, service.ErrForbidden("scope root changed while opening Unix descriptor")
	}
	return &unixRootedPlatform{root: root}, nil
}

func (p *unixRootedPlatform) Close() error {
	return p.root.Close()
}

func (p *unixRootedPlatform) Stat(name string) (os.FileInfo, error) {
	if name != "." {
		parent, base, err := p.openParent(name)
		if err != nil {
			return nil, err
		}
		defer unix.Close(parent)
		var stat unix.Stat_t
		if err := unix.Fstatat(parent, base, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return nil, unixPathError("stat rooted path", err)
		}
		if stat.Mode&unix.S_IFMT == unix.S_IFLNK {
			return nil, service.ErrForbidden("refusing to follow symlink")
		}
	}
	f, err := p.open(name, unix.O_RDONLY|unix.O_NONBLOCK)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, service.ErrInternal("failed to stat rooted path", err)
	}
	return info, nil
}

func (p *unixRootedPlatform) List(name string) ([]os.DirEntry, error) {
	f, err := p.open(name, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, service.ErrInternal("failed to read rooted directory", err)
	}
	materialized := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		info, infoErr := statDirEntryAt(int(f.Fd()), entry.Name())
		if errors.Is(infoErr, unix.ENOENT) {
			continue
		}
		if infoErr != nil {
			return nil, unixPathError("stat rooted directory entry", infoErr)
		}
		materialized = append(materialized, materializedDirEntry{name: entry.Name(), info: info})
	}
	return materialized, nil
}

func statDirEntryAt(parent int, name string) (os.FileInfo, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(parent, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, err
	}
	switch uint32(stat.Mode) & unix.S_IFMT {
	case unix.S_IFLNK:
		return symlinkFileInfo{name: name}, nil
	case unix.S_IFREG, unix.S_IFDIR:
		fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return nil, err
		}
		f := os.NewFile(uintptr(fd), name)
		defer f.Close()
		return f.Stat()
	default:
		// Sockets, FIFOs, and device nodes cannot be opened O_RDONLY (a socket
		// returns ENXIO), so synthesize FileInfo from the stat we already hold.
		// Without this, listing a directory that holds one — e.g. the workspace
		// .loom folder with its live daemon.sock — fails the whole listing.
		return specialDirEntryInfo{name: name, size: stat.Size, ifmt: uint32(stat.Mode) & unix.S_IFMT}, nil
	}
}

// specialDirEntryInfo is an os.FileInfo for a non-regular, non-directory entry
// (socket/FIFO/device) that cannot be opened. ModTime is intentionally zero to
// stay portable across the Stat_t timestamp field differences between platforms.
type specialDirEntryInfo struct {
	name string
	size int64
	ifmt uint32
}

func (i specialDirEntryInfo) Name() string { return i.name }
func (i specialDirEntryInfo) Size() int64  { return i.size }
func (i specialDirEntryInfo) Mode() os.FileMode {
	switch i.ifmt {
	case unix.S_IFSOCK:
		return os.ModeSocket
	case unix.S_IFIFO:
		return os.ModeNamedPipe
	case unix.S_IFBLK:
		return os.ModeDevice
	case unix.S_IFCHR:
		return os.ModeDevice | os.ModeCharDevice
	default:
		return os.ModeIrregular
	}
}
func (i specialDirEntryInfo) ModTime() time.Time { return time.Time{} }
func (i specialDirEntryInfo) IsDir() bool        { return false }
func (i specialDirEntryInfo) Sys() any           { return nil }

func (p *unixRootedPlatform) Read(name string, limit int64) ([]byte, os.FileInfo, bool, error) {
	f, err := p.open(name, unix.O_RDONLY|unix.O_NONBLOCK)
	if err != nil {
		return nil, nil, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, false, service.ErrInternal("failed to stat rooted file", err)
	}
	if info.IsDir() {
		return nil, nil, false, service.ErrValidation("path is a directory, not a file")
	}
	if !info.Mode().IsRegular() {
		return nil, nil, false, service.ErrValidation("path is not a regular file")
	}
	return readRootedFile(f, info, limit)
}

func (p *unixRootedPlatform) Hash(name string) ([]byte, os.FileInfo, error) {
	f, err := p.open(name, unix.O_RDONLY|unix.O_NONBLOCK)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, service.ErrInternal("failed to stat rooted file", err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, service.ErrValidation("path is not a regular file")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return nil, nil, service.ErrInternal("failed to hash rooted file", err)
	}
	return hash.Sum(nil), info, nil
}

func (p *unixRootedPlatform) ReadVersioned(name string, limit int64) ([]byte, os.FileInfo, bool, []byte, error) {
	f, err := p.open(name, unix.O_RDONLY|unix.O_NONBLOCK)
	if err != nil {
		return nil, nil, false, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, false, nil, service.ErrInternal("failed to stat rooted file", err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, false, nil, service.ErrValidation("path is not a regular file")
	}
	return readAndHashRootedFile(f, info, limit)
}

func (p *unixRootedPlatform) Readlink(name string) (string, error) {
	parent, base, err := p.openParent(name)
	if err != nil {
		return "", err
	}
	defer unix.Close(parent)
	buffer := make([]byte, 64<<10)
	n, err := unix.Readlinkat(parent, base, buffer)
	if err != nil {
		return "", unixPathError("read rooted symlink", err)
	}
	if n == len(buffer) {
		return "", service.ErrPayloadTooLarge("symlink target exceeds limit")
	}
	return string(buffer[:n]), nil
}

func readRootedFile(f *os.File, info os.FileInfo, limit int64) ([]byte, os.FileInfo, bool, error) {
	truncated := limit >= 0 && info.Size() > limit
	reader := io.Reader(f)
	if limit >= 0 {
		reader = io.LimitReader(f, limit)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, nil, false, service.ErrInternal("failed to read rooted file", err)
	}
	return data, info, truncated, nil
}

func (p *unixRootedPlatform) open(name string, flags int) (*os.File, error) {
	if err := validateRootedName(name); err != nil {
		return nil, err
	}
	if name == "." {
		fd, err := unix.Openat(int(p.root.Fd()), ".", flags|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return nil, unixPathError("open scope descriptor", err)
		}
		return os.NewFile(uintptr(fd), p.root.Name()), nil
	}
	parent, base, err := p.openParent(name)
	if err != nil {
		return nil, err
	}
	defer unix.Close(parent)
	fd, err := unix.Openat(parent, base, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, unixPathError("open rooted path", err)
	}
	return os.NewFile(uintptr(fd), name), nil
}

func (p *unixRootedPlatform) openParent(name string) (int, string, error) {
	if err := validateRootedName(name); err != nil {
		return -1, "", err
	}
	parts := strings.Split(name, string(filepath.Separator))
	if len(parts) == 0 || name == "." {
		return -1, "", service.ErrValidation("path has no parent component")
	}
	fd, err := unix.Dup(int(p.root.Fd()))
	if err != nil {
		return -1, "", service.ErrInternal("failed to duplicate scope descriptor", err)
	}
	for _, part := range parts[:len(parts)-1] {
		next, openErr := openDirAt(fd, part)
		_ = unix.Close(fd)
		if openErr != nil {
			return -1, "", unixPathError("open rooted parent", openErr)
		}
		fd = next
	}
	return fd, parts[len(parts)-1], nil
}

func openDirAt(parent int, name string) (int, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(parent, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return -1, err
	}
	switch stat.Mode & unix.S_IFMT {
	case unix.S_IFLNK:
		return -1, unix.ELOOP
	case unix.S_IFDIR:
		fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(err, unix.ENOTDIR) {
			return -1, unix.ELOOP
		}
		return fd, err
	default:
		return -1, unix.ENOTDIR
	}
}

func unixPathError(action string, err error) error {
	switch {
	case errors.Is(err, unix.ENOENT):
		return service.ErrNotFound("path not found")
	case errors.Is(err, unix.ELOOP):
		return service.ErrForbidden("refusing to follow symlink")
	case errors.Is(err, unix.ENOTDIR):
		return service.ErrValidation("path component is not a directory")
	case errors.Is(err, unix.EACCES), errors.Is(err, unix.EPERM):
		return service.ErrForbidden("filesystem access denied")
	case errors.Is(err, unix.ENXIO), errors.Is(err, unix.ENODEV):
		// A socket cannot be opened O_RDONLY (ENXIO); treat these special-file
		// opens as a client error, matching the regular-file guard, rather than
		// surfacing a 500 when a listed socket is read.
		return service.ErrValidation("path is not a regular file")
	default:
		return service.ErrInternal(action, err)
	}
}

func (p *unixRootedPlatform) WriteAtomic(name string, content []byte) error {
	parent, base, err := p.openParent(name)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	perm, err := unixWritePermissions(parent, base)
	if err != nil {
		return err
	}
	tempName, temp, err := createUnixTemp(parent, perm)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Unlinkat(parent, tempName, 0) }()
	if err := writeUnixTemp(temp, content, perm); err != nil {
		return err
	}
	if err := unix.Renameat(parent, tempName, parent, base); err != nil {
		return unixPathError("rename rooted temporary file", err)
	}
	return nil
}

func unixWritePermissions(parent int, base string) (os.FileMode, error) {
	var stat unix.Stat_t
	err := unix.Fstatat(parent, base, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return 0o644, nil
	}
	if err != nil {
		return 0, unixPathError("stat rooted destination", err)
	}
	switch stat.Mode & unix.S_IFMT {
	case unix.S_IFLNK:
		return 0, service.ErrForbidden("refusing to overwrite symlink")
	case unix.S_IFDIR:
		return 0, service.ErrValidation("path is a directory, not a file")
	case unix.S_IFREG:
		return os.FileMode(stat.Mode & 0o777), nil
	default:
		return 0, service.ErrValidation("path is not a regular file")
	}
}

func createUnixTemp(parent int, perm os.FileMode) (string, *os.File, error) {
	for i := 0; i < 10; i++ {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, service.ErrInternal("failed to generate temporary file name", err)
		}
		name := ".loom-file-" + hex.EncodeToString(random[:]) + ".tmp"
		fd, err := unix.Openat(parent, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(perm))
		if err == nil {
			return name, os.NewFile(uintptr(fd), name), nil
		}
		if !errors.Is(err, unix.EEXIST) {
			return "", nil, unixPathError("create rooted temporary file", err)
		}
	}
	return "", nil, service.ErrInternal("failed to allocate temporary file", nil)
}

func writeUnixTemp(temp *os.File, content []byte, perm os.FileMode) error {
	defer temp.Close()
	if err := temp.Chmod(perm); err != nil {
		return service.ErrInternal("failed to set temporary file permissions", err)
	}
	if _, err := temp.Write(content); err != nil {
		return service.ErrInternal("failed to write temporary file", err)
	}
	if err := temp.Sync(); err != nil {
		return service.ErrInternal("failed to sync temporary file", err)
	}
	if err := temp.Close(); err != nil {
		return service.ErrInternal("failed to close temporary file", err)
	}
	return nil
}

func (p *unixRootedPlatform) MkdirAll(name string) error {
	if err := validateRootedName(name); err != nil {
		return err
	}
	fd, err := unix.Dup(int(p.root.Fd()))
	if err != nil {
		return service.ErrInternal("failed to duplicate scope descriptor", err)
	}
	for _, part := range strings.Split(name, string(filepath.Separator)) {
		next, openErr := openDirAt(fd, part)
		if errors.Is(openErr, unix.ENOENT) {
			if mkdirErr := unix.Mkdirat(fd, part, 0o755); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				_ = unix.Close(fd)
				return unixPathError("create rooted directory", mkdirErr)
			}
			next, openErr = openDirAt(fd, part)
		}
		if openErr != nil {
			_ = unix.Close(fd)
			if errors.Is(openErr, unix.ENOTDIR) {
				return service.ErrConflict("file exists at path")
			}
			return unixPathError("open rooted directory", openErr)
		}
		_ = unix.Close(fd)
		fd = next
	}
	return unix.Close(fd)
}

func (p *unixRootedPlatform) Remove(name string, directory bool) error {
	parent, base, err := p.openParent(name)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	flags := 0
	if directory {
		flags = unix.AT_REMOVEDIR
	}
	if err := unix.Unlinkat(parent, base, flags); err != nil {
		return unixPathError("remove rooted path", err)
	}
	return nil
}

func (p *unixRootedPlatform) Rename(from, to string) error {
	fromParent, fromBase, err := p.openParent(from)
	if err != nil {
		return err
	}
	defer unix.Close(fromParent)
	toParent, toBase, err := p.openParent(to)
	if err != nil {
		return err
	}
	defer unix.Close(toParent)
	if err := unix.Renameat(fromParent, fromBase, toParent, toBase); err != nil {
		return unixPathError("rename rooted path", err)
	}
	return nil
}
