package skillmat

import (
	"io"
	"os"
)

type secureRoot interface {
	io.Closer
	ReadFile(name string, maxBytes int64) ([]byte, os.FileMode, error)
	Lstat(name string) (securePathInfo, error)
	MkdirAll(name string, perm os.FileMode) error
	CreateFile(name string, content []byte, perm os.FileMode) error
	AppendFile(name string, content []byte, perm os.FileMode) error
	Symlink(target, name string) error
	Rename(oldName, newName string) error
	Remove(name string) error
	RemoveDir(name string) error
}

type securePathInfo struct {
	Mode       os.FileMode
	LinkTarget string
}
