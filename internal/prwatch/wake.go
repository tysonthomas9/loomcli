package prwatch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// WakeLimit is how many comment-only wakes in a row end a watch: check or
// conflict news resets the count, so it only stops a chatty bot looping an
// agent that replies to it (T3 pullRequestWatch.ts).
const WakeLimit = 10

// Wake is what a sweep tells an agent about its watched PR: the text, a
// request ID built from the cursors it moves between, and its watch's change.
type Wake struct {
	RequestID string
	Text      string
	Change    loomstore.PRWatchWake
}

// Decide compares the open or closed (not merged) PR s with what watch w
// last told, and returns the wake that tells the news, or false when there
// is none: then the wake's Change.Cursor is what to save quietly if it
// differs from w's (a conflict that cleared). News is checks that finished (none still running) since, a new
// conflict, a comment by anyone but the host viewer s.Viewer, or the PR's
// close, which ends the watch. Only what is told moves the cursor: checks
// still running keep theirs until they finish.
func Decide(w loomstore.PRWatch, s Snapshot) (Wake, bool) {
	told, next := w.Cursor, w.Cursor
	var news []string
	checks := s.Cursor.Checks != told.Checks && s.Total > 0 && !s.Pending
	if checks {
		next.Head, next.Checks = s.Cursor.Head, s.Cursor.Checks
		news = append(news, checksNews(s))
	}
	var seen, now map[string]int64 // comment cursors by kind, and "conflict"
	_ = json.Unmarshal([]byte(told.Comments), &seen)
	_ = json.Unmarshal([]byte(s.Cursor.Comments), &now)
	cursor := maps.Clone(now)
	conflict := now["conflict"] == 1 && seen["conflict"] != 1
	if conflict {
		news = append(news, "- The branch now conflicts with its base.")
	}
	if s.Mergeable == "unknown" { // GitHub still computing keeps what was told
		cursor["conflict"] = seen["conflict"]
	}
	fresh := freshComments(seen, s)
	if len(fresh) > 0 {
		noun := "comments"
		if len(fresh) == 1 {
			noun = "comment"
		}
		news = append(news, fmt.Sprintf("- %d new %s: %s.", len(fresh), noun, strings.Join(fresh, ", ")))
	}
	closed := s.State != "open"
	if closed {
		news = append(news, "- It was closed, so Loom stopped watching it. Watch it again if it reopens.")
	}
	if len(news) == 0 { // a conflict that cleared is saved quietly, so the next is news
		if cursor["conflict"] != seen["conflict"] {
			maps.Copy(seen, map[string]int64{"conflict": cursor["conflict"]})
			b, _ := json.Marshal(seen)
			next.Comments = string(b)
		}
		return Wake{Change: loomstore.PRWatchWake{PRWatchKey: w.PRWatchKey, Since: w.CreatedAt, Cursor: next}}, false
	}
	b, _ := json.Marshal(cursor) // map keys marshal sorted
	next.Comments = string(b)
	count := 0
	if !checks && !conflict && !closed {
		count = w.WakeCount + 1
	}
	end := closed || count >= WakeLimit
	text := wakeText(w.PRWatchKey, news, closed, end)
	from, _ := json.Marshal(told)
	to, _ := json.Marshal(next)
	// The watch's last write (made, told or settled) keeps news that recurs,
	// as a conflict back after it cleared or a PR closed again after a
	// rewatch, from reusing an earlier receipt.
	sum := sha256.Sum256(append(append(append([]byte(w.UpdatedAt), 0), append(from, 0)...), to...))
	return Wake{
		RequestID: fmt.Sprintf("pr-watch:%s/%s#%d:%s", w.Owner, w.Repo, w.Number, hex.EncodeToString(sum[:12])),
		Text:      text,
		Change:    loomstore.PRWatchWake{PRWatchKey: w.PRWatchKey, Since: w.CreatedAt, Viewer: s.Viewer, Cursor: next, WakeCount: count, LastTold: text, End: end},
	}, true
}

// freshComments are s's comments after the comment cursors seen, but for
// the host viewer's own.
func freshComments(seen map[string]int64, s Snapshot) []string {
	var fresh []string
	for _, c := range s.Comments {
		if c.ID > seen[c.Kind] && !strings.EqualFold(c.Author, s.Viewer) {
			fresh = append(fresh, fmt.Sprintf("%s (%s %d)", c.Author, c.Kind, c.ID))
		}
	}
	return fresh
}

// checksNews tells how s's finished checks fared.
func checksNews(s Snapshot) string {
	on := " on " + s.Head[:min(7, len(s.Head))]
	if len(s.Failed) > 0 {
		return fmt.Sprintf("- Checks finished%s; %d of %d failed: %s.", on, len(s.Failed), s.Total, strings.Join(s.Failed, ", "))
	}
	return fmt.Sprintf("- All %d checks finished%s; none failed.", s.Total, on)
}

// wakeText is a wake's message on k's PR telling news; end says the watch
// ends, as it does after a close (which news says).
func wakeText(k loomstore.PRWatchKey, news []string, closed, end bool) string {
	tail := "Look into each item and act on it as your task requires. Loom keeps watching and wakes you on the next change, so end your turn when you are done."
	switch {
	case closed:
		tail = ""
	case end:
		tail = fmt.Sprintf("Loom stopped watching after %d comment-only updates in a row. Watch it again to keep hearing about it.", WakeLimit)
	}
	return strings.TrimSpace(fmt.Sprintf("Update on pull request %s/%s#%d, which Loom is watching for you:\n%s\n\n%s",
		k.Owner, k.Repo, k.Number, strings.Join(news, "\n"), tail))
}
