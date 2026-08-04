package backend

import (
	"testing"
	"time"
)

func TestSortCommentsByCreation_OldestFirstWithIDTieBreaker(t *testing.T) {
	base := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
	comments := []CommentData{
		{ID: 3, Text: "third", CreatedAt: base.Add(2 * time.Second)},
		{ID: 2, Text: "second", CreatedAt: base.Add(time.Second)},
		{ID: 4, Text: "same-time-higher-id", CreatedAt: base},
		{ID: 1, Text: "first", CreatedAt: base},
	}

	SortCommentsByCreation(comments)

	got := []int64{comments[0].ID, comments[1].ID, comments[2].ID, comments[3].ID}
	want := []int64{1, 4, 2, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
