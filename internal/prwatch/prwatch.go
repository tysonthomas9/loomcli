// Package prwatch is the GitHub PR-watch foundation (OR10): an agent's
// watch on one PR of its repo, saved in loomstore, and the host-level reads
// a sweep makes. Every read goes through the host GitHub connector (Host),
// as the host's own GitHub viewer, never through an agent's bridge.
package prwatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// Host is the host GitHub connector: github_read ops and the viewer login on
// a GitHub repo registered in the workspace, and the repo an agent's clone
// is.
type Host interface {
	Repo(ctx context.Context, ws, repoPath string) (owner, repo string, err error)
	Viewer(ctx context.Context, ws, owner, repo string) (string, error)
	Read(ctx context.Context, ws, owner, repo, op string, args map[string]any) (map[string]any, error)
}

// maxPages bounds one list read at 100 items a page.
const maxPages = 50

// Comment is one PR comment or review.
type Comment struct {
	Kind   string // issue, review or reviewComment
	ID     int64
	Author string
}

// Snapshot is a PR as one complete observation saw it. Cursor is what a
// watch reports after telling an agent about it.
type Snapshot struct {
	Viewer    string
	State     string // open or closed
	Merged    bool
	Mergeable string // GitHub's mergeable_state
	Head      string
	Checks    []map[string]any // the head's check runs
	Comments  []Comment
	Cursor    loomstore.PRWatchCursor
}

// Observe reads PR number on owner/repo through host: the viewer, the PR,
// every page of its head's check runs, and every page of its comments,
// review comments and reviews. Any failed read (a rate limit included)
// fails it whole, so no partial snapshot can advance a cursor.
func Observe(ctx context.Context, host Host, ws, owner, repo string, number int) (Snapshot, error) {
	viewer, err := host.Viewer(ctx, ws, owner, repo)
	if err != nil {
		return Snapshot{}, fmt.Errorf("PR watch needs the host GitHub viewer: %w", err)
	}
	body, err := host.Read(ctx, ws, owner, repo, "pr_view", map[string]any{"number": number})
	if err != nil {
		return Snapshot{}, err
	}
	pr, _ := body["item"].(map[string]any)
	head, _ := pr["head"].(map[string]any)
	s := Snapshot{Viewer: viewer}
	s.State, _ = pr["state"].(string)
	s.Merged, _ = pr["merged"].(bool)
	s.Mergeable, _ = pr["mergeable_state"].(string)
	s.Head, _ = head["sha"].(string)
	if s.Head == "" {
		return Snapshot{}, fmt.Errorf("GitHub answered PR %s/%s#%d with no head SHA", owner, repo, number)
	}
	runs, err := readAll(ctx, host, ws, owner, repo, "check_runs", map[string]any{"ref": s.Head})
	if err != nil {
		return Snapshot{}, err
	}
	checks := make([]string, 0, len(runs))
	for _, r := range runs {
		run, _ := r.(map[string]any)
		s.Checks = append(s.Checks, run)
		checks = append(checks, fmt.Sprint(id(run), ":", run["status"], ":", run["conclusion"]))
	}
	sort.Strings(checks)
	sum := sha256.Sum256([]byte(fmt.Sprint(checks)))
	cursor := map[string]int64{"issue": 0, "review": 0, "reviewComment": 0}
	for kind, op := range map[string]string{"issue": "issue_comments", "review": "pr_reviews", "reviewComment": "pr_review_comments"} {
		items, err := readAll(ctx, host, ws, owner, repo, op, map[string]any{"number": number})
		if err != nil {
			return Snapshot{}, err
		}
		for _, it := range items {
			c, _ := it.(map[string]any)
			user, _ := c["user"].(map[string]any)
			login, _ := user["login"].(string)
			s.Comments = append(s.Comments, Comment{Kind: kind, ID: id(c), Author: login})
			cursor[kind] = max(cursor[kind], id(c))
		}
	}
	comments, _ := json.Marshal(cursor) // map keys marshal sorted
	s.Cursor = loomstore.PRWatchCursor{Head: s.Head, Checks: hex.EncodeToString(sum[:16]), Comments: string(comments)}
	return s, nil
}

// readAll reads every page of a github_read list op.
func readAll(ctx context.Context, host Host, ws, owner, repo, op string, args map[string]any) ([]any, error) {
	var all []any
	for page := 1; ; page++ {
		if page > maxPages {
			return nil, fmt.Errorf("PR watch %s has more than %d pages", op, maxPages)
		}
		a := map[string]any{"page": page, "perPage": 100}
		for k, v := range args {
			a[k] = v
		}
		body, err := host.Read(ctx, ws, owner, repo, op, a)
		if err != nil {
			return nil, err
		}
		items, _ := body["items"].([]any)
		all = append(all, items...)
		if next, _ := body["next"].(string); next == "" {
			return all, nil
		}
	}
}

// id is a GitHub object's numeric id (JSON numbers decode as float64).
func id(m map[string]any) int64 {
	switch v := m["id"].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case json.Number:
		n, _ := strconv.ParseInt(string(v), 10, 64)
		return n
	}
	return 0
}

// Register saves agentID's watch on PR number of its repo (the clone at
// repoPath in ws), with the cursors of a complete first observation so the
// watch reports only later news. Watching a watched PR keeps its cursors
// and records the current viewer. A closed PR cannot be watched; created
// reports a new watch.
func Register(ctx context.Context, st *loomstore.Store, host Host, ws, agentID, repoPath string, number int) (loomstore.PRWatch, bool, error) {
	if number <= 0 {
		return loomstore.PRWatch{}, false, fmt.Errorf("PR watch needs a PR number: %w", domain.ErrInvalid)
	}
	owner, repo, err := host.Repo(ctx, ws, repoPath)
	if err != nil {
		return loomstore.PRWatch{}, false, err
	}
	s, err := Observe(ctx, host, ws, owner, repo, number)
	if err != nil {
		return loomstore.PRWatch{}, false, err
	}
	if s.State != "open" || s.Merged {
		return loomstore.PRWatch{}, false, fmt.Errorf("PR %s/%s#%d is %s, so it cannot be watched: %w", owner, repo, number, s.State, domain.ErrInvalid)
	}
	return st.RegisterPRWatch(ctx, loomstore.PRWatch{
		PRWatchKey:  loomstore.PRWatchKey{AgentID: agentID, Owner: owner, Repo: repo, Number: number},
		WorkspaceID: ws, Viewer: s.Viewer, Cursor: s.Cursor,
	})
}

// Unregister removes agentID's watch on PR number of its repo, reporting
// whether there was one.
func Unregister(ctx context.Context, st *loomstore.Store, host Host, ws, agentID, repoPath string, number int) (bool, error) {
	owner, repo, err := host.Repo(ctx, ws, repoPath)
	if err != nil {
		return false, err
	}
	return st.UnregisterPRWatch(ctx, loomstore.PRWatchKey{AgentID: agentID, Owner: owner, Repo: repo, Number: number})
}

// Service is the agents' github/watch and github/unwatch on a store and the
// host GitHub connector.
type Service struct {
	Store *loomstore.Store
	Host  Host
}

// Watch is Register.
func (s Service) Watch(ctx context.Context, ws, agentID, repoPath string, number int) (loomstore.PRWatch, bool, error) {
	return Register(ctx, s.Store, s.Host, ws, agentID, repoPath, number)
}

// Unwatch is Unregister.
func (s Service) Unwatch(ctx context.Context, ws, agentID, repoPath string, number int) (bool, error) {
	return Unregister(ctx, s.Store, s.Host, ws, agentID, repoPath, number)
}
