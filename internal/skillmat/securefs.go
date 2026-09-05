package skillmat

import (
	"context"
	"io"
	"os"
)

type secureRoot interface {
	io.Closer
	ReadFile(name string, maxBytes int64) ([]byte, os.FileMode, error)
	ReadDir(name string) ([]string, error)
	Lstat(name string) (securePathInfo, error)
	MkdirAll(name string, perm os.FileMode) error
	CreateFile(name string, content []byte, perm os.FileMode) error
	CopyFile(sourceName, destinationName string, perm os.FileMode, maxBytes int64) (int64, error)
	AppendFile(name string, content []byte, perm os.FileMode) error
	Symlink(target, name string) error
	Rename(oldName, newName string) error
	// Swap atomically exchanges two existing paths on the same filesystem.
	Swap(firstName, secondName string) error
	// Lock holds a target-scoped advisory lock until its closer is closed.
	// The operating system releases it if the process exits or crashes.
	Lock(ctx context.Context, name string) (io.Closer, error)
	Remove(name string) error
	RemoveDir(name string) error
}

type securePathInfo struct {
	Mode       os.FileMode
	Size       int64
	LinkTarget string
}
