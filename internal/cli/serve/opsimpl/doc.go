// Package opsimpl provides the concrete implementations of the internal/ops
// interfaces used by the server process.
//
// GitOpsImpl and BackendOpsImpl bind those interfaces to loom's real git and
// backend packages. The ops interfaces are declared in a leaf package that
// neither webui nor cli owns; this package is where the server supplies the
// implementations, keeping handlers dependent on the interface rather than on
// the packages behind it.
package opsimpl
