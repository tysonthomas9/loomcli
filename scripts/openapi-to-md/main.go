// openapi-to-md generates docs/api.md from api/openapi.yaml.
//
// The spec is the source of truth for the endpoint reference because it is
// already gated against the committed Go types by
// scripts/check-go-api-staleness.sh, so the docs inherit that guarantee. Prose
// a spec cannot express (base URL, auth model, rate limits, error codes) lives
// in checked-in partials that this tool concatenates around the generated
// sections.
//
// The tool additionally scans internal/webui for net/http route registrations
// and emits a coverage appendix, because the spec is known to be an incomplete
// description of what actually serves traffic. See the "Spec Coverage vs
// Registered Routes" section of the generated document.
//
// Output is written to stdout. Deterministic: no timestamps, no map-iteration
// ordering, so running it twice produces byte-identical output.
//
// Usage:
//
//	go run ./scripts/openapi-to-md api/openapi.yaml > docs/api.md
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	var (
		preamblePath = flag.String("preamble", "docs/api.preamble.md", "checked-in markdown prepended to the generated reference")
		appendixPath = flag.String("appendix", "docs/api.appendix.md", "checked-in markdown appended after the generated reference")
		routesDir    = flag.String("routes", "internal/webui", "directory scanned for net/http route registrations")
		repoRoot     = flag.String("repo-root", ".", "repo root used to render route file paths")
		genTarget    = flag.String("make-target", "gen-api-docs", "make target named in the do-not-edit banner")
	)
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: openapi-to-md [flags] <spec.yaml>")
		flag.PrintDefaults()
		os.Exit(2)
	}

	out, err := run(flag.Arg(0), *preamblePath, *appendixPath, *routesDir, *repoRoot, *genTarget)
	if err != nil {
		fmt.Fprintf(os.Stderr, "openapi-to-md: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.WriteString(out); err != nil {
		fmt.Fprintf(os.Stderr, "openapi-to-md: write output: %v\n", err)
		os.Exit(1)
	}
}

// run builds the document. Split from main so tests can exercise it.
func run(specPath, preamblePath, appendixPath, routesDir, repoRoot, genTarget string) (string, error) {
	data, err := os.ReadFile(specPath) //nolint:gosec // G304: caller-controlled spec path
	if err != nil {
		return "", fmt.Errorf("read spec: %w", err)
	}
	spec, err := parseSpec(data)
	if err != nil {
		return "", err
	}

	preamble, err := readPartial(preamblePath)
	if err != nil {
		return "", err
	}
	appendix, err := readPartial(appendixPath)
	if err != nil {
		return "", err
	}

	routes, mounts, err := scanRoutes(routesDir, repoRoot)
	if err != nil {
		return "", err
	}

	r := &renderer{
		spec:      spec,
		drift:     compareRoutes(spec.operations(), routes, mounts),
		genTarget: genTarget,
	}
	return r.render(preamble, appendix), nil
}

// readPartial loads a checked-in prose partial. A missing partial is a hard
// error: silently dropping the hand-written sections is exactly the failure
// mode this generator exists to prevent.
func readPartial(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path) //nolint:gosec // G304: caller-controlled partial path
	if err != nil {
		return "", fmt.Errorf("read partial %s: %w", path, err)
	}
	return string(b), nil
}
