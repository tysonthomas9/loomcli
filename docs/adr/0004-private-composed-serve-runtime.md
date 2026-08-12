# Expose one private composed serve runtime

Status: accepted

`internal/app/serve` is the sole capability and runtime composition root and
returns one small runtime surface for HTTP handling, explicit start, graceful
close, and health. Individual capability factories and the parallel
`internal/cli/serve/serveadapter` composition plane are deleted. This favors
locality and interface-level testing over reuse of construction details.

Construction accepts validated typed configuration and starts no goroutines or
listeners. `Start` rolls partial startup back in reverse order and `Close` is
idempotent. CLI and Desktop continue to own environment parsing, listener and
signal mechanics, OS integration, and process policy. Tests compose the same
runtime as production with substituted adapters rather than importing private
capability constructors.
