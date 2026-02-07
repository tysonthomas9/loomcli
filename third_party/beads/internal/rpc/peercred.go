package rpc

import "errors"

// errNoPeerCred is returned when peer credential verification is not available
// on the current platform or connection type.
var errNoPeerCred = errors.New("peer credentials not available")
