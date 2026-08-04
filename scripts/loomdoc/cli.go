package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/data"

	// Blank imports fire each sub-package's init(), which calls
	// cli.RegisterCommand so the pending list AssembleRootCmd flushes is
	// complete. This set MUST mirror cmd/loom/main.go — if that file gains or
	// drops a command package, change this list to match, or the generated CLI
	// reference will silently disagree with the real binary's command tree.
	_ "github.com/tysonthomas9/loomcli/internal/cli/agent"
	_ "github.com/tysonthomas9/loomcli/internal/cli/agent/lead"
	_ "github.com/tysonthomas9/loomcli/internal/cli/agentdef"
	_ "github.com/tysonthomas9/loomcli/internal/cli/automode"
	_ "github.com/tysonthomas9/loomcli/internal/cli/backends"
	_ "github.com/tysonthomas9/loomcli/internal/cli/cleanup"
	_ "github.com/tysonthomas9/loomcli/internal/cli/connector"
	_ "github.com/tysonthomas9/loomcli/internal/cli/daemon"
	_ "github.com/tysonthomas9/loomcli/internal/cli/doctor"
	_ "github.com/tysonthomas9/loomcli/internal/cli/driver"
	_ "github.com/tysonthomas9/loomcli/internal/cli/epic"
	_ "github.com/tysonthomas9/loomcli/internal/cli/git"
	_ "github.com/tysonthomas9/loomcli/internal/cli/hooks"
	_ "github.com/tysonthomas9/loomcli/internal/cli/local"
	_ "github.com/tysonthomas9/loomcli/internal/cli/monitor"
	_ "github.com/tysonthomas9/loomcli/internal/cli/repo"
	_ "github.com/tysonthomas9/loomcli/internal/cli/role"
	_ "github.com/tysonthomas9/loomcli/internal/cli/serve"
	_ "github.com/tysonthomas9/loomcli/internal/cli/serve/install"
	_ "github.com/tysonthomas9/loomcli/internal/cli/serve/logroutercmd"
	_ "github.com/tysonthomas9/loomcli/internal/cli/serve/worker"
	_ "github.com/tysonthomas9/loomcli/internal/cli/stack"
	_ "github.com/tysonthomas9/loomcli/internal/cli/trigger"
	_ "github.com/tysonthomas9/loomcli/internal/cli/workflow"
	_ "github.com/tysonthomas9/loomcli/internal/cli/workspace"
)

// init mirrors cmd/loom/main.go's registration of the sdk-only data sub-tree.
// The data package must not import internal/cli, so it cannot self-register via
// init() like every other sub-package; instead cmd/loom (and this generator)
// pull its commands in explicitly. The provider closure is lazy — DefaultIssueBackend
// runs only if a data command executes, which never happens here (we only walk
// the tree), so no backend or network work occurs at generation time.
func init() {
	data.SetLocalIssueBackendProvider(func(_ context.Context) backend.IssueBackend {
		return cli.DefaultIssueBackend()
	})
	for _, c := range data.Commands() {
		cli.RegisterCommand(c)
	}
}

