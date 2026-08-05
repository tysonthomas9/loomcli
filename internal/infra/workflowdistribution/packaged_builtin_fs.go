//go:build !loom_packaged_builtins

package workflowdistribution

import "io/fs"

type absentPackagedBuiltinFS struct{}

func (absentPackagedBuiltinFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// packagedBuiltinFS stays empty for ordinary CLI/source builds. The desktop
// packaging build tag replaces it with the generated bundle embedded below.
var packagedBuiltinFS fs.FS = absentPackagedBuiltinFS{}

func PackagedBuiltinFS() fs.FS { return packagedBuiltinFS }
