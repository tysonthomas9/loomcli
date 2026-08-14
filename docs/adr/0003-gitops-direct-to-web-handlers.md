# Web handlers consume GitOps directly; AgentService keeps agent-domain only

AgentService had grown to 21 exported methods, roughly 15 of which were 1:1 forwards over `ops.GitOps` differing only by a validate-agent-name-and-map-error wrapper. We decided to split it: AgentService retains what is genuinely agent-domain (CRUD, lifecycle, terminal-mode invariants), and web handlers consume the `GitOps` interface directly, with the name-validation and error-mapping wrapper concentrated in one shared helper.

## Considered Options

- **Keep the facade as an anti-corruption surface** — defensible only if the web tier never sees `ops` types; it already forwarded them, so the facade added interface width without hiding anything. Rejected.
- **Collapse to generic methods** (`Git(op, args)`) — smaller surface but a stringly contract; depth by obfuscation. Rejected.

## Consequences

- Anyone expecting a conventional service layer between handlers and git operations should read this first: the seam is `GitOps` itself, and the deleted forwards are not to be reintroduced one convenience method at a time.
- The shared helper is the single home for agent-name validation and error mapping on the git path.
