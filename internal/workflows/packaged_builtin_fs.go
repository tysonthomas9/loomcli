//go:build !loom_packaged_builtins

package workflows

import "io/fs"

// packagedBuiltinFS stays empty for ordinary CLI/source builds. The desktop
// packaging build tag replaces it with the generated bundle embedded below.
var packagedBuiltinFS fs.FS = absentPackagedBuiltinFS{}
