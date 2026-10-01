// Package protocol holds the codex app-server protocol types the codex
// adapter uses, generated from codex's own JSON schema.
//
// schema/ is the pinned output of `codex app-server generate-json-schema
// --experimental` at the tested version (loomharness.Versions["codex"]),
// trimmed to the files of the types the adapter uses. To use another type,
// copy its file from a fresh schema bundle into schema/ (same relative path)
// and run go generate. TestSchemaMatchesInstalled (LOOM_REAL_CODEX=1) fails
// when the installed codex changes any pinned file.
package protocol

//go:generate go run gen.go
