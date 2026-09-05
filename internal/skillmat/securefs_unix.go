//go:build unix

package skillmat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type unixSecureRoot struct {
	fd int
}

func ensurePlatformSupported() error { return nil }

func openSecureRoot(rootPath string) (secureRoot, error) {
	fd, err := unix.Open(rootPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, securePathError(rootPath, err)
	}
	return &unixSecureRoot{fd: fd}, nil
}

func (r *unixSecureRoot) Close() error { return unix.Close(r.fd) }

func (r *unixSecureRoot) ReadFile(name string, maxBytes int64) ([]byte, os.FileMode, error) {
	parent, base, err := r.openParent(name)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = unix.Close(parent) }()
	fd, err := unix.Openat(parent, base, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, 0, securePathError(name, err)
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%s is not a regular file", name)
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return nil, 0, fmt.Errorf("%s exceeds maximum size of %d bytes", name, maxBytes)
	}
	reader := io.Reader(f)
	if maxBytes > 0 {
		reader = io.LimitReader(f, maxBytes+1)
	}
	b, err := io.ReadAll(reader)
	if err != nil {
		return nil, 0, err
	}
	if maxBytes > 0 && int64(len(b)) > maxBytes {
		return nil, 0, fmt.Errorf("%s exceeds maximum size of %d bytes", name, maxBytes)
	}
	return b, info.Mode(), nil
}

func (r *unixSecureRoot) ReadDir(name string) ([]string, error) {
	parent, base, err := r.openParent(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(parent) }()
	fd, err := openDirAtNoFollow(parent, base)
	if err != nil {
		return nil, securePathError(name, err)
	}
	dir := os.NewFile(uintptr(fd), name)
	entries, err := dir.ReadDir(-1)
	if closeErr := dir.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names, nil
}

func (r *unixSecureRoot) Lstat(name string) (securePathInfo, error) {
	parent, base, err := r.openParent(name)
	if err != nil {
		return securePathInfo{}, err
	}
	defer func() { _ = unix.Close(parent) }()
	var stat unix.Stat_t
	if err := unix.Fstatat(parent, base, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return securePathInfo{}, securePathError(name, err)
	}
	info := securePathInfo{Mode: fileModeFromUnixStat(stat), Size: stat.Size}
	if info.Mode&os.ModeSymlink != 0 {
		target, err := readlinkAt(parent, base)
		if err != nil {
			return securePathInfo{}, securePathError(name, err)
		}
		info.LinkTarget = target
	}
	return info, nil
}

func (r *unixSecureRoot) MkdirAll(name string, perm os.FileMode) error {
	if name == "." {
		return nil
	}
	if err := validateSecureName(name); err != nil {
		return err
	}
	fd, err := unix.Dup(r.fd)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	for _, part := range strings.Split(name, "/") {
		next, openErr := openDirAtNoFollow(fd, part)
		if errors.Is(openErr, unix.ENOENT) {
			if mkdirErr := unix.Mkdirat(fd, part, uint32(perm.Perm())); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				return securePathError(name, mkdirErr)
			}
			next, openErr = openDirAtNoFollow(fd, part)
		}
		if openErr != nil {
			return securePathError(name, openErr)
		}
		_ = unix.Close(fd)
		fd = next
	}
	return nil
}

func (r *unixSecureRoot) CreateFile(name string, content []byte, perm os.FileMode) error {
	parent, base, err := r.openParent(name)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(parent) }()
	fd, err := unix.Openat(parent, base, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(perm.Perm()))
	if err != nil {
		return securePathError(name, err)
	}
	f := os.NewFile(uintptr(fd), name)
	created := true
	defer func() {
		if created {
			_ = unix.Unlinkat(parent, base, 0)
		}
	}()
	if err := f.Chmod(perm.Perm()); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	created = false
	return nil
}

