package workflowauthoring

import "errors"

// ErrBuildToolchainUnavailable distinguishes an environment that cannot build
// a workflow bundle from invalid source or a catalog failure. Distribution
// adapters wrap this sentinel when their local toolchain cannot run.
var ErrBuildToolchainUnavailable = errors.New("workflow build toolchain unavailable")
