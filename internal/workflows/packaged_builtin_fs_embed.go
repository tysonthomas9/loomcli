//go:build loom_packaged_builtins

package workflows

import (
	"embed"
	"io/fs"
)

// The desktop preparation step creates this ignored tree immediately before
// the tagged Go build. A missing tree is a compile-time packaging failure.
//
//go:embed builtin-dist/*/dist/*
var embeddedPackagedBuiltinFS embed.FS

var packagedBuiltinFS fs.FS = embeddedPackagedBuiltinFS