func (r *unixSecureRoot) CopyFile(sourceName, destinationName string, perm os.FileMode, maxBytes int64) (written int64, retErr error) {
	source, err := r.openBoundedRegularFile(sourceName, maxBytes)
	if err != nil {
		return 0, err
	}
	defer source.Close()

	destinationParent, destinationBase, err := r.openParent(destinationName)
	if err != nil {
		return 0, err
	}
	defer func() { _ = unix.Close(destinationParent) }()
	destinationFD, err := unix.Openat(destinationParent, destinationBase, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(perm.Perm()))
	if err != nil {
		return 0, securePathError(destinationName, err)
	}
	destination := os.NewFile(uintptr(destinationFD), destinationName)
	created := true
	defer func() {
		if created {
			_ = unix.Unlinkat(destinationParent, destinationBase, 0)
		}
	}()
	if err := destination.Chmod(perm.Perm()); err != nil {
		_ = destination.Close()
		return 0, err
	}
	reader := io.Reader(source)
	if maxBytes >= 0 {
		reader = io.LimitReader(source, maxBytes+1)
	}
	written, err = io.Copy(destination, reader)
	if err == nil && maxBytes >= 0 && written > maxBytes {
		err = fmt.Errorf("%s exceeds clone limit of %d bytes", sourceName, maxBytes)
	}
	if err == nil {
		err = destination.Sync()
	}
	if closeErr := destination.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return written, err
	}
	created = false
	return written, nil
}

func (r *unixSecureRoot) openBoundedRegularFile(name string, maxBytes int64) (*os.File, error) {
	sourceParent, sourceBase, err := r.openParent(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(sourceParent) }()
	sourceFD, err := unix.Openat(sourceParent, sourceBase, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, securePathError(name, err)
	}
	source := os.NewFile(uintptr(sourceFD), name)
	info, err := source.Stat()
	if err != nil {
		_ = source.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = source.Close()
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	if maxBytes >= 0 && info.Size() > maxBytes {
		_ = source.Close()
		return nil, fmt.Errorf("%s exceeds clone limit of %d bytes", name, maxBytes)
	}
	return source, nil
}

func (r *unixSecureRoot) AppendFile(name string, content []byte, perm os.FileMode) error {
	parent, base, err := r.openParent(name)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(parent) }()
	fd, err := unix.Openat(parent, base, unix.O_WRONLY|unix.O_APPEND|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, uint32(perm.Perm()))
	if err != nil {
		return securePathError(name, err)
	}
	f := os.NewFile(uintptr(fd), name)
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return fmt.Errorf("%s is not a regular file", name)
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (r *unixSecureRoot) Symlink(target, name string) error {
	parent, base, err := r.openParent(name)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(parent) }()
	if err := unix.Symlinkat(target, parent, base); err != nil {
		return securePathError(name, err)
	}
	return nil
}

func (r *unixSecureRoot) Rename(oldName, newName string) error {
	oldParent, oldBase, err := r.openParent(oldName)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(oldParent) }()
	newParent, newBase, err := r.openParent(newName)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(newParent) }()
	if err := unix.Renameat(oldParent, oldBase, newParent, newBase); err != nil {
		return securePathError(newName, err)
	}
	return nil
}

func (r *unixSecureRoot) Swap(firstName, secondName string) error {
	firstParent, firstBase, err := r.openParent(firstName)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(firstParent) }()
	secondParent, secondBase, err := r.openParent(secondName)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(secondParent) }()
	if err := swapAt(firstParent, firstBase, secondParent, secondBase); err != nil {
		return securePathError(secondName, err)
	}
	return nil
}

type advisoryLock struct{ file *os.File }

func (l *advisoryLock) Close() error {
	unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	return errors.Join(unlockErr, l.file.Close())
}

