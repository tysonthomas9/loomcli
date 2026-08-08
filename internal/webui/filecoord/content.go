package filecoord

import (
	"bytes"
	"unicode/utf8"
)

// IsBinaryContent reports whether data is not valid UTF-8 or contains a NUL byte.
func IsBinaryContent(data []byte) bool {
	return !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0
}
