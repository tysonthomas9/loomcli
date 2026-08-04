// loomdoc generates deterministic reference documentation from Loom source, so
// the docs cannot rot: each doc is derived from code and regenerated on demand,
// and a staleness gate (added in a later phase) fails CI if the committed file
// drifts from a fresh regen. It mirrors the sibling scripts/openapi-to-md, which
// generates docs/api.md from api/openapi.yaml under the same discipline.
//
// Three generators, one per subcommand, each writing into docs/reference/:
//
//	envvars → env-vars.md      every LOOM_* variable read via os.Getenv/
//	                           os.LookupEnv/os.Setenv, resolved through type
//	                           info so reads via named constants are counted.
//	cli     → cli.md           the loom command reference, walked from the
//	                           assembled cobra tree (internal/cli).
//	layers  → architecture.md  the package-layer architecture, from the
//	                           depguard rules in .golangci.yml plus the real
//	                           import graph.
//
// With no subcommand it generates all three. Parsers own inventory (what
// exists); humans own rationale (why): each doc may carry a checked-in
// docs/reference/<name>.preamble.md that is prepended verbatim.
//
// Determinism is non-negotiable. Every collection is sorted before rendering,
// no map is ranged without sorting keys, and the output carries no timestamps,
// absolute paths, or hostnames — running twice produces byte-identical files.
//
// Usage:
//
//	go run ./scripts/loomdoc            # regenerate all reference docs
//	go run ./scripts/loomdoc envvars   # regenerate docs/reference/env-vars.md
//	go run ./scripts/loomdoc -stdout cli > /tmp/cli.md   # for staleness diffing
//
// Flags must precede the subcommand: the standard library flag package stops
// parsing at the first positional argument.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// generator describes one subcommand: the doc it writes, the one-line source
// note that goes in the banner, and the entrypoint a Phase 2 agent implements.
// Keeping outName and sourceNote here (not inside each generator) lets every
// generateXxx keep the exact signature func(*genConfig) (string, error).
type generator struct {
	// outName is the doc basename: docs/reference/<outName>.md, with an
	// optional docs/reference/<outName>.preamble.md.
	outName string
	// sourceNote is a one-line description of what the body is derived from,
	// rendered into the generated-file banner.
	sourceNote string
	// run produces the markdown body (no banner, no preamble).
	run func(*genConfig) (string, error)
}

// generators is the subcommand registry. Never range over this map directly for
// output ordering — use genOrder, which is deterministic.
var generators = map[string]generator{
	"envvars": {
		outName:    "env-vars",
		sourceNote: "LOOM_* environment variables, from os.Getenv/os.LookupEnv/os.Setenv call sites in git-tracked Go source.",
		run:        generateEnvVars,
	},
	"cli": {
		outName:    "cli",
		sourceNote: "loom CLI command reference, from the assembled cobra command tree in internal/cli.",
		run:        generateCLI,
	},
	"layers": {
		outName:    "architecture",
		sourceNote: "package-layer architecture, from the depguard rules in .golangci.yml and the module import graph.",
		run:        generateLayers,
	},
}

// genOrder fixes the deterministic order used when generating all docs.
var genOrder = []string{"envvars", "cli", "layers"}

func main() {
	var (
		repoRootFlag = flag.String("repo-root", ".", "module root; resolved upward to the nearest go.mod")
		makeTarget   = flag.String("make-target", defaultMakeTarget, "make target named in the do-not-edit banner")
		toStdout     = flag.Bool("stdout", false, "write the single requested doc to stdout instead of docs/reference/ (requires exactly one subcommand)")
	)
	flag.Usage = usage
	flag.Parse()

	if err := run(flag.Args(), *repoRootFlag, *makeTarget, *toStdout); err != nil {
		fmt.Fprintf(os.Stderr, "loomdoc: %v\n", err)
		os.Exit(1)
	}
}

// run dispatches to one or all generators. Split from main so tests can drive
// it without touching os.Exit.
func run(args []string, repoRootFlag, makeTarget string, toStdout bool) error {
	if len(args) > 1 {
		return fmt.Errorf("expected at most one subcommand, got %d: %s", len(args), strings.Join(args, " "))
	}

	targets := genOrder
	if len(args) == 1 {
		if _, ok := generators[args[0]]; !ok {
			return fmt.Errorf("unknown subcommand %q (want one of: %s)", args[0], strings.Join(sortedKeys(), ", "))
		}
		targets = []string{args[0]}
	}
	if toStdout && len(targets) != 1 {
		return fmt.Errorf("-stdout requires exactly one subcommand (envvars, cli, or layers)")
	}

	cfg, err := newGenConfig(repoRootFlag, makeTarget)
	if err != nil {
		return err
	}
	for _, name := range targets {
		if err := generateOne(cfg, generators[name], toStdout); err != nil {
			return err
		}
	}
	return nil
}

// generateOne runs a single generator and either writes its doc or emits it to
// stdout.
func generateOne(cfg *genConfig, g generator, toStdout bool) error {
	doc, err := renderDoc(cfg, g)
	if err != nil {
		return err
	}
	if toStdout {
		_, err := os.Stdout.WriteString(doc)
		return err
	}
	return writeDoc(filepath.Join(cfg.RepoRoot, refDir, g.outName+".md"), doc)
}

// renderDoc is the pure, side-effect-free half of generateOne: the pipeline is
// identical for every subcommand — body → prepend the optional preamble →
// prepend the banner. Split out so determinism can be tested without writing.
func renderDoc(cfg *genConfig, g generator) (string, error) {
	body, err := g.run(cfg)
	if err != nil {
		return "", fmt.Errorf("%s: %w", g.outName, err)
	}
	preamble, err := readPreamble(cfg.RepoRoot, g.outName)
	if err != nil {
		return "", err
	}
	return assembleDoc(generatedHeader(g.sourceNote, cfg.MakeTarget), preamble, body), nil
}

// sortedKeys returns the subcommand names in sorted order for error messages.
func sortedKeys() []string {
	keys := make([]string, 0, len(generators))
	for k := range generators {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: loomdoc [flags] [envvars|cli|layers]")
	fmt.Fprintln(os.Stderr, "  With no subcommand, regenerates every reference doc under docs/reference/.")
	flag.PrintDefaults()
}
