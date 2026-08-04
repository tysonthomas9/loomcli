package backend

import "sort"

// SortCommentsByCreation normalizes the IssueBackend comment contract:
// comments are oldest-first by creation time, with ID as a stable tie-breaker.
func SortCommentsByCreation(comments []CommentData) {
	sort.SliceStable(comments, func(i, j int) bool {
		if comments[i].CreatedAt.Equal(comments[j].CreatedAt) {
			return comments[i].ID < comments[j].ID
		}
		return comments[i].CreatedAt.Before(comments[j].CreatedAt)
	})
}