// generateCLI renders the loom CLI command reference body: a navigable index of
// the whole command tree, the global (root persistent) flags, then one section
// per command with its usage, descriptions, aliases, hidden status, and
// command-specific flags.
//
// The tree comes from the compiled-in cobra commands (cli.AssembleRootCmd), not
// from cfg.Packages(): the CLI generator introspects the assembled command
// objects directly, so cfg is unused here. Determinism comes from copying and
// sorting every child list by Name before rendering (cobra reports children in
// registration order) and from sorting every flag list by name; the assembler is
// idempotent, so rendering twice in one process yields byte-identical output.
func generateCLI(cfg *genConfig) (string, error) {
	_ = cfg // the command tree is compiled in, not derived from the package load.

	root := cli.AssembleRootCmd()

	// Cobra registers `help` and `completion` (with its per-shell subcommands)
	// inside ExecuteC, not at construction time, so a tree taken straight from
	// AssembleRootCmd is missing commands the real binary has. Add them the same
	// way ExecuteC does. Both helpers are idempotent — InitDefaultHelpCmd reuses
	// its cached command and re-adds it, InitDefaultCompletionCmd returns early
	// once a `completion` command exists — so repeated renders in one process
	// stay byte-identical. The `__complete` command ExecuteC also installs comes
	// from an unexported cobra method and so cannot be added here; it is cobra's
	// shell-completion transport, not a loom command.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()

	cmds := flattenCommands(root)

	// Stabilize cobra's lazy, stateful flag merge before rendering. UseLine()
	// appends "[flags]" only when a command HasAvailableFlags, which turns true
	// only after mergePersistentFlags folds each command's inherited (global)
	// flags into its own flag set. That merge mutates cobra state, so the first
	// render would otherwise change what later in-process renders observe — the
	// second render's UseLine would gain "[flags]". Forcing the merge for every
	// command up front makes repeated renders in one process byte-identical, the
	// determinism the staleness gate's unit test asserts. InheritedFlags()
	// triggers the merge and is idempotent.
	for _, c := range cmds {
		_ = c.InheritedFlags()
	}

	globals := globalFlags(root)

	var b strings.Builder
	renderCommandIndex(&b, cmds)
	renderGlobalFlags(&b, root)
	renderCommandReference(&b, cmds, globals)
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

// flattenCommands returns the command tree in a deterministic pre-order: each
// node precedes its children, and siblings are visited sorted by Name. Cobra
// reports children in registration order, so the sort is what makes the output
// stable. Hidden commands are included — this is a complete inventory, not the
// filtered view `loom --help` shows.
func flattenCommands(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	var visit func(c *cobra.Command)
	visit = func(c *cobra.Command) {
		out = append(out, c)
		for _, ch := range sortedChildren(c) {
			visit(ch)
		}
	}
	visit(root)
	return out
}

// sortedChildren returns a command's immediate subcommands as a fresh slice
// sorted by Name, so ranging it never depends on cobra's registration order or
// mutates cobra's cached ordering.
func sortedChildren(c *cobra.Command) []*cobra.Command {
	children := append([]*cobra.Command{}, c.Commands()...)
	sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
	return children
}

// commandDepth is the nesting depth of a command: 0 for root ("loom"), 1 for a
// top-level subcommand ("loom task"), and so on. Derived from the command path
// so it needs no traversal state.
func commandDepth(c *cobra.Command) int {
	return len(strings.Fields(c.CommandPath())) - 1
}

// anchorFor is the GitHub heading slug for a command's reference section. Command
// paths are lowercase words joined by spaces, so the slug is just the path with
// spaces turned into hyphens — matching what GitHub generates for the
// "### <path>" heading this links to.
func anchorFor(c *cobra.Command) string {
	return strings.ReplaceAll(c.CommandPath(), " ", "-")
}

func renderCommandIndex(b *strings.Builder, cmds []*cobra.Command) {
	hidden := 0
	for _, c := range cmds {
		if c.Hidden {
			hidden++
		}
	}
	fmt.Fprintf(b, "## Command Index\n\n")
	fmt.Fprintf(b, "%d commands (%d hidden). Global flags apply to every command; see [Global Flags](#global-flags).\n\n", len(cmds), hidden)
	for _, c := range cmds {
		indent := strings.Repeat("  ", commandDepth(c))
		line := fmt.Sprintf("%s- [`%s`](#%s)", indent, c.CommandPath(), anchorFor(c))
		if short := cliOneLine(c.Short); short != "" {
			line += " — " + short
		}
		if c.Hidden {
			line += " _(hidden)_"
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

func renderGlobalFlags(b *strings.Builder, root *cobra.Command) {
	flags := sortedFlags(root.PersistentFlags())
	fmt.Fprintf(b, "## Global Flags\n\n")
	if len(flags) == 0 {
		b.WriteString("None.\n\n")
		return
	}
	b.WriteString("Persistent flags defined on the root command; every subcommand inherits them.\n\n")
	renderFlagTable(b, flags)
}

func renderCommandReference(b *strings.Builder, cmds []*cobra.Command, globals map[string]*pflag.Flag) {
	fmt.Fprintf(b, "## Command Reference\n\n")
	for _, c := range cmds {
		renderCommand(b, c, globals)
	}
}

func renderCommand(b *strings.Builder, c *cobra.Command, globals map[string]*pflag.Flag) {
	fmt.Fprintf(b, "### %s\n\n", c.CommandPath())

	if c.Hidden {
		b.WriteString("**Hidden command** — not shown in `loom --help`.\n\n")
	}
	if short := cliOneLine(c.Short); short != "" {
		fmt.Fprintf(b, "%s\n\n", short)
	}
	fmt.Fprintf(b, "**Usage:** `%s`\n\n", c.UseLine())
	if len(c.Aliases) > 0 {
		aliases := append([]string{}, c.Aliases...)
		sort.Strings(aliases)
		quoted := make([]string, len(aliases))
		for i, a := range aliases {
			quoted[i] = "`" + a + "`"
		}
		fmt.Fprintf(b, "**Aliases:** %s\n\n", strings.Join(quoted, ", "))
	}
	if long := strings.TrimRight(c.Long, "\n"); strings.TrimSpace(long) != "" && long != strings.TrimRight(c.Short, "\n") {
		fence := cliCodeFence(long)
		fmt.Fprintf(b, "%s\n%s\n%s\n\n", fence, long, fence)
	}

	renderCommandFlags(b, c, globals)
}

// renderCommandFlags renders a command's own flags. For the root that is only
// its local, non-persistent flags (the persistent ones are documented once under
// Global Flags); for every other command it is the non-inherited set, which
// cobra defines as the command's local plus its own persistent flags, excluding
// anything inherited from an ancestor. The globals check is a belt-and-braces
// guard against re-listing an inherited global, and compares flag IDENTITY, not
// name: several commands define their own flag that shadows a global name (for
// example `loom pull`'s `-W, --workspace`), and those are the command's own
// flags, so they must stay in the table.
func renderCommandFlags(b *strings.Builder, c *cobra.Command, globals map[string]*pflag.Flag) {
	var fs *pflag.FlagSet
	if c.HasParent() {
		fs = c.NonInheritedFlags()
	} else {
		fs = c.LocalNonPersistentFlags()
	}
	var flags []*pflag.Flag
	for _, f := range sortedFlags(fs) {
		if globals[f.Name] == f {
			continue
		}
		flags = append(flags, f)
	}
	if len(flags) == 0 {
		return
	}
	b.WriteString("**Flags:**\n\n")
	renderFlagTable(b, flags)
}

func renderFlagTable(b *strings.Builder, flags []*pflag.Flag) {
	b.WriteString("| Flag | Shorthand | Type | Default | Description |\n")
	b.WriteString("|------|-----------|------|---------|-------------|\n")
	for _, f := range flags {
		name := "`--" + f.Name + "`"
		if f.Hidden {
			name += " _(hidden)_"
		}
		shorthand := "—"
		if f.Shorthand != "" {
			shorthand = "`-" + f.Shorthand + "`"
		}
		def := "—"
		if f.DefValue != "" {
			def = "`" + f.DefValue + "`"
		}
		usage := cliCell(f.Usage)
		if f.Deprecated != "" {
			usage += " (deprecated: " + cliOneLine(f.Deprecated) + ")"
		}
		fmt.Fprintf(b, "| %s | %s | `%s` | %s | %s |\n",
			name, shorthand, f.Value.Type(), def, usage)
	}
	b.WriteByte('\n')
}

// sortedFlags collects a flag set into a slice sorted by name, so the render
// never depends on pflag's internal ordering.
func sortedFlags(fs *pflag.FlagSet) []*pflag.Flag {
	if fs == nil {
		return nil
	}
	var flags []*pflag.Flag
	fs.VisitAll(func(f *pflag.Flag) { flags = append(flags, f) })
	sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })
	return flags
}

// globalFlags indexes the root's persistent flags by name, so per-command flag
// tables can drop them (they are documented under Global Flags). Callers must
// match on the *pflag.Flag value, not just the name — a command that defines its
// own flag under a global's name owns a different flag object and keeps its row.
func globalFlags(root *cobra.Command) map[string]*pflag.Flag {
	flags := make(map[string]*pflag.Flag)
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) { flags[f.Name] = f })
	return flags
}

// cliCodeFence returns a backtick fence long enough to wrap content verbatim
// without a run of backticks inside it closing the block early. Keeps rendering
// of multi-line Long help text deterministic and injection-proof.
func cliCodeFence(content string) string {
	longest, run := 0, 0
	for _, r := range content {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
			continue
		}
		run = 0
	}
	n := 3
	if longest >= n {
		n = longest + 1
	}
	return strings.Repeat("`", n)
}

// cliOneLine collapses whitespace runs (including newlines) into single spaces and
// trims the ends, so a value is safe to drop into prose or a heading.
func cliOneLine(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

// cliCell makes a string safe inside a markdown table cell: single line, pipes
// escaped, empty rendered as an em dash.
func cliCell(s string) string {
	s = cliOneLine(s)
	if s == "" {
		return "—"
	}
	return strings.ReplaceAll(s, "|", "\\|")
}
