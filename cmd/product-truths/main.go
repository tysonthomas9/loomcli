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
	flag.Parse()
	result := producttruth.Validate(*root, *registry, *catalog)
	fmt.Print(producttruth.Format(result))
	if len(result.Errors) != 0 {
		os.Exit(1)
	}
}
