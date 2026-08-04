package svcimpl

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const (
	maxDirectoryVersionEntries = 10_000
	maxDirectoryVersionBytes   = 64 << 20
)

// pathLockSet serializes Loom browser mutations by canonical scope/path. It
// cannot provide transactional CAS against terminal or agent processes that
// modify the same files without using this in-process lock.
type pathLockSet struct {
	mu    sync.Mutex
	locks map[string]*referencedPathLock
}

type referencedPathLock struct {
	mu   sync.Mutex
	refs int
}

func (s *pathLockSet) lock(identity string, paths ...string) func() {
	keys := canonicalMutationLockKeys(identity, paths...)
	s.mu.Lock()
	if s.locks == nil {
		s.locks = make(map[string]*referencedPathLock)
	}
	locks := make([]*referencedPathLock, 0, len(keys))
	for _, key := range keys {
		entry := s.locks[key]
		if entry == nil {
			entry = &referencedPathLock{}
			s.locks[key] = entry
		}
		entry.refs++
		locks = append(locks, entry)
	}
	s.mu.Unlock()
	for _, entry := range locks {
		entry.mu.Lock()
	}
	return func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].mu.Unlock()
		}
		s.mu.Lock()
		for i, key := range keys {
			locks[i].refs--
			if locks[i].refs == 0 {
				delete(s.locks, key)
			}
		}
		s.mu.Unlock()
	}
}

func canonicalMutationLockKeys(identity string, paths ...string) []string {
	identity = foldFilesystemCase(filepath.Clean(identity))
	seen := make(map[string]struct{})
	for _, path := range paths {
		path = foldFilesystemCase(filepath.Clean(path))
		for path != "." && path != "" {
			seen[identity+"\x00"+path] = struct{}{}
			next := filepath.Dir(path)
			if next == path {
				break
			}
			path = next
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// foldFilesystemCase produces a stable key for Unicode simple case aliases.
// Some scope roots live on case-sensitive filesystems, where this deliberately
// over-serializes names that would be distinct on disk.
func foldFilesystemCase(value string) string {
	var folded strings.Builder
	folded.Grow(len(value))
	for _, r := range value {
		minimum := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < minimum {
				minimum = next
			}
		}
		folded.WriteRune(minimum)
	}
	return folded.String()
}

func versionForPath(store *rootedFileStore, path string) (string, os.FileInfo, error) {
	info, err := store.stat(path)
	if err != nil {
		return "", nil, err
	}
	if info.Mode().IsRegular() {
		sum, hashedInfo, err := store.Hash(path)
		if err != nil {
			return "", nil, err
		}
		return "sha256:" + hex.EncodeToString(sum), hashedInfo, nil
	}
	if !info.IsDir() {
		return "", nil, service.ErrValidation("path is not a regular file or directory")
	}
	h := sha256.New()
	entries := 0
	var bytes int64
	if err := hashDirectoryManifest(store, path, ".", h, &entries, &bytes); err != nil {
		return "", nil, err
	}
	return "dir-sha256:" + hex.EncodeToString(h.Sum(nil)), info, nil
}

func hashDirectoryManifest(store *rootedFileStore, root, relative string, h hash.Hash, entries *int, bytes *int64) error {
	manifest := directoryManifest{store: store, root: root, hash: h, entries: entries, bytes: bytes}
	return manifest.hashDirectory(relative)
}

type directoryManifest struct {
	store   *rootedFileStore
	root    string
	hash    hash.Hash
	entries *int
	bytes   *int64
}

func (m *directoryManifest) hashDirectory(relative string) error {
	current := m.root
	if relative != "." {
		current = filepath.Join(m.root, relative)
	}
	dirEntries, err := m.store.List(current)
	if err != nil {
		return err
	}
	sort.Slice(dirEntries, func(i, j int) bool { return dirEntries[i].Name() < dirEntries[j].Name() })
	for _, entry := range dirEntries {
		if strings.EqualFold(entry.Name(), ".git") {
			return service.ErrForbidden("directory contains .git metadata")
		}
		*m.entries++
		if *m.entries > maxDirectoryVersionEntries {
			return service.ErrPayloadTooLarge("directory version manifest exceeds entry limit")
		}
		childRelative := entry.Name()
		if relative != "." {
			childRelative = filepath.Join(relative, entry.Name())
		}
		if err := m.hashEntry(entry, childRelative); err != nil {
			return err
		}
	}
	return nil
}

func (m *directoryManifest) hashEntry(entry os.DirEntry, relative string) error {
	childPath := filepath.Join(m.root, relative)
	switch {
	case entry.Type()&os.ModeSymlink != 0:
		target, err := m.store.Readlink(childPath)
		if err != nil {
			return err
		}
		if int64(len(target)) > maxDirectoryVersionBytes-*m.bytes {
			return service.ErrPayloadTooLarge("directory version manifest exceeds byte limit")
		}
		*m.bytes += int64(len(target))
		writeManifestField(m.hash, "symlink")
		writeManifestField(m.hash, filepath.ToSlash(relative))
		writeManifestField(m.hash, target)
	case entry.IsDir():
		writeManifestField(m.hash, "dir")
		writeManifestField(m.hash, filepath.ToSlash(relative))
		return m.hashDirectory(relative)
	default:
		info, err := entry.Info()
		if err != nil {
			return service.ErrInternal("failed to stat directory manifest entry", err)
		}
		if !info.Mode().IsRegular() {
			return service.ErrValidation("directory contains unsupported file type")
		}
		if info.Size() < 0 || *m.bytes > maxDirectoryVersionBytes-info.Size() {
			return service.ErrPayloadTooLarge("directory version manifest exceeds byte limit")
		}
		*m.bytes += info.Size()
		sum, _, err := m.store.Hash(childPath)
		if err != nil {
			return err
		}
		writeManifestField(m.hash, "file")
		writeManifestField(m.hash, filepath.ToSlash(relative))
		writeManifestField(m.hash, hex.EncodeToString(sum))
	}
	return nil
}

func writeManifestField(h hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write([]byte(value))
}
