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

// commitCache caches commit details by HEAD SHA.
type commitCache struct {
	mu      sync.Mutex
	entries map[string][]CommitDetail // keyed by HEAD SHA
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
// Returns (hit, clean, count, changes).
func (d *worktreeChangeDetector) CheckStatus(gitDir string) (hit bool, clean bool, count int, changes []FileChange) {
	headMtime := readMtime(filepath.Join(gitDir, "HEAD"))
	indexMtime := readMtime(filepath.Join(gitDir, "index"))

	d.mu.Lock()
	defer d.mu.Unlock()

	entry, ok := d.entries[gitDir]
	if !ok {
		return false, false, 0, nil
	}

	// Both mtimes must match and be non-zero for a cache hit
	if headMtime.IsZero() || indexMtime.IsZero() {
		return false, false, 0, nil
	}
	if !entry.headMtime.Equal(headMtime) || !entry.indexMtime.Equal(indexMtime) {
		return false, false, 0, nil
	}

	return true, entry.clean, entry.count, entry.changes
}

// UpdateStatus stores status results keyed by current mtimes.
func (d *worktreeChangeDetector) UpdateStatus(gitDir string, clean bool, count int, changes []FileChange) {
	headMtime := readMtime(filepath.Join(gitDir, "HEAD"))
	indexMtime := readMtime(filepath.Join(gitDir, "index"))

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
	entry.changes = changes
}

// CheckAheadBehind returns cached ahead/behind if ref SHAs haven't changed.
// Fast path: if local == remote SHA, returns (true, 0, 0) without checking cache.
func (d *worktreeChangeDetector) CheckAheadBehind(commonDir, localBranch, remoteBranch string) (hit bool, ahead, behind int) {
	localSHA, err := ReadRefSHA(commonDir, "refs/heads/"+localBranch)
	if err != nil {
		return false, 0, 0
	}
	remoteSHA, _ := ReadRefSHA(commonDir, "refs/remotes/origin/"+remoteBranch)

	// Fast path: identical SHAs means perfectly in sync
	if localSHA != "" && remoteSHA != "" && localSHA == remoteSHA {
		return true, 0, 0
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	entry, ok := d.entries[commonDir+"::"+localBranch+"::"+remoteBranch]
	if !ok {
		return false, 0, 0
	}

	if entry.localSHA == localSHA && entry.remoteSHA == remoteSHA &&
		localSHA != "" && remoteSHA != "" {
		return true, entry.ahead, entry.behind
	}

	return false, 0, 0
}

// UpdateAheadBehind stores ahead/behind results keyed by current ref SHAs.
func (d *worktreeChangeDetector) UpdateAheadBehind(commonDir, localBranch, remoteBranch string, ahead, behind int) {
	localSHA, _ := ReadRefSHA(commonDir, "refs/heads/"+localBranch)
	remoteSHA, _ := ReadRefSHA(commonDir, "refs/remotes/origin/"+remoteBranch)

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

// Get returns cached commit details for the given HEAD SHA.
func (c *commitCache) Get(headSHA string) ([]CommitDetail, bool) {
	if headSHA == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	commits, ok := c.entries[headSHA]
	return commits, ok
}

// Set stores commit details keyed by HEAD SHA.
func (c *commitCache) Set(headSHA string, commits []CommitDetail) {
	if headSHA == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[headSHA] = commits
}
