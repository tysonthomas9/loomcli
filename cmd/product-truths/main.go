package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tysonthomas9/loomcli/internal/producttruth"
)

func main() {
	root := flag.String("root", ".", "repository root")
	registry := flag.String("registry", "docs/qa/product-invariants.yaml", "invariant registry")
	catalog := flag.String("catalog", "docs/qa/feature-user-stories.tsv", "user-story catalog")
	scenarioMap := flag.String("scenario-map", "tests/aft/coverage/scenario-map.yaml", "AFT scenario map")
	flag.Parse()
	result := producttruth.Validate(*root, *registry, *catalog)
	scenarioResult := producttruth.ValidateScenarioMap(*root, *scenarioMap)
	fmt.Print(producttruth.Format(result))
	fmt.Print(producttruth.FormatScenarioMap(scenarioResult))
	if len(result.Errors) != 0 || len(scenarioResult.Errors) != 0 {
		os.Exit(1)
	}
}
