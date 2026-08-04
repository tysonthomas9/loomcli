# Loom CLI Reference

`loom` is the agent-management CLI. This page is the complete command reference,
derived from the assembled cobra command tree so it cannot drift from the binary.
It lists every command — including the hidden, internal-only ones that
`loom --help` omits — with usage, aliases, descriptions, and flags.

**How to read this**

- The [Command Index](#command-index) is the whole tree; each entry links to that
  command's section.
- [Global Flags](#global-flags) are the root's persistent flags. Every command
  inherits them, so they are documented once and not repeated per command.
- Under each command, the **Flags** table lists only that command's own
  (non-inherited) flags. A persistent flag defined on an intermediate parent
  (for example `loom data`'s `--output`) is documented on the parent that
  defines it and inherited by its subcommands. A command that defines its *own*
  flag under a global's name shadows the global, so that flag stays in the
  command's table — `loom pull`'s `-W, --workspace` is the command's flag, not
  the root's.
- Hidden commands are for internal or test-only use; they are included here for
  completeness but are absent from `loom --help`.
- `loom help` and `loom completion` are cobra's built-ins, installed during
  `Execute` rather than at construction; the generator installs them the same
  way before walking the tree (`scripts/loomdoc/cli.go:88-89`). The one gap is
  cobra's hidden shell-completion transport, `__complete` (aliased
  `__completeNoDesc`) — cobra registers it from an unexported method, so the
  generator cannot reach it.

For the rationale behind these commands and the workflows they compose, see the
design docs under `docs/design/`. This page is the inventory of *what* exists;
those documents cover *why*.
