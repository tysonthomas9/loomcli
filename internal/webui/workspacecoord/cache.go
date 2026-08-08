package workspacecoord

import (
	"context"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

const (
	defaultWorkspaceDataCacheTTL = 10 * time.Second
	maxWorkspaceDataCacheEntries = 128
)

type workspaceDataLoadFn func(context.Context, string) (*ops.WorkspaceData, error)

type workspaceDataCache struct {
	ttl time.Duration

	mu      sync.Mutex
	entries map[string]*workspaceDataCacheEntry
}

type workspaceDataCacheEntry struct {
	mu       sync.Mutex
	cachedAt time.Time
	data     *ops.WorkspaceData
}

func newWorkspaceDataCache(ttl time.Duration) *workspaceDataCache {
	if ttl <= 0 {
		ttl = defaultWorkspaceDataCacheTTL
	}
	return &workspaceDataCache{
		ttl:     ttl,
		entries: make(map[string]*workspaceDataCacheEntry),
	}
}

func (c *workspaceDataCache) get(ctx context.Context, key string, load workspaceDataLoadFn) (*ops.WorkspaceData, error) {
	if load == nil {
		return nil, nil
	}
	if c == nil {
		return load(ctx, key)
	}

	entry := c.cacheEntry(key)
	entry.mu.Lock()
	defer entry.mu.Unlock()

	now := time.Now()
	if entry.data != nil && !entry.cachedAt.IsZero() && now.Sub(entry.cachedAt) < c.ttl {
		return cloneWorkspaceData(entry.data), nil
	}

	data, err := load(ctx, key)
	if err != nil {
		return nil, err
	}
	entry.data = cloneWorkspaceData(data)
	entry.cachedAt = time.Now()
	return cloneWorkspaceData(data), nil
}

func (c *workspaceDataCache) invalidateAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.entries = make(map[string]*workspaceDataCacheEntry)
	c.mu.Unlock()
}

func (c *workspaceDataCache) cacheEntry(key string) *workspaceDataCacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry := c.entries[key]
	if entry != nil {
		return entry
	}
	if len(c.entries) >= maxWorkspaceDataCacheEntries {
		return &workspaceDataCacheEntry{}
	}
	entry = &workspaceDataCacheEntry{}
	c.entries[key] = entry
	return entry
}

func cloneWorkspaceData(in *ops.WorkspaceData) *ops.WorkspaceData {
	if in == nil {
		return nil
	}
	out := *in
	out.Repos = append([]ops.WorkspaceRepo(nil), in.Repos...)
	for i := range out.Repos {
		out.Repos[i].Groups = append([]string(nil), in.Repos[i].Groups...)
	}
	out.Groups = append([]string(nil), in.Groups...)
	out.Agents = append([]ops.WorkspaceAgentInfo(nil), in.Agents...)
	for i := range out.Agents {
		out.Agents[i].Repos = append([]string(nil), in.Agents[i].Repos...)
		out.Agents[i].RepoGroups = append([]string(nil), in.Agents[i].RepoGroups...)
	}
	out.Workspaces = append([]ops.WorkspaceSummary(nil), in.Workspaces...)
	out.WorkspaceOrder = append([]string(nil), in.WorkspaceOrder...)
	return &out
}
