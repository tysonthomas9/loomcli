package execution

import "time"

// persistedCommandTimeMatches accounts for PostgreSQL's microsecond
// timestamptz precision while retaining exact comparisons for backends that
// preserve nanoseconds. Command timestamps are audit data, not authority, but
// a valid durable receipt must still match the originating command.
func persistedCommandTimeMatches(got, want time.Time) bool {
	return !got.IsZero() && !want.IsZero() &&
		got.Truncate(time.Microsecond).Equal(want.Truncate(time.Microsecond))
}
