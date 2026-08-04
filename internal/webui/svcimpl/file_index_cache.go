package svcimpl

import (
	"container/list"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const (
	fileIndexCacheMaxEntries = 32
	fileIndexCacheMaxBytes   = int64(64 << 20)
)

type fileIndexCacheEntry struct {
	key            string
	root           string
	allowSensitive bool
	result         service.FileIndexResult
	size           int64
	expiresAt      time.Time
}

type fileIndexCache struct {
	mu         sync.Mutex
	entries    map[string]*list.Element
	lru        list.List
	bytes      int64
	generation uint64
	maxEntries int
	maxBytes   int64
	ttl        time.Duration
	now        func() time.Time
}

func newFileIndexCache(maxEntries int, maxBytes int64, ttl time.Duration) *fileIndexCache {
	return &fileIndexCache{
		entries:    make(map[string]*list.Element),
		maxEntries: maxEntries,
		maxBytes:   maxBytes,
		ttl:        ttl,
		now:        time.Now,
	}
}

func (c *fileIndexCache) get(root string, allowSensitive bool) (*service.FileIndexResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := fileIndexCacheKey(root, allowSensitive)
	element, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	entry := element.Value.(*fileIndexCacheEntry)
	if !c.now().Before(entry.expiresAt) {
		c.remove(element)
		return nil, false
	}
	c.lru.MoveToFront(element)
	return cloneFileIndexResult(&entry.result), true
}

func (c *fileIndexCache) put(root string, allowSensitive bool, result *service.FileIndexResult) {
	c.putAtGeneration(root, allowSensitive, result, nil)
}

func (c *fileIndexCache) putIfGeneration(root string, allowSensitive bool, result *service.FileIndexResult, generation uint64) {
	c.putAtGeneration(root, allowSensitive, result, &generation)
}

func (c *fileIndexCache) putAtGeneration(root string, allowSensitive bool, result *service.FileIndexResult, expectedGeneration *uint64) {
	if result == nil {
		return
	}
	copyResult := cloneFileIndexResult(result)
	entry := &fileIndexCacheEntry{
		key:            fileIndexCacheKey(root, allowSensitive),
		root:           root,
		allowSensitive: allowSensitive,
		result:         *copyResult,
		size:           estimateFileIndexSize(copyResult),
		expiresAt:      c.now().Add(c.ttl),
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if expectedGeneration != nil && c.generation != *expectedGeneration {
		return
	}
	if existing, ok := c.entries[entry.key]; ok {
		c.remove(existing)
	}
	if c.maxEntries <= 0 || c.maxBytes <= 0 || entry.size > c.maxBytes {
		return
	}
	element := c.lru.PushFront(entry)
	c.entries[entry.key] = element
	c.bytes += entry.size
	for c.lru.Len() > c.maxEntries || c.bytes > c.maxBytes {
		c.remove(c.lru.Back())
	}
}

func (c *fileIndexCache) invalidateOverlapping(root string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	for element := c.lru.Front(); element != nil; {
		next := element.Next()
		entry := element.Value.(*fileIndexCacheEntry)
		if canonicalRootsOverlap(root, entry.root) {
			c.remove(element)
		}
		element = next
	}
}

func (c *fileIndexCache) currentGeneration() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

func (c *fileIndexCache) remove(element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(*fileIndexCacheEntry)
	delete(c.entries, entry.key)
	c.bytes -= entry.size
	c.lru.Remove(element)
}

func cloneFileIndexResult(result *service.FileIndexResult) *service.FileIndexResult {
	if result == nil {
		return nil
	}
	return &service.FileIndexResult{
		Paths:          append(make([]string, 0, len(result.Paths)), result.Paths...),
		Truncated:      result.Truncated,
		PartialReasons: append(make([]service.FilePartialReason, 0, len(result.PartialReasons)), result.PartialReasons...),
	}
}

func estimateFileIndexSize(result *service.FileIndexResult) int64 {
	const estimatedStringHeader = 16
	size := int64(32)
	for _, item := range result.Paths {
		size += int64(estimatedStringHeader + len(item))
	}
	for _, reason := range result.PartialReasons {
		size += int64(estimatedStringHeader + len(reason))
	}
	return size
}

func fileIndexCacheKey(root string, allowSensitive bool) string {
	if allowSensitive {
		return root + "\x00sensitive"
	}
	return root + "\x00filtered"
}

func fileIndexBuildKey(root string, allowSensitive bool, generation uint64) string {
	return fileIndexCacheKey(root, allowSensitive) + "\x00generation:" + strconv.FormatUint(generation, 10)
}

func canonicalRootsOverlap(a, b string) bool {
	return canonicalRootContains(a, b) || canonicalRootContains(b, a)
}

func canonicalRootContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
