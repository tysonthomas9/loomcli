package monitor

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// worktreeChangeDetector caches per-worktree git state, keyed by filesystem mtimes/SHAs.
// When the filesystem signals haven't changed, it returns cached results without
// spawning any git subprocesses.
type worktreeChangeDetector struct {
	mu      sync.Mutex
	entries map[string]*worktreeCacheEntry // keyed by worktree gitdir path
}

type worktreeCacheEntry struct {
	// Filesystem signals
	headMtime  time.Time // mtime of <gitdir>/HEAD
	indexMtime time.Time // mtime of <gitdir>/index
	localSHA   string    // SHA of refs/heads/<branch>
	remoteSHA  string    // SHA of refs/remotes/origin/<remoteBranch>

	// Cached status results
	clean   bool
	count   int
	changes []FileChange

	// Cached ahead/behind results
	ahead  int
	behind int
}

// commitCache caches commit details keyed by (headSHA, remoteSHA, defaultBranch, githubURL).
// HEAD SHA alone isn't sufficient: getWorktreeCommitDetailsDeps queries
// `origin/<defaultBranch>..HEAD`, so the result depends on the remote's tip
// and the integration branch selection. The github URL is also baked into
// each CommitDetail's links and must participate in the key.
type commitCache struct {
	mu      sync.Mutex
	entries map[string][]CommitDetail
}

var (
	globalChangeDetector = &worktreeChangeDetector{entries: make(map[string]*worktreeCacheEntry)}
	globalCommitCache    = &commitCache{entries: make(map[string][]CommitDetail)}
)

// readMtime returns the modification time of a file, or zero time on error.
func readMtime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// CheckStatus returns cached dirty/clean status if index+HEAD mtimes haven't changed.
// Returns (hit, clean, count, changes, headMtime, indexMtime). The mtimes are
// returned so the caller can pass them to UpdateStatus on a miss, ensuring the
// stored entry reflects the snapshot the subprocess actually observed (not a
// later state introduced between the read and the write — TOCTOU).
func (d *worktreeChangeDetector) CheckStatus(gitDir string) (hit bool, clean bool, count int, changes []FileChange, headMtime, indexMtime time.Time) {
	headMtime = readMtime(filepath.Join(gitDir, "HEAD"))
	indexMtime = readMtime(filepath.Join(gitDir, "index"))

	d.mu.Lock()
	defer d.mu.Unlock()

	entry, ok := d.entries[gitDir]
	if !ok {
		return false, false, 0, nil, headMtime, indexMtime
	}

	// Both mtimes must match and be non-zero for a cache hit
	if headMtime.IsZero() || indexMtime.IsZero() {
		return false, false, 0, nil, headMtime, indexMtime
	}
	if !entry.headMtime.Equal(headMtime) || !entry.indexMtime.Equal(indexMtime) {
		return false, false, 0, nil, headMtime, indexMtime
	}

	return true, entry.clean, entry.count, entry.changes, headMtime, indexMtime
}

// UpdateStatus stores status results under the mtimes the caller already
// observed. Storing under pre-subprocess mtimes (rather than re-reading after)
// prevents persisting stale results under a future mtime if the index moved
// during the subprocess call. Slices are defensively copied so cache aliases
// can't be mutated by callers.
func (d *worktreeChangeDetector) UpdateStatus(gitDir string, headMtime, indexMtime time.Time, clean bool, count int, changes []FileChange) {
	d.mu.Lock()
	defer d.mu.Unlock()

	entry := d.entries[gitDir]
	if entry == nil {
		entry = &worktreeCacheEntry{}
		d.entries[gitDir] = entry
	}
	entry.headMtime = headMtime
	entry.indexMtime = indexMtime
	entry.clean = clean
	entry.count = count
	if changes != nil {
		copied := make([]FileChange, len(changes))
		copy(copied, changes)
		entry.changes = copied
	} else {
		entry.changes = nil
	}
}

// CheckAheadBehind returns cached ahead/behind if ref SHAs haven't changed.
// Fast path: if local == remote SHA, returns (true, 0, 0) without checking cache.
// The observed (localSHA, remoteSHA) are returned so the caller can pass them
// back into UpdateAheadBehind, avoiding a TOCTOU where a ref advances between
// CheckAheadBehind and UpdateAheadBehind.
//
// An empty remoteSHA (refs/remotes/origin/<branch> doesn't exist — branch never
// pushed, or fetch hasn't run) is still cached: the stored empty matches a
// future empty, and a real remote ref appearing will be a miss.
func (d *worktreeChangeDetector) CheckAheadBehind(commonDir, localBranch, remoteBranch string) (hit bool, ahead, behind int, localSHA, remoteSHA string) {
	localSHA, err := ReadRefSHA(commonDir, "refs/heads/"+localBranch)
	if err != nil {
		return false, 0, 0, "", ""
	}
	remoteSHA, _ = ReadRefSHA(commonDir, "refs/remotes/origin/"+remoteBranch)

	// Fast path: identical SHAs means perfectly in sync
	if localSHA != "" && remoteSHA != "" && localSHA == remoteSHA {
		return true, 0, 0, localSHA, remoteSHA
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	entry, ok := d.entries[commonDir+"::"+localBranch+"::"+remoteBranch]
	if !ok {
		return false, 0, 0, localSHA, remoteSHA
	}

	if localSHA != "" && entry.localSHA == localSHA && entry.remoteSHA == remoteSHA {
		return true, entry.ahead, entry.behind, localSHA, remoteSHA
	}

	return false, 0, 0, localSHA, remoteSHA
}

// UpdateAheadBehind stores ahead/behind results under the SHAs the caller
// already observed (returned by CheckAheadBehind). Storing under pre-subprocess
// SHAs avoids a TOCTOU where the local ref advances during the git rev-list
// call and the stale result then gets keyed under the new SHA.
func (d *worktreeChangeDetector) UpdateAheadBehind(commonDir, localBranch, remoteBranch, localSHA, remoteSHA string, ahead, behind int) {
	key := commonDir + "::" + localBranch + "::" + remoteBranch

	d.mu.Lock()
	defer d.mu.Unlock()

	entry := d.entries[key]
	if entry == nil {
		entry = &worktreeCacheEntry{}
		d.entries[key] = entry
	}
	entry.localSHA = localSHA
	entry.remoteSHA = remoteSHA
	entry.ahead = ahead
	entry.behind = behind
}

// commitCacheKey returns the composite cache key for getWorktreeCommitDetails
// results. The query depends on HEAD SHA, the integration branch's remote tip
// SHA, the integration branch name itself, and the github URL embedded in the
// returned details.
func commitCacheKey(headSHA, remoteSHA, defaultBranch, githubURL string) string {
	return headSHA + "|" + remoteSHA + "|" + defaultBranch + "|" + githubURL
}

// Get returns cached commit details for the given key.
func (c *commitCache) Get(key string) ([]CommitDetail, bool) {
	if key == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	commits, ok := c.entries[key]
	return commits, ok
}

// Set stores commit details under the given key. Slices are defensively copied
// so cache aliases can't be mutated by callers.
func (c *commitCache) Set(key string, commits []CommitDetail) {
	if key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if commits != nil {
		copied := make([]CommitDetail, len(commits))
		copy(copied, commits)
		c.entries[key] = copied
	} else {
		c.entries[key] = nil
	}
}
