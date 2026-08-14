package localredis

import (
	"strconv"

	"github.com/redis/go-redis/v9"
)

// xAddArgsN builds a stream entry with the integer ID `n-0`. Used by
// the cap test where deterministic monotonic IDs make the assertion
// easy.
func xAddArgsN(stream string, n int, values map[string]any) *redis.XAddArgs {
	return &redis.XAddArgs{Stream: stream, ID: strconv.Itoa(n) + "-0", Values: values}
}

// strconvI is a one-line helper for the cap test's expected-ID
// formatting; mirrors strconv.Itoa to avoid hand-rolled conversions
// in the assertion.
func strconvI(n int) string { return strconv.Itoa(n) }