func (r *unixSecureRoot) Lock(ctx context.Context, name string) (io.Closer, error) {
	parent, base, err := r.openParent(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(parent) }()
	fd, err := unix.Openat(parent, base, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, securePathError(name, err)
	}
	file := os.NewFile(uintptr(fd), name)
	for {
		if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return &advisoryLock{file: file}, nil
		} else if !errors.Is(err, unix.EWOULDBLOCK) {
			_ = file.Close()
			return nil, securePathError(name, err)
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (r *unixSecureRoot) Remove(name string) error {
	parent, base, err := r.openParent(name)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(parent) }()
	var stat unix.Stat_t
	if err := unix.Fstatat(parent, base, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return securePathError(name, err)
	}
	flags := 0
	if stat.Mode&unix.S_IFMT == unix.S_IFDIR {
		flags = unix.AT_REMOVEDIR
	}
	if err := unix.Unlinkat(parent, base, flags); err != nil {
		return securePathError(name, err)
	}
	return nil
}

func (r *unixSecureRoot) RemoveDir(name string) error {
	parent, base, err := r.openParent(name)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(parent) }()
	if err := unix.Unlinkat(parent, base, unix.AT_REMOVEDIR); err != nil {
		return securePathError(name, err)
	}
	return nil
}

func (r *unixSecureRoot) openParent(name string) (int, string, error) {
	if err := validateSecureName(name); err != nil {
		return -1, "", err
	}
	parts := strings.Split(name, "/")
	fd, err := unix.Dup(r.fd)
	if err != nil {
		return -1, "", err
	}
	for _, part := range parts[:len(parts)-1] {
		next, openErr := openDirAtNoFollow(fd, part)
		_ = unix.Close(fd)
		if openErr != nil {
			return -1, "", securePathError(name, openErr)
		}
		fd = next
	}
	return fd, parts[len(parts)-1], nil
}

func openDirAtNoFollow(parent int, name string) (int, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(parent, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return -1, err
	}
	if stat.Mode&unix.S_IFMT == unix.S_IFLNK {
		return -1, unix.ELOOP
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return -1, unix.ENOTDIR
	}
	return unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
}

func validateSecureName(name string) error {
	if name == "" || name == "." || strings.HasPrefix(name, "/") || path.Clean(name) != name {
		return fmt.Errorf("unsafe rooted path %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("unsafe rooted path %q", name)
		}
	}
	return nil
}

func securePathError(name string, err error) error {
	if errors.Is(err, unix.ELOOP) {
		return fmt.Errorf("%s: refusing to follow symlink: %w", name, err)
	}
	if errors.Is(err, unix.ENOTDIR) {
		return fmt.Errorf("%s: path component is not a real directory: %w", name, err)
	}
	return &os.PathError{Op: "skill materialization", Path: name, Err: err}
}

func fileModeFromUnixStat(stat unix.Stat_t) os.FileMode {
	raw := uint32(stat.Mode) //nolint:unconvert // stat.Mode is uint16 on darwin; the conversion is required there.
	mode := os.FileMode(raw & 0o777)
	switch raw & uint32(unix.S_IFMT) {
	case uint32(unix.S_IFDIR):
		mode |= os.ModeDir
	case uint32(unix.S_IFLNK):
		mode |= os.ModeSymlink
	case uint32(unix.S_IFIFO):
		mode |= os.ModeNamedPipe
	case uint32(unix.S_IFSOCK):
		mode |= os.ModeSocket
	case uint32(unix.S_IFCHR):
		mode |= os.ModeDevice | os.ModeCharDevice
	case uint32(unix.S_IFBLK):
		mode |= os.ModeDevice
	case uint32(unix.S_IFREG):
	default:
		mode |= os.ModeIrregular
	}
	return mode
}

func readlinkAt(parent int, base string) (string, error) {
	for size := 256; size <= 64*1024; size *= 2 {
		buf := make([]byte, size)
		n, err := unix.Readlinkat(parent, base, buf)
		if err != nil {
			return "", err
		}
		if n < len(buf) {
			return string(buf[:n]), nil
		}
	}
	return "", fmt.Errorf("symlink target is too long")
}
