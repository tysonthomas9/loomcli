package svcimpl

import (
	"os"
	"time"
)

// rootedPlatform supplies descriptor-relative primitives on platforms that
// can enforce no-follow traversal atomically. A nil implementation uses the
// os.Root fallback.
type rootedPlatform interface {
	Close() error
	Stat(name string) (os.FileInfo, error)
	List(name string) ([]os.DirEntry, error)
	Read(name string, limit int64) ([]byte, os.FileInfo, bool, error)
	ReadVersioned(name string, limit int64) ([]byte, os.FileInfo, bool, []byte, error)
	Hash(name string) ([]byte, os.FileInfo, error)
	Readlink(name string) (string, error)
	WriteAtomic(name string, content []byte) error
	MkdirAll(name string) error
	Remove(name string, directory bool) error
	Rename(from, to string) error
}

type materializedDirEntry struct {
	name string
	info os.FileInfo
}

func (e materializedDirEntry) Name() string      { return e.name }
func (e materializedDirEntry) IsDir() bool       { return e.info.IsDir() }
func (e materializedDirEntry) Type() os.FileMode { return e.info.Mode().Type() }
func (e materializedDirEntry) Info() (os.FileInfo, error) {
	return e.info, nil
}

type symlinkFileInfo struct{ name string }

func (i symlinkFileInfo) Name() string       { return i.name }
func (i symlinkFileInfo) Size() int64        { return 0 }
func (i symlinkFileInfo) Mode() os.FileMode  { return os.ModeSymlink }
func (i symlinkFileInfo) ModTime() time.Time { return time.Time{} }
func (i symlinkFileInfo) IsDir() bool        { return false }
func (i symlinkFileInfo) Sys() any           { return nil }
