package sessioncoord

import "regexp"

var (
	validTaskID    = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
	validSessionID = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)
