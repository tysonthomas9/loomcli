package fleet

import (
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// repoFilterWarnInterval throttles the starvation warning. The daemon polls the
// ready queue continuously, so an unthrottled warning would be a log flood
// rather than a signal.
const repoFilterWarnInterval = 5 * time.Minute

var repoFilterWarns = struct {
	sync.Mutex
	last map[string]time.Time
}{last: make(map[string]time.Time)}

// warnReadyRepoFilterStarvation reports a repo-filtered ready queue that came
// back empty even though the server returned candidates.
//
// This is the signal that was missing when the repo filter silently dropped
// every unscoped issue: the agent saw an empty queue and stopped claiming work,
// and nothing in the log distinguished "nothing to do" from "N candidates, all
// filtered out". The unscoped count is included because unscoped candidates
// surviving to zero results is the exact regression signature.
//
// Only the ready queue is instrumented: it is the dispatch path, where an empty
// result stalls an agent. An empty list or count is a routine query answer.
func warnReadyRepoFilterStarvation(sourceRepos []string, candidates, kept []backend.IssueData) {
	if len(sourceRepos) == 0 || len(candidates) == 0 || len(kept) > 0 {
		return
	}
	if !allowRepoFilterWarn(strings.Join(sourceRepos, ","), time.Now()) {
		return
	}
	slog.Warn("repo-filtered ready queue is empty but the server returned candidates; agent will idle",
		"repos", sourceRepos,
		"candidates", len(candidates),
		"unscoped_candidates", countUnscopedIssues(candidates),
	)
}

func allowRepoFilterWarn(key string, now time.Time) bool {
	repoFilterWarns.Lock()
	defer repoFilterWarns.Unlock()
	if last, ok := repoFilterWarns.last[key]; ok && now.Sub(last) < repoFilterWarnInterval {
		return false
	}
	repoFilterWarns.last[key] = now
	return true
}

func countUnscopedIssues(issues []backend.IssueData) int {
	n := 0
	for _, issue := range issues {
		if issue.SourceRepo == "" {
			n++
		}
	}
	return n
}
